# KubeLoop Operator

Operator 是 KubeLoop 根项目中的 Kubernetes 控制面组件，不是独立仓库或嵌套 Go module。
它与 Control Plane、Gateway 共用根目录的 `go.mod`、版本号、CI 和 Helm 发布流程，但以独立进程和
ServiceAccount 部署，以便隔离 Kubernetes 资源协调权限。

根目录的 `PROJECT` 保存 Kubebuilder 生成器元数据；CRD API 位于 `api/v1alpha1`，Controller 位于
`internal/controller/`，生成清单位于根 `config/`，端到端测试位于 `e2e/operator/`。Go import path
仍属于根 module `github.com/fengqi-dev/kube-loop`。

进程入口统一放在根项目 `cmd/`：`cmd/kubeloop-control-plane` 运行 HTTP API、认证和任务控制面，
`cmd/kubeloop-operator` 只运行 Kubernetes Controller。Operator API 和 Controller 按 Kubebuilder
标准目录组织，部署与生成配置由根项目统一管理。

## TrafficBinding

`traffic.kubeloop.io/v1alpha1` 的 `TrafficBinding` 是 namespaced CRD，统一描述四种流量任务：

- `PortForward`：校验 Pod 或 Service 目标，不创建或改写 Service/EndpointSlice。
- `Preview`：创建由 CR 持有的 ClusterIP Service 和 EndpointSlice。
- `Exchange`：保存原 Service 端点快照后，将流量切换至 Gateway relay；删除 CR 时恢复。
- `Mirror`：使用相同的可恢复 Kubernetes 资源生命周期，数据平面的复制语义由 Gateway 管理。

Operator 只接受 Control Plane 写入的可信 relay 地址。客户端凭证、OAuth/OIDC token 和本地桌面
地址不会写入 CRD。

## Control Plane 与 Operator 的边界

Control Plane 的 `internal/controlplane/trafficbindingclient` 是 CR 生命周期客户端。四类任务先写入
持久化 Task，再以 Task UUID 创建不可变的 `TrafficBinding`，等待 Operator 回写与当前
`metadata.generation` 对应的 `Ready=True` 后才进入 `running`。`Degraded=True` 会作为任务启动失败
返回；停止任务时，Control Plane 删除 CR 并等待对象消失，因此 Operator finalizer 完成恢复或清理
后，Task 才能进入终态。

Exchange 和 Mirror 的 Service/Endpoints/EndpointSlice 快照仍由用户授权的只读 Kubernetes
客户端采集，但所有资源写入与恢复只由 Operator 执行。Preview 的 Service/EndpointSlice 创建和
删除、Port Forward 目标的最终校验同样经由 CRD。Control Plane 的周期回收器会删除已无持久化 Task、
Task 已终止或所有权不匹配的 CR，补上数据库级联删除与 CR 删除之间的崩溃窗口。

Helm 默认同时部署 `kubeloop-control-plane`、`kubeloop-gateway` 和 `kubeloop-operator`。Control Plane
ServiceAccount 只拥有 `TrafficBinding` 的 create/get/list/watch/delete 以及流量快照所需的只读
权限；Operator ServiceAccount 独占 Service、Endpoints、EndpointSlice 的写权限和
`TrafficBinding/status`、`TrafficBinding/finalizers` 权限。

## 根项目中的开发命令

从仓库根目录执行：

```sh
make operator-manifests operator-generate
make operator-test
make operator-build
make operator-docker-build OPERATOR_IMG=ghcr.io/fengqi-dev/kube-loop/operator:tag
```

Docker 构建上下文固定为仓库根目录，因此镜像使用根 `go.mod`。CRD 和 RBAC 文件由
`controller-gen` 生成，不直接编辑 `config/crd/bases` 或 `config/rbac/role.yaml`。

根项目的应用控制面位于 `internal/controlplane`；Operator 的 Kubernetes Controller 位于
`internal/controller`，两者职责和包名保持明确分离。根目录的
`cmd/kubeloop-operator` 可以导入根 `internal` 范围内的实现。

生产安装由根项目的 Helm chart 统一完成；根 `config/` 仅用于 CRD/RBAC 生成、开发测试和调试部署。
