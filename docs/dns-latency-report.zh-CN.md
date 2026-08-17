# KubeLoop macOS TUN DNS 延迟技术报告

## 1. 摘要

- 测试日期：2026-08-17（Asia/Shanghai）
- 测试对象：KubeLoop Desktop 开发环境，macOS TUN 模式
- 测试结论：修复后未再复现约 3 秒的 Kubernetes DNS 延迟；跨过 30 秒缓存周期后延迟保持稳定。
- 5 分钟监测中，保留的 28 次有效采样全部返回 HTTP 200；DNS 正查询最大 30 ms，负查询最大 23 ms，HTTP 总耗时最大 22.644 ms。
- macOS 搜索域、helper 进程和 sing-box 进程在监测期间保持稳定。

## 2. 问题现象

通过 TUN 访问 `my-service.default.svc` 时，连接有时很快，但首次访问或间隔一段时间后可能重新出现明显延迟。修复前的现场测量包括：

| 场景 | 修复前耗时 |
|---|---:|
| `my-service.default.svc` A 首次查询 | 约 30 ms |
| `my-service.default.svc` AAAA 首次查询 | 约 3015 ms |
| 完整 FQDN 查询 | 约 1–4 ms |
| 不存在的 `.svc` A/AAAA 查询 | 约 3020 ms |
| 系统解析正查询首次 | 约 3.05 s |
| 系统解析负查询首次 | 约 6.10 s |

延迟集中在不完整的 `.svc` 名称、AAAA 查询和负查询，并在缓存失效后重新出现。

## 3. 根因分析

### 3.1 `.svc` 名称错误回退到公共 DNS

DNS 搜索代理在处理 `my-service.default.svc` 时，除生成规范候选
`my-service.default.svc.cluster.local.` 外，还保留了原始 `.svc` 候选。原始候选可能被转发到本地或公共 DNS，触发约 3 秒的 UDP 超时。

### 3.2 NODATA 后继续搜索

上游返回 `NOERROR` 但 Answer 为空时，代理继续尝试后续候选。对于已经明确返回 NODATA 的名称，这会产生无意义的额外查询和超时。

### 3.3 搜索域范围过大

会话配置曾把集群中的多个 namespace 都加入搜索域，增加了查询候选数量。修复后仅保留当前 namespace、`svc.<cluster-domain>` 和集群根域，并使用 `ndots:1`。

### 3.4 macOS 搜索域残留

macOS helper 写入搜索域时，会把新搜索域与网络服务当前值合并。当前值中可能已包含旧 KubeLoop 会话注入的 `*.svc.cluster.local`，导致旧 namespace 搜索域被反复保留。

修复后，活动连接会过滤同一集群的旧 Kubernetes 搜索域，同时：

- 保留用户原有的非 Kubernetes 搜索域；
- 原样保存网络配置备份；
- 断开连接时仍按原始备份恢复。

## 4. 修复内容

| 文件 | 修复 |
|---|---|
| `internal/singbox/runtime/dns_search_proxy.go` | `.svc` 名称只生成规范集群 FQDN；NODATA 立即返回 |
| `internal/singbox/session_spec.go` | 搜索域收敛到当前 namespace；设置 `ndots:1` |
| `internal/helper/platform/search_domains.go` | 提供 macOS/Windows 共用的集群根域识别和安全 PowerShell 脚本生成 |
| `internal/helper/platform/lifecycle_darwin.go` | 过滤旧 Kubernetes 搜索域，保留非 Kubernetes 搜索域和原始恢复备份 |
| `internal/helper/platform/lifecycle_windows.go` | 写入全局搜索域前过滤同一集群的旧 Kubernetes 后缀，保留其他搜索域和断开恢复备份 |
| 对应测试文件 | 增加 NODATA、候选生成、SessionSpec 和 macOS 搜索域合并回归测试 |

## 5. 测试环境

| 项目 | 值 |
|---|---|
| 操作系统 | macOS 26.5.2，ARM64 |
| Kubernetes | v1.35.1 |
| KubeLoop | development build |
| 连接模式 | TUN |
| 测试服务 | `my-service.default.svc` |
| Service ClusterIP | `10.109.67.207` |
| 测试期间本地 DNS | `127.0.0.1:51733` |
| DNS 搜索域 | `default.svc.cluster.local`, `svc.cluster.local`, `cluster.local` |
| helper PID | `22043` |
| sing-box PID | `22196` |

DNS 端口和 PID 是本次开发会话的临时值，重新连接后可能变化。

## 6. 测试方法

### 6.1 即时验证

1. 使用 `dig` 直接查询 KubeLoop 本地 DNS，分别测试存在和不存在的 `.svc` 名称。
2. 使用 `dscacheutil` 测试 macOS 系统解析路径。
3. 使用 `curl` 通过系统解析和 TUN 访问测试 Service。
4. 检查 `/etc/resolver/svc`、`scutil --dns` 和 Wi-Fi 搜索域。

### 6.2 缓存周期验证

等待 35 秒，跨过此前观察到的约 30 秒缓存周期，再次执行系统正查询、负查询和 HTTP 请求。

### 6.3 五分钟连续监测

- 时间：20:10:44–20:15:39
- 计划采样：30 次
- 间隔：10 秒
- 保留明细：28 次
- 每次采集：直接 DNS 正/负查询、系统正/负解析、HTTP 状态与耗时、搜索域、helper PID、sing-box PID

第 27、28 次采样输出因交互界面中断未被保留；监测进程继续运行并完成第 29、30 次采样。因此统计仅使用保留的 28 次记录，不推测缺失样本的值。

## 7. 测试结果

### 7.1 即时与缓存过期验证

| 场景 | 修复后耗时/结果 |
|---|---:|
| 直接 DNS 正查询 | 约 20 ms |
| 直接 DNS 负查询 | 约 10 ms |
| 系统 DNS 正查询 | 约 20 ms |
| HTTP 首轮 | 200，约 13.873 ms |
| 35 秒后系统正查询 | 约 40 ms |
| 35 秒后系统负查询 | 约 20 ms |
| 35 秒后 HTTP | 200，约 14.314 ms |

缓存过期后没有恢复到修复前约 3 秒或 6 秒的延迟。

### 7.2 五分钟统计

P95 使用 nearest-rank 方法计算。系统解析耗时由 `/usr/bin/time -p` 采集，分辨率为 10 ms，因此系统解析平均值仅用于趋势判断。

| 指标 | 平均 | 最小 | P95 | 最大 |
|---|---:|---:|---:|---:|
| 直接 DNS 正查询 | 6.54 ms | 1 ms | 18 ms | 30 ms |
| 直接 DNS 负查询 | 4.32 ms | 1 ms | 13 ms | 23 ms |
| 系统 DNS 正查询 | 4.6 ms | <10 ms | 10 ms | 10 ms |
| 系统 DNS 负查询 | 1.4 ms | <10 ms | 10 ms | 10 ms |
| HTTP 总耗时 | 15.549 ms | 8.995 ms | 20.605 ms | 22.644 ms |

### 7.3 稳定性

| 检查项 | 结果 |
|---|---:|
| 保留的 HTTP 成功样本 | 28/28 |
| HTTP 非 200 | 0 |
| DNS 超时（≥1 s） | 0 |
| 约 3 秒延迟复现 | 0 |
| 搜索域变化 | 0 |
| helper PID 变化 | 0 |
| sing-box PID 变化 | 0 |

## 8. 原始保留采样

单位：DNS 为 ms，系统解析和 HTTP 为 s。

| # | 时间 | DNS 正 | DNS 负 | 系统正 | 系统负 | HTTP | HTTP 总耗时 |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 | 20:10:44 | 6 | 9 | 0.01 | 0.01 | 200 | 0.022644 |
| 2 | 20:10:54 | 2 | 2 | 0.00 | 0.00 | 200 | 0.018400 |
| 3 | 20:11:04 | 4 | 2 | 0.00 | 0.00 | 200 | 0.017403 |
| 4 | 20:11:15 | 9 | 13 | 0.00 | 0.00 | 200 | 0.019478 |
| 5 | 20:11:25 | 4 | 1 | 0.01 | 0.00 | 200 | 0.012985 |
| 6 | 20:11:35 | 5 | 1 | 0.00 | 0.00 | 200 | 0.018737 |
| 7 | 20:11:45 | 30 | 9 | 0.01 | 0.00 | 200 | 0.012561 |
| 8 | 20:11:55 | 4 | 1 | 0.01 | 0.00 | 200 | 0.016008 |
| 9 | 20:12:06 | 4 | 3 | 0.01 | 0.01 | 200 | 0.014972 |
| 10 | 20:12:16 | 8 | 4 | 0.00 | 0.00 | 200 | 0.020605 |
| 11 | 20:12:26 | 17 | 1 | 0.01 | 0.00 | 200 | 0.013797 |
| 12 | 20:12:36 | 2 | 2 | 0.00 | 0.00 | 200 | 0.012203 |
| 13 | 20:12:46 | 7 | 3 | 0.00 | 0.00 | 200 | 0.014090 |
| 14 | 20:12:56 | 4 | 2 | 0.01 | 0.00 | 200 | 0.018345 |
| 15 | 20:13:07 | 1 | 2 | 0.00 | 0.00 | 200 | 0.012964 |
| 16 | 20:13:17 | 12 | 4 | 0.00 | 0.00 | 200 | 0.011479 |
| 17 | 20:13:27 | 2 | 2 | 0.01 | 0.01 | 200 | 0.016989 |
| 18 | 20:13:37 | 18 | 2 | 0.00 | 0.00 | 200 | 0.012844 |
| 19 | 20:13:47 | 8 | 10 | 0.00 | 0.00 | 200 | 0.018229 |
| 20 | 20:13:58 | 3 | 3 | 0.01 | 0.00 | 200 | 0.014878 |
| 21 | 20:14:08 | 2 | 3 | 0.00 | 0.00 | 200 | 0.018101 |
| 22 | 20:14:18 | 9 | 6 | 0.00 | 0.00 | 200 | 0.008995 |
| 23 | 20:14:28 | 1 | 1 | 0.01 | 0.00 | 200 | 0.013834 |
| 24 | 20:14:38 | 2 | 1 | 0.00 | 0.00 | 200 | 0.016385 |
| 25 | 20:14:48 | 9 | 8 | 0.01 | 0.01 | 200 | 0.013040 |
| 26 | 20:14:59 | 4 | 23 | 0.01 | 0.00 | 200 | 0.017229 |
| 29 | 20:15:29 | 1 | 2 | 0.01 | 0.00 | 200 | 0.014913 |
| 30 | 20:15:39 | 5 | 1 | 0.00 | 0.00 | 200 | 0.013270 |

所有保留样本的搜索域均为：

```text
default.svc.cluster.local
svc.cluster.local
cluster.local
```

所有保留样本的 helper PID 均为 `22043`，sing-box PID 均为 `22196`。

## 9. 代码验证

以下检查均通过：

```text
gopls check
go test ./internal/singbox/... ./internal/helper/platform ./internal/helper -count=1
go test -race ./internal/singbox/... ./internal/helper/platform ./internal/helper -count=1
GOOS=windows GOARCH=amd64 go test -exec=/usr/bin/true ./internal/helper/platform
GOOS=windows GOARCH=arm64 go test -exec=/usr/bin/true ./internal/helper/platform
git diff --check
```

## 10. 结论与限制

本次修复消除了 `.svc` 查询向公共 DNS 回退造成的约 3 秒超时，并防止 macOS 和 Windows 搜索域在重连后持续累积。正查询、负查询、系统解析和 HTTP 请求在缓存过期前后均保持低延迟，5 分钟窗口内未观察到周期性退化。

本报告的实时网络测试只覆盖单台 macOS 开发机、单个 Kubernetes Service 和 5 分钟窗口，不等同于长期压力测试。Windows 当前完成了单元测试和 amd64/arm64 交叉构建，尚未进行 Windows 实机 PowerShell 与网络栈验证。发布前建议在 CI 或专用环境增加更长时间的监测，并覆盖 Windows 实机、多个 namespace、AAAA、NXDOMAIN、休眠唤醒和网络切换场景。
