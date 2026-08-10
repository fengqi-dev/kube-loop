# V2 Kubernetes 调用点与迁移归属

本文档是 V2-010 的权威盘点。范围是 `cmd`、`internal` 与 `operator` 下所有非测试 Go 文件对 `k8s.io/*` 的直接依赖，以及这些依赖最终产生的 Kubernetes API、watch 或 SPDY 流。测试夹具和 `e2e` 不计入生产调用点。

`internal/architecture.TestKubernetesDirectImportInventoryIsExhaustive` 会扫描生产源码；新增或删除任何直接 Kubernetes 依赖包时，必须同时评审并更新本文档。独立的依赖图测试继续保证桌面组合根、`clientv2`、MCP 和 Data Plane 不依赖 Kubernetes SDK。

## 边界结论

- V2 桌面客户端只连接用户填写的服务地址。当前桌面可执行依赖图不包含 `k8s.io/*`、`internal/cluster` 或 `internal/session`，不读取 kubeconfig，也不创建 Kubernetes REST client。
- `kubeloop-controller` 是 API/认证/Task 控制面，负责用户态读取、SSAR、exec/file 等流式调用以及创建 `TrafficBinding`；启用 impersonation 时，面向用户的调用使用由认证 Principal 派生的 client。
- `kubeloop-operator` 是独立进程入口，只 watch `TrafficBinding` 并协调 Service、Endpoints 和 EndpointSlice。它使用独立 ServiceAccount，不能处理 OAuth token、客户端 WSS 或普通业务 API。
- `kubeloop-gateway` Data Plane 不依赖 `k8s.io/*`，且不自动挂载通用 Kubernetes API credential。默认 Registry 模式只显式投影短期、限定 `kubeloop-relay` audience、Pod-bound 的 ServiceAccount Token，用于 Controller TokenReview 工作负载认证；它不能替代 RelayTicket，也不授予 Data Plane 调用 Kubernetes API 的能力。数据流仍只按 Controller 签发的 RelayTicket 和 NetworkSpec 处理已授权网络流。
- Helper 只管理本机 TUN、路由和 DNS，不接触 Kubernetes。Helm 负责统一安装/升级 Controller、Gateway、Operator 三个服务端组件，桌面不再创建 Namespace、Deployment、Secret、Service 或 Ingress。
- `internal/cluster` 及其子包是隔离的 V1 实现，已不在桌面生产组合根中；保留期间只用于旧实现测试和迁移对照，不能重新接入 V2 Session。

## 直接 Kubernetes 依赖清单

| 包 | 层级 | 直接依赖/调用 | 类型 | V2 归属或处理 |
| --- | --- | --- | --- | --- |
| `cmd/kubeloop-controller` | V2 Controller 组合根 | 取得 `SystemClient` 并装配 Relay Registry 的 TokenReview/Pod 拓扑认证 | 凭据装配 | 只在 Controller 进程中连接 Kubernetes；Data Plane 不接收 client 或 REST config |
| `cmd/kubeloop-operator` | V2 Operator 组合根 | 装配 controller-runtime Manager、TrafficBinding scheme 和 Reconciler | 凭据装配 | 只运行声明式 Kubernetes 资源协调，不暴露用户 API |
| `internal/cluster` | V1 遗留 | kubeconfig/clientcmd、REST config、Namespace/Pod/Service list/get、EndpointSlice、SSAR、ServerVersion | 控制面 | 已由 `clientv2` → Controller API 取代；生产桌面不可达 |
| `internal/cluster/discovery` | V1 遗留 | Node、Pod、Service、Deployment、ConfigMap 探测及临时 Service probe | 控制面 | 由 `controller/networkapi` 生成 Session NetworkSpec |
| `internal/cluster/gatewayruntime` | V1 遗留 | Namespace、Deployment、Secret、Service 的 get/create/update，生成 Ingress 清单，发现 Gateway Pod | 控制面写 | 由 Helm 安装和运维，不允许桌面执行 |
| `internal/cluster/inventory` | V1 遗留 | Pod、Service、Deployment shared informer/list-watch | 长连接 | V2 使用分页 HTTPS Inventory；后续若需要 watch，只能由 Controller 暴露受权事件流 |
| `internal/cluster/kubeportforward` | V1 遗留 | client-go SPDY port-forward | 流式 | V2 Controller 解析目标并授权，Data Plane WSS 传输 TCP/UDP，不在桌面创建 SPDY 流 |
| `internal/cluster/podexec` | V1 遗留 | `pods/exec` SPDY、TTY resize | 流式 | 由 `controller/execapi` 终止认证 WSS 并创建 SPDY exec |
| `internal/intercept/clusteradapter` | V1 遗留适配 | Kubernetes Service/EndpointSlice 类型转换 | 类型适配 | V2 Exchange/Mirror 直接使用 Controller handler 与共享 `servicebinding`；桌面不可达 |
| `internal/controller/kubernetes` | V2 Controller | in-cluster config、typed client、可选 impersonation、`/version` readiness、system compensation client | 凭据入口 | 唯一 V2 Kubernetes client 工厂 |
| `internal/controller/kubeapi` | V2 Controller | `/version`、Namespace/Pod/Service list/get、SelfSubjectAccessReview | 控制面 | HTTPS API；Policy 后再执行 Kubernetes 授权检查 |
| `internal/controller/networkapi` | V2 Controller | Node/ServiceCIDR、namespace Pod/Service，以及固定名称的 kube-dns/CoreDNS Service 与 ConfigMap | 控制面 | 创建 Cluster Session 前发现并规范化 NetworkSpec；namespace 资源使用 principal client，集群网络元数据使用受限的 Controller system client |
| `internal/controller/portforwardapi` | V2 Controller | Pod/Service get，解析 PodIP/ClusterIP 与端口 | 控制面 | 只解析并授权目标；实际字节由 Data Plane WSS 转发 |
| `internal/controller/trafficbindingclient` | V2 Controller | 使用 Controller ServiceAccount 创建、读取、等待和删除 `TrafficBinding`；清理数据库中已不存在或已终态 Task 的孤儿 CR | 声明式控制面 | 不直接写 Service/Endpoints/EndpointSlice；Task 先持久化，CR Ready 后才开放数据流，删除等待 Operator finalizer 完成 |
| `internal/controller/execapi` | V2 Controller | Pod get、`pods/exec` SPDY、stdin/stdout/stderr/TTY resize | 流式 | Controller WSS 所有者持有 SPDY 流和授权 lease |
| `internal/controller/fileapi` | V2 Controller | Pod get，复用固定生成命令的 exec executor | 流式 | Controller WSS 上的文件协议；Pod 侧通过受限 exec/tar |
| `internal/controller/fileopsapi` | V2 Controller | Kubernetes 名称校验；复用 file executor 执行 list/create/rename/delete | 控制面加短流 | Task、Policy、SSAR 和路径边界均由 Controller 执行 |
| `internal/controller/exchangeapi` | V2 Controller | 只读捕获 Service/EndpointSlice 快照，分配反向 listener，并通过 `TrafficBinding` 请求接管/恢复 | 控制面读加长连接 | Controller 拥有反向 WSS、listener 和授权 lease；Operator 独占资源写入与 finalizer 恢复 |
| `internal/controller/mirrorapi` | V2 Controller | 只读捕获 Service/EndpointSlice 和原 backend，分配 mirror listener，并通过 `TrafficBinding` 请求接管/恢复 | 控制面读加长连接 | Controller 保持 primary/shadow 数据流；Operator 独占资源写入，Data Plane 不持有 Kubernetes client |
| `internal/controller/previewapi` | V2 Controller | 持久化 cleanup intent，通过 `TrafficBinding` 请求 Preview Service，并读取 CR status 中的 ClusterIP | 声明式控制面加长连接 | Controller 不直接创建/删除 Service 或 EndpointSlice；停止与失主恢复删除 CR 并等待 finalizer |
| `internal/controller/relayregistry` | V2 Controller | TokenReview、按 UID/ServiceAccount 查询 Pod、读取 Node region/zone/hostname | 工作负载认证与控制面 | 只信任短期专用 audience 的 Pod-bound token 或 mTLS/SPIFFE；Relay ID 与拓扑均由权威身份派生，不接受注册 body 自报 |
| `internal/servicebinding` | 迁移期共享类型与只读快照 | Service、Endpoint、EndpointSlice 快照类型、原 backend 解析及隔离的 V1 写入参考实现 | 类型/控制面读 | V2 Controller 生产路径只使用 Capture/解析和快照类型；Service/Endpoint 写入已经迁到 Operator |
| `internal/controller/sessionapi` | V2 Controller | 仅 Kubernetes DNS 名称校验工具 | 类型/校验 | 不发起 API 请求；可在后续移除该轻量依赖 |
| `internal/operator/api/v1alpha1` | V2 Operator API | TrafficBinding CRD 类型、Service/EndpointSlice 回滚状态 | API 类型 | Controller 与 Operator 共享的声明式契约，不包含认证信息 |
| `internal/operator/trafficbinding` | V2 Operator Reconciler | PortForward 目标校验；Preview Service 创建；Exchange/Mirror Service 和端点接管、快照、恢复 | 声明式控制面写 | 使用 Operator ServiceAccount，按 finalizer 保证恢复；不处理数据流 |

`internal/controller/fileopsapi` 与 `sessionapi` 目前只直接使用 `apimachinery` 校验代码；它们仍列入白名单，避免“只引用工具包”成为未评审引入 Kubernetes SDK 的入口。

## 功能迁移归属

| V1 功能 | V1 入口 | V2 控制面所有者 | V2 流所有者 | 当前状态 |
| --- | --- | --- | --- | --- |
| Namespace | `cluster.Provider.Namespaces` | `controller/kubeapi` | 无 | 已迁移 |
| Inventory | `ListServices`、`ListPods`、`inventory.Watch` | `controller/kubeapi` 分页 API | 无；当前不开放 watch | 已迁移 |
| ServerVersion/Capability | `ServerVersion`、`ProbeCapabilities` | `controller/kubeapi` | 无 | 已迁移；版本化能力文档绑定 principal/namespace/Gateway version，客户端使用 30 秒有界内存缓存 |
| 网络发现 | `cluster/discovery.Discover` | `controller/networkapi` | 无 | 已迁移到 Session NetworkSpec |
| Gateway 安装与发现 | `EnsureGateway`、`gatewayruntime.Ensure*` | Helm release | Data Plane 自身 | 已移出桌面；Helm 完整验收属于 V2-200～205 |
| Service/Pod Port Forward | `StartPortForward`、`kubeportforward.Start` | `controller/portforwardapi` → `TrafficBinding` → Operator 校验 | Data Plane WSS | 已接入；Task 持久化后等待 CR Ready，停止/孤儿回收删除 CR |
| Exchange/Intercept | `ApplyServiceIntercept`、Intercept Manager | `controller/exchangeapi` → `TrafficBinding` → Operator | Controller reverse WSS | 已接入；Operator status 持久化快照并以 finalizer 恢复 |
| Mirror/Intercept | Intercept Manager + mirror | `controller/mirrorapi` → `TrafficBinding` → Operator | Controller reverse WSS | 已接入；原 backend 只读快照仍由 Controller 用于 primary，资源接管归 Operator |
| Preview | `CreatePreviewService` | `controller/previewapi` → `TrafficBinding` → Operator | Controller reverse WSS | 已接入；ClusterIP 从 CR status 返回，删除等待 Operator 完成资源清理 |
| Pod exec/TTY | `cluster.Provider.Exec`、`podexec.Exec` | `controller/execapi` | Controller WSS ↔ Kubernetes SPDY | 已迁移 |
| 文件上传/下载 | V1 file manager + Pod exec/tar | `controller/fileapi` | Controller WSS ↔ Kubernetes SPDY | 已迁移 |
| 文件 list/create/rename/delete | V1 file manager | `controller/fileopsapi` | Controller 短时 exec | 已迁移 |

## 调用规则

1. 所有用户触发的 Kubernetes 调用必须先完成 Access Token、Token Family、Principal、Gateway Policy、Cluster Session/namespace 所有权校验；有对应 Kubernetes verb 时再执行 SSAR。
2. Controller 使用 `ClientFor(subject)` 完成用户意图解析和授权；Operator 只接受已经授权并写入的 `TrafficBinding`，使用自己的 ServiceAccount 执行可恢复的声明式写入。
3. 流式调用由创建流的 Controller 副本持有授权 lease；Token 撤销、客户端断开或 shutdown 必须取消 Kubernetes stream。Data Plane 只能处理网络数据流，不能创建 exec/file Kubernetes stream。
4. Kubernetes 对象写入必须先持久化 Task/TrafficBinding 与 rollback/cleanup intent。Operator 在实际变更前将快照写入 CR status，并通过 finalizer 重试恢复。
5. 客户端提交的地址、Pod、Service、container 和端口都只是请求意图；Controller 必须从 Kubernetes 权威对象重新解析并将最终目标绑定到 Task/RelayTicket。

## 验证命令

```bash
go test ./internal/architecture
go list -deps .
go list -json ./... | jq -r 'select(any(.Imports[]?; startswith("k8s.io/"))) | .ImportPath'
```

第一条命令保证本清单与源码直接依赖集合一致，并阻止 Kubernetes 运行时回流桌面、类型化客户端或 Data Plane；第二条命令的桌面依赖图不得出现 `k8s.io/*`。
