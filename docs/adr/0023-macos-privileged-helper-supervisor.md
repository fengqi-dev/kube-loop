# ADR 0023：macOS 特权 Helper Supervisor 与无重复授权更新

- 状态：Accepted（dev Phase 0-4 已实现；release Phase 5 待完成）
- 日期：2026-08-14
- 决策范围：macOS Desktop、开发/E2E helper 更新、正式包升级

## 实施状态（2026-08-14）

已落地独立的 dev/release Supervisor 身份、4-byte 大端有界 framing、严格 JSON、
peer UID + 256-bit token 鉴权、固定操作与路径、Worker manifest/hash/identity 校验、
活动 Session 拒绝、跨进程更新锁、journal、current/previous 原子切换、launchd
重启、20 秒 readiness 和回滚。`EnsureCurrentInstall` 与 E2E 已切换为：首次或
Supervisor 自身摘要变化时请求系统授权，Supervisor 不变时通过 Socket 更新 Worker。

macOS 实机回归确认：首次安装后，byte-distinct dev Worker 从摘要
`e2fa0878…` 无密码更新为 `dbf561fe…`，运行状态为 protocol 6、CoreReady；提交
合法 Mach-O 但错误组件身份的二进制在激活前被拒绝，原 Worker PID/摘要和运行状态
保持不变，未产生 update journal。release 固定 Team ID/designated requirement、
notarization 与 `SMAppService` 仍属于 Phase 5，不能把当前 dev owner-trusted 策略视为
正式发布验收完成。

## 背景

KubeLoop Desktop 需要 root helper 管理 sing-box、TUN、路由和 split DNS。当前
macOS 安装链路把内嵌的 `kubeloop-helper` 暂存后，通过 AppleScript
`do shell script ... with administrator privileges` 执行 helper 的 `install`
子命令。安装过程停止 launchd 服务、替换
`/Library/PrivilegedHelperTools/<label>`、更新系统侧认证文件和 plist，随后等待
helper `ping` 就绪。

这个模型适合首次安装，但把“建立特权信任关系”和“更新频繁变化的 Worker”合并为
同一操作。开发或 E2E 每次重新编译 helper 都会改变 SHA-256；严格安装发现内嵌
helper 与已安装 helper 不一致后再次进入 AppleScript 提权，因此无法形成无人值守
的 helper 测试循环。

当前工作区已经区分：

- 自动 TUN 启动可以在开发构建中复用健康且协议兼容的 helper；
- 显式安装和 E2E `ensure-helper` 仍要求安装本次内嵌 helper。

这个区分可以减少无关弹窗，但不能验证新 helper 代码。长期需要把稳定的特权更新
能力与频繁变化的运行 Worker 分离。

## 目标

1. macOS 首次安装时由用户完成一次管理员授权。
2. Supervisor 不变时，后续 Worker helper 更新不再请求管理员密码。
3. 开发/E2E 可以自动安装刚构建的 dev helper，并验证实际运行的是该构建。
4. 正式包只接受受信发布身份签名的 release helper。
5. 更新必须具备有界输入、调用者认证、完整性校验、原子切换、健康检查和自动回滚。
6. dev 与 release 的服务、状态、Socket、签名策略和安装路径必须隔离。
7. Supervisor 不能成为任意 root 命令、任意文件写入或任意路径安装接口。

## 非目标

- 不让 MCP、Desktop 或测试程序读取、保存或填写管理员密码。
- 不让 `sudoers NOPASSWD` 成为产品安装要求。
- 不实现通用软件更新器、通用 root shell 或包管理器。
- 不保证更新 Worker 时已有 TUN Session 无中断迁移；首版默认在无活跃 Session 时更新。
- 不允许 release Supervisor 接受未签名、adhoc 签名或 dev channel 的 Worker。
- 不让 Worker 无提示更新 Supervisor；Supervisor 自身升级仍需要系统授权。

## 决策

新增一个独立、低频变化的 `kubeloop-supervisor` LaunchDaemon。Supervisor 是本机
helper 更新信任根，只负责验证、切换、启动、健康检查和回滚 Worker。现有
`kubeloop-helper` 继续负责 sing-box、TUN、路由、DNS 和 Session 生命周期。

Desktop、E2E 和未来 MCP 只调用普通用户侧 Supervisor Client；它们不能直接写入
`/Library/PrivilegedHelperTools`，也不能向 Supervisor 提交目标路径或 shell 命令。

```text
KubeLoop Desktop / E2E / MCP
              |
              | Unix Socket
              | peer UID + token + channel policy
              v
kubeloop-supervisor (root, stable trust root)
              |
              | bounded stream, hash/signature verification
              | atomic activate, launchd restart, readiness, rollback
              v
kubeloop-helper (root, frequently replaced Worker)
              |
              +-- sing-box / TUN / routes / split DNS / Sessions
```

### 服务与通道隔离

| 通道 | Supervisor label | Worker label |
| --- | --- | --- |
| release | `dev.fengqi.kubeloop.supervisor` | `dev.fengqi.kubeloop.helper` |
| dev | `dev.fengqi.kubeloop.supervisor.dev` | `dev.fengqi.kubeloop.helper.dev` |

两套通道使用独立的 Unix Socket、token、状态目录、日志、LaunchDaemon plist 和二进制
路径。Supervisor 从自身编译时 channel 决定所有路径；请求不能覆盖 channel 或路径。

## 组件职责

### Desktop / E2E Supervisor Client

Client 负责：

- 获取 Supervisor 状态；
- 从内嵌文件或构建产物读取 Worker；
- 计算清单并以有界流发送 Worker 字节；
- 展示 `authorization-required`、`update-rejected`、`rollback-completed` 等稳定错误；
- 更新完成后重新查询 Worker 状态并验证构建摘要。

Client 不负责：

- 选择系统安装路径；
- 调用 `launchctl`；
- 验证自己的签名是否合法；
- 决定是否绕过 release 签名策略；
- 发送密码、sudo ticket 或 shell 文本。

### Supervisor

Supervisor 只暴露四个操作：

| 操作 | 作用 | 是否修改状态 |
| --- | --- | --- |
| `status` | 返回 Supervisor、active/previous Worker 和更新状态 | 否 |
| `update-worker` | 接收、验证、激活新 Worker | 是 |
| `rollback-worker` | 恢复 Supervisor 已记录的 previous Worker | 是 |
| `restart-worker` | 重启当前已验证 Worker | 是 |

明确禁止 `run-command`、`execute-shell`、`write-file`、`install-path`、任意环境变量和
任意 launchd label 参数。

### Worker Helper

Worker 继续承担现有职责。为了让 Supervisor 与不同 Worker 协议版本兼容，Worker
保留一个最小且向后兼容的 bootstrap health contract：

- `ping` 操作名不变；
- 响应至少包含 `version`、`protocol`、`coreReady`；
- 新字段允许旧 Supervisor 忽略；
- 破坏该 health contract 时必须先升级 Supervisor，并走一次管理员授权。

## 本机文件布局

release 与 dev 分别使用下列根目录；表中以 dev 为例：

```text
/Library/PrivilegedHelperTools/
  dev.fengqi.kubeloop.supervisor.dev
  dev.fengqi.kubeloop.helper.dev
  .kubeloop-helper-dev.previous
  .kubeloop-helper-dev.staged-<random>

/Library/LaunchDaemons/
  dev.fengqi.kubeloop.supervisor.dev.plist
  dev.fengqi.kubeloop.helper.dev.plist

/var/lib/kubeloop-dev/
  supervisor.json
  supervisor.lock
  helper.auth.json
  helper.token

/var/run/kubeloop-dev/
  supervisor.sock
  helper.sock
```

Supervisor 可执行文件、状态文件、staged、previous 和 active Worker 都由 root 创建并
拥有。用户不能预先创建目标、临时或回滚路径。

## Supervisor 协议

Supervisor 使用独立的 `internal/protocol/supervisor`，首版协议号为 1，不复用
Worker `helperprotocol.Version`。

### 更新清单

```go
type UpdateManifest struct {
	SchemaVersion             int    `json:"schemaVersion"`
	RequestID                 string `json:"requestId"`
	Channel                   string `json:"channel"`
	Version                   string `json:"version"`
	WorkerProtocol            int    `json:"workerProtocol"`
	MinimumSupervisorProtocol int    `json:"minimumSupervisorProtocol"`
	Size                      int64  `json:"size"`
	SHA256                    string `json:"sha256"`
	Force                     bool   `json:"force,omitempty"`
}
```

`RequestID` 使用 128 bit 以上随机值；Supervisor 保存一个有界的最近请求集合，拒绝
重复更新请求。Supervisor 自己分配单调递增的本机 generation，不能信任客户端传入
generation。

### 帧格式

`update-worker` 使用单连接有界流：

1. 4-byte big-endian manifest 长度；上限 64 KiB；
2. 严格 JSON manifest，拒绝未知字段；
3. 精确 `manifest.size` 字节的 Worker；默认上限 128 MiB；
4. 不接受附加字节；
5. 返回一条有界 JSON response。

Supervisor 使用 `io.CopyN` 流式写入 root-owned staged 文件，不把整个 Worker 读入
内存。所有连接、读取、验证、激活和 readiness 操作都有显式超时。

## 调用者认证与授权

Supervisor 的更新授权必须同时满足以下条件：

1. Socket 由 root 创建，`chown(root, enrolledOwnerGID)`，权限不宽于 `0660`；如果
   采用单用户所有权则优先 `chown(enrolledOwnerUID)` + `0600`。
2. Darwin `LOCAL_PEERCRED` 返回的 peer UID 等于首次安装时登记的 owner UID。
3. 请求携带随机 256-bit token；Supervisor 仅持有 token 哈希并做常量时间比较。
4. 请求 channel 与 Supervisor 编译时 channel 完全一致。
5. Worker 满足该 channel 的 ArtifactPolicy。
6. 操作通过 active Session、并发更新和降级策略检查。

现有 Unix helper Socket 使用 `0666` 且主要依赖 bearer token。Supervisor 不复制该
模式；后续应另开任务把 Worker Socket 也收紧到 peer UID + token。

## ArtifactPolicy

Supervisor 内部使用小接口隔离 dev/release 验证策略：

```go
type ArtifactPolicy interface {
	Verify(context.Context, UpdateManifest, string) error
}
```

接口定义在消费它的 Supervisor 包中，构造函数返回具体 `*Server`。实现至少包含：

- `DarwinReleasePolicy`：验证 macOS 静态代码签名、固定 Team ID、identifier 和
  designated requirement；不通过 shell 输出解析做最终授权判断；
- `DarwinDevelopmentPolicy`：只存在于 dev Supervisor，并明确区分 signed-dev 与
  owner-trusted 两种模式。

### Release 策略

release Worker 必须由发布流水线签名。验证顺序：

1. SHA-256 与 manifest 一致；
2. 文件为当前 CPU 架构可执行的 Mach-O；
3. 通过 Security.framework 静态代码校验；
4. identifier 为固定 release Worker identifier；
5. Team ID 和 designated requirement 与 Supervisor 内嵌策略一致；
6. manifest channel 为 release；
7. 非显式 rollback 时拒绝版本降级。

### Dev 策略

优先使用稳定的本地开发签名身份并验证固定 identifier/requirement。为了支持没有
Apple Development certificate 的隔离测试机，可以在**首次安装 dev Supervisor**
时显式启用 `owner-trusted`：

- 该能力只能编译进 dev Supervisor；
- 首次启用需要管理员授权，并持久记录风险选择；
- 仅允许登记 owner UID 调用；
- 仍执行 hash、大小、格式、channel、并发、回滚和审计检查；
- UI 和日志明确标记“此用户可无密码安装 dev root Worker”；
- release Supervisor 不认识也不接受该状态字段。

`owner-trusted` 的残余风险无法通过 token 消除：同一用户上下文中的恶意进程如果
同时获得 token，就可能提交恶意 root Worker。因此它只适用于个人测试机、VM 或
可重建环境，不应在日常生产终端默认启用。

## 更新状态机

```text
Idle
  |
  v
Receiving --invalid/timeout--> Rejected
  |
  v
Verifying --policy failure--> Rejected
  |
  v
Staged --active sessions/no force--> Rejected
  |
  v
Activating
  |
  v
StartingWorker
  |\
  | +-- readiness timeout/version mismatch --> RollingBack
  |                                             |
  v                                             v
Committed                               PreviousWorkerReady
```

### 激活步骤

1. 获取进程内更新 mutex 和跨进程 `supervisor.lock`；
2. 写入并 `fsync` staged 文件；
3. 完成 hash、ArtifactPolicy 和兼容性验证；
4. 查询 Worker active Sessions；默认有 Session 时拒绝；`force` 仅在 dev 或明确的
   release 管理动作中允许；
5. 写入 `prepared` journal 并原子持久化；
6. 将 current 重命名为 previous；
7. 将 staged 原子重命名为 current，设置 root ownership 和 `0755`；
8. `fsync` 父目录并写入 `activated` journal；
9. 使用固定 label 执行 `launchctl kickstart -k`；
10. 最多等待 20 秒 bootstrap health response；
11. 校验 version、Worker protocol、core readiness 和当前文件 SHA-256；
12. 成功写入 `committed` 状态；失败恢复 previous、再次 kickstart 并验证回滚版本。

Supervisor 永远不先删除 current。previous 至少保留到下一次成功更新；首版只保留
一个 previous，避免无界磁盘增长。

### Supervisor 崩溃恢复

状态文件使用 temp + rename + parent `fsync` 原子写入。Supervisor 启动时根据 journal
恢复：

| journal | 恢复动作 |
| --- | --- |
| `prepared` | 删除 staged，保留 current |
| `activated` 且新 Worker 健康 | 补写 committed |
| `activated` 且新 Worker不健康 | 恢复 previous |
| current 缺失、previous 存在 | 恢复 previous |
| current/previous 均缺失 | fail closed，要求管理员修复安装 |

## Supervisor 首次安装与自身升级

首次安装沿用一次明确的系统授权，负责：

- 安装 Supervisor 二进制和 LaunchDaemon；
- 写入 channel、owner UID/GID、token hash 和 ArtifactPolicy 配置；
- 安装或接管当前 Worker；
- 启动并验证 Supervisor；
- 删除失败安装留下的临时文件。

macOS 13+ 的正式包逐步迁移到 `SMAppService` 管理 app bundle 内的 LaunchDaemon。
迁移不与 Worker 自更新首版绑定，避免同时改变安装、签名和更新三条链路。

Supervisor 自身升级不通过 `update-worker`。需要升级信任策略、Supervisor 协议或
bootstrap health contract 时，Desktop 必须重新进入系统授权流程。

## 审计与可观测性

每次 Supervisor 操作记录结构化、本机有界事件：

- request ID；
- peer UID；
- channel；
- old/new version、protocol、SHA-256 前缀和 generation；
- policy 名称；
- active Session 数；
- state transition、结果、耗时和 rollback 结果。

禁止记录 token、完整二进制、用户主目录中的敏感路径、签名私钥、helper auth 文件
内容、Session 配置或 sing-box 凭据。Supervisor 日志轮换或接入 unified logging，
不能无限追加单一文件。

## 代码组织

新增：

```text
cmd/kubeloop-supervisor/
  main.go
  root.go
  install.go
  run.go
  version.go

internal/protocol/supervisor/
  protocol.go
  framing.go

internal/supervisor/
  server.go
  client.go
  auth.go
  peercred_darwin.go
  policy.go
  policy_darwin.go
  receiver.go
  updater.go
  journal.go
  launchd_darwin.go
  paths_darwin.go
  status.go
```

修改：

| 文件/区域 | 修改内容 |
| --- | --- |
| `build/helper-prebuild.go` | 构建并内嵌 Supervisor；为 Worker 生成 manifest |
| `build/package-desktop.sh` | release Supervisor/Worker 签名顺序和 requirement 验证 |
| `internal/app/embedded_helper.go` | 区分 Supervisor、Worker 和 manifest 资源 |
| `internal/helperinstall/ensure.go` | 优先通过 Supervisor 更新 Worker |
| `internal/helperinstall/elevate_darwin.go` | 只保留首次安装/修复 Supervisor 的提权入口 |
| `internal/app/bindings_server_network.go` | 展示 Supervisor/Worker 独立状态和修复动作 |
| `e2e/scripts/ensure-helper.go` | 发送当前构建 Worker，并校验 digest/generation |
| `internal/helper/socket_access_default.go` | 后续独立收紧 Worker Socket；不阻塞首版 Supervisor |

不要把 Supervisor server、更新状态机或 ArtifactPolicy 放入 `internal/helper`，避免
Worker 高频修改意外扩大或破坏稳定更新协议。

## 分阶段实施

### Phase 0：保留当前开发复用行为

- 合入开发自动启动复用健康 helper、显式安装严格匹配的区分；
- 记录当前 AppleScript 首次安装链路测试作为迁移基线；
- 不把减少弹窗误认为新 helper 已被测试。

### Phase 1：只读 Supervisor

- 新增 dev/release label、路径和独立协议；
- 首次安装 Supervisor；
- 实现 `status`、peer UID、token hash 和 Socket 权限；
- 不允许更新 Worker。

验收：安装一次后重启 macOS，Supervisor 自动运行；错误 UID/token/channel 全部拒绝。

### Phase 2：接收与验证但不激活

- 实现 manifest framing、大小上限、流式 staged、SHA-256 和请求去重；
- 实现 dev/release ArtifactPolicy；
- 增加 `verify-only` 内部测试入口，但不作为产品 RPC 暴露。

验收：坏长度、截断、附加数据、未知字段、错误 hash、错误签名和跨通道全部 fail closed。

### Phase 3：原子更新与回滚

- 实现 journal、current/previous 切换、launchd restart 和 readiness；
- 实现 Supervisor 启动恢复；
- 实现显式 rollback。

验收：注入 staged 后崩溃、两次 rename 间崩溃、Worker 启动失败和 readiness 超时，
最终都恢复到可运行的 previous Worker。

### Phase 4：Desktop/E2E 切换

- `EnsureCurrentInstall` 优先调用 Supervisor；
- Supervisor 缺失/版本过旧时才提示一次系统授权；
- E2E 校验 active digest/generation，而不只校验 `Version == "dev"`；
- UI 区分 Supervisor 故障、Worker 故障和更新回滚。

验收：连续构建三个 byte-distinct dev Worker 并依次更新，全程不出现管理员密码弹窗，
每次 E2E 都确认实际进程 digest 对应本次构建。

### Phase 5：正式签名与 SMAppService

- release CI 生成并签名 Supervisor/Worker；
- macOS 包装验证 Team ID、identifier、requirement 和 notarization；
- 评估并迁移 Supervisor 注册到 `SMAppService`；
- 保留旧安装检测和显式修复路径。

## 测试矩阵

### 单元测试

- manifest 严格解码、长度边界和未知字段；
- channel、minimum protocol、版本降级和 request replay；
- token 常量时间验证；
- peer UID allow/deny；
- staged hash、截断、附加数据和最大尺寸；
- update state transition 和 journal recovery；
- current/previous 路径不接受客户端覆盖；
- release/dev policy 不可互换。

### macOS 集成测试

- 首次安装需要授权，后续 Worker 更新不需要；
- Supervisor/Worker 在重启后由 launchd 恢复；
- Worker 运行失败自动回滚；
- Supervisor 在每个 crash injection point 重启后收敛；
- active Session 默认阻止更新；dev `force` 会先安全停止 Session；
- 错误 peer UID 即使持有 token 也被拒绝；
- 错误签名即使 hash 正确也被拒绝；
- dev Supervisor 不能修改 release Worker；
- Supervisor 自身更新仍要求系统授权。

### E2E

1. 构建 Worker A，首次安装；
2. 构建 byte-distinct Worker B，无授权更新并确认 digest B；
3. 构建无法 ready 的 Worker C，确认自动回滚 digest B；
4. 并发提交 Worker D/E，仅一个进入激活；
5. 重启 Mac，再次更新 Worker F；
6. 检查日志不含 token、auth 内容或完整 Session 配置。

## 验收标准

- 首次安装后，正常 dev helper 迭代不再调用 AppleScript 管理员授权。
- E2E 能证明运行进程对应本次构建 SHA-256，而非仅协议兼容的旧 helper。
- release Supervisor 不能安装未通过固定签名 requirement 的二进制。
- 任意客户端输入都不能选择路径、label、命令或环境变量。
- 更新失败和 Supervisor 中途崩溃都不会留下“无 current 且无法自动恢复”的状态。
- dev/release 不共享 Socket、token、状态、日志、路径或 rollback 文件。
- Supervisor 自身和信任策略升级仍需要明确管理员授权。

## 风险与权衡

### Owner-trusted dev 是有意的本机提权委派

为了完全自动化本地未签名 helper 测试，owner-trusted dev 必然允许登记用户提交将以
root 执行的代码。它不能被包装成“安全等价于签名发布”。缓解措施是 dev-only
编译隔离、显式首次授权、peer UID、token、固定路径、测试机使用和高可见审计。

### Supervisor 增加了长期 root 进程

新增攻击面通过极小 RPC、无 shell/任意路径、严格 framing、签名策略、输入上限、
单更新锁和 crash recovery 控制。Supervisor 应比 Worker 更少依赖、更少发布。

### Worker 更新会中断现有 Session

首版在有 active Session 时拒绝，测试场景可显式 force。无中断 Session 迁移需要
双 Worker、路由/DNS 所有权转移和连接状态迁移，复杂度与当前目标不匹配。

### SMAppService 迁移不能替代更新协议

`SMAppService` 改善首次注册和系统授权管理，但不会自动提供应用专属的 artifact
验证、原子切换、readiness 和回滚，因此它是 Supervisor 的安装机制，不是
Supervisor 的替代品。

## 被否决方案

### MCP 自动输入管理员密码

系统授权框可能阻止合成输入；密码会进入模型、剪贴板或自动化进程的信任范围，且
不能提供原子更新和回滚。

### 永久 sudo ticket 或 `NOPASSWD`

把可变 helper 路径交给无密码 sudo 等价于授予普通用户通用 root 代码执行，缺少
channel、签名、更新状态机和审计。

### Worker 直接增加任意 self-update RPC

Worker 高频变化，更新协议会与 Worker 协议一起漂移；旧 Worker 可能无法理解新
更新请求。若只用现有 bearer token 接受任意二进制，还会把 token 泄漏升级为 root
代码执行。稳定 Supervisor 能独立维护兼容和信任策略。

### 每次只复用旧 dev Worker

可以减少弹窗，但不能测试本次 helper 修改；E2E 会得到“通过了旧 Worker”的假阳性。

## 结果

KubeLoop 在 macOS 上把一次性的系统授权转换成一条长期、窄且可审计的 Worker 更新
能力。开发和 E2E 可以持续验证新的 helper 构建，正式包则继续由代码签名和明确的
release channel 约束。代价是新增一个必须长期稳定维护的 root Supervisor，以及对
owner-trusted dev 模式残余风险的明确接受。
