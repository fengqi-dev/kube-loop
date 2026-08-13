# ADR-0001：Control Plane 存储采用 SQLite 与外部 Datasource

- 状态：Accepted
- 日期：2026-08-09
- 对应任务：V2-008、V2-106、V2-107、V2-108、V2-109

## 背景

KubeLoop V2 将身份、Session、Task、资源恢复快照、幂等记录和审计事件放到集群内 Control Plane。桌面端原有 `internal/store` 是单用户 JSON 状态文件，不能提供事务、唯一约束、并发写、过期清理或多副本一致性，因此不能被 Control Plane 复用。

## 决策

1. 未配置 `datasourceURL` 时默认使用 SQLite；外部 datasource URL 支持 PostgreSQL 与 MySQL 方言。
2. SQLite 使用 `modernc.org/sqlite` 的无 CGO `database/sql` 驱动；启用 foreign keys、busy timeout、WAL 和 `synchronous=NORMAL`，并限制为一个数据库连接和一个 Control Plane 副本。
3. PostgreSQL 使用 `pgx`，MySQL 使用 `go-sql-driver/mysql`；datasource URL 只允许来自配置、环境变量或 Secret 文件引用，Helm 不把 URL 写入 ConfigMap、参数或日志。
4. 三种后端共享 Repository 接口、领域对象和逻辑 schema version，并使用对应 SQL dialect；业务 handler 不接触驱动类型。
5. migration 在 Control Plane 对外 ready 前执行。检测到高于当前程序的 schema version 时拒绝启动；migration 失败必须回滚并保持原 version。
6. SQLite 文件必须位于本地持久卷，由单 Pod 独占。禁止多个 Pod 打开同一文件，也不支持将文件直接放到 NFS、SMB 等共享网络文件系统。
7. 外部 datasource 允许 Control Plane 多副本；migration 使用数据库 advisory lock 避免并发首次启动的 DDL 竞态。
8. 外部 datasource 使用有界连接池、连接寿命和超时；共享事务使用 `SERIALIZABLE` 隔离级别，并对 PostgreSQL serialization/deadlock 与 MySQL deadlock/lock-timeout 执行有界指数退避重试。
9. Storage 包不提供数据库导入、导出或物理备份 API；数据库迁移与灾备由部署环境使用数据库原生工具完成。

## Secret 与日志边界

- SQLite 路径和 datasource URL 只存在 Control Plane Pod；Data Plane 不接收任何数据库环境变量或卷。
- Datasource Secret 不进入 Helm NOTES、Pod annotation、日志或 API 响应。
- 驱动错误以保留 cause、隐藏诊断文本的内部错误包装传递，只向日志/API 暴露稳定操作消息；这样既能按 SQLSTATE 重试，也不会泄露 DSN、schema、表名或查询片段。
- readiness 只返回 `ready` 或 `unavailable`，不返回后端类型、路径、主机、用户名或驱动错误。
- OAuth token、code 与 challenge 只持久化 SHA-256 signature/hash，不保存明文。

## 部署约束

| 后端 | Control Plane 副本 | 更新策略 | 持久化 |
| --- | ---: | --- | --- |
| SQLite | 1 | Recreate | RWO PVC，默认 `/var/lib/kubeloop/kubeloop.db` |
| PostgreSQL / MySQL | 1 或更多 | RollingUpdate | 外部托管数据库，datasource URL 来自 Secret |

Helm 模板在没有 datasource Secret 时拒绝 SQLite 多副本。Control Plane 启动时再次校验副本约束。

## 后果

- 小规模安装不需要运维外部数据库。
- Control Plane HA 必须使用 PostgreSQL 或 MySQL datasource。
- 多方言 migration 和 conformance test 会增加维护成本，但比在业务代码中隐式兼容方言更可控。
- Data Plane 可以独立扩缩容和替换，不受数据库迁移、PVC 或认证 Secret 影响。
