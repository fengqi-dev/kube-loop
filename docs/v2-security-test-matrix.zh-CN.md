# KubeLoop V2 安全测试矩阵

本矩阵记录 V2 Gateway、Controller、协议与桌面客户端的安全回归入口。默认测试不需要 Kubernetes 集群；Minikube E2E 在全部代码完成后统一执行。

| 攻击面 | 主要控制 | 回归测试 |
| --- | --- | --- |
| OAuth/OIDC callback 篡改与重放 | loopback callback 白名单、state/nonce/PKCE、一次性交换码、Provider 绑定 | `internal/controller/authn/login/service_test.go`、`internal/controller/authn/httpauth/handler_test.go`、`internal/controller/authn/oidc/provider_test.go` |
| Refresh Token replay | 单次旋转、并发 CAS、整族撤销、重启后保留撤销状态 | `internal/controller/authn/token/service_test.go` |
| JWT confusion | Access Token 仅接受 EdDSA，并绑定 `kid`、`typ=JWT`、issuer、audience、时间和 token family | `internal/controller/authn/token/service_test.go` |
| AD/LDAP 注入与密码喷洒 | LDAP filter 转义、密码 buffer 清零、账号 5/min 与客户端 30/min 双限流、统一失败响应 | `internal/controller/authn/ad/provider_test.go`、`internal/controller/authn/httpauth/handler_test.go` |
| 跨用户 IDOR 与 stream 越权 | 统一 Authorizer 前置；Session、Task、Stream 同时绑定 principal/session/namespace；无权访问统一返回 403/404 | `internal/controller/api_test.go`、各 `*api/handler_test.go` 与 `*api/stream_test.go` |
| SSRF 与 Origin 混淆 | Gateway URL 必须为可信 HTTP(S) origin；禁止重定向、跨 Origin discovery、URL userinfo/query/fragment；OIDC issuer/redirect 与 AD TLS 配置 fail closed | `internal/clientv2/discovery/client_test.go`、`internal/controller/server_test.go`、`internal/controller/authn/oidc/provider_test.go`、`internal/controller/authn/ad/provider_test.go` |
| 路径穿越与归档攻击 | 绝对远程根校验、清理路径、拒绝 `..`、symlink/hardlink/device、限制归档条目/大小/输出 | `internal/controller/fileapi/path_test.go`、`executor_test.go`、`handler_test.go`、`fileopsapi/operator_test.go` |
| 请求与 WSS 资源耗尽 | HTTP header 64 KiB；API body 1 MiB 可配置；auth body 16 KiB；协议帧固定最大值；WebSocket compression 关闭 | `internal/controller/server_test.go`、`api_test.go`、`authn/httpauth/handler_test.go`、各 stream/protocol test |
| 敏感信息泄露 | API 错误、readiness、audit、数据库错误和任务结果使用稳定脱敏消息；不记录 token、claims、命令、内容或 DSN Secret | `internal/controller/api_test.go`、`server_test.go`、`audit_test.go`、`storage/*_test.go` 与各任务测试 |

## Fuzz 入口

以下入口可以独立持续运行，且已纳入普通 `go test` 的 seed corpus：

```bash
go test ./internal/controller \
  -run '^$' -fuzz '^FuzzGatewayHTTPEntryBoundedAndRedacted$' -fuzztime=30s

go test ./internal/protocol/execstream \
  -run '^$' -fuzz '^FuzzWebSocketExecFrameDecode$' -fuzztime=30s
```

HTTP fuzz 验证任意路径、Content-Type 和 JSON 字节不会造成未恢复 panic、无界响应或 bearer/body 泄露。WSS fuzz 验证 Pod Exec 二进制帧 decoder 对任意输入保持有界，并保证所有成功解码帧可无损重新编码。生产连接另在 WebSocket 层设置 `MaximumPayload + 1` 的读上限。
