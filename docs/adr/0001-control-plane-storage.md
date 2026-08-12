# ADR-0001：Control Plane 存储采用 SQLite / PostgreSQL 双后端

- 状态：Accepted
- 日期：2026-08-09
- 对应任务：V2-008、V2-106、V2-107、V2-108、V2-109、V2-110

## 背景

KubeLoop V2 将身份、Session、Task、资源恢复快照、幂等记录和审计事件放到集群内 Control Plane。桌面端原有 `internal/store` 是单用户 JSON 状态文件，不能提供事务、唯一约束、并发写、过期清理或多副本一致性，因此不能被 Control Plane 复用。

## 决策

1. 默认后端为 SQLite，外部后端只正式支持 PostgreSQL，不承诺任意 SQL 数据库兼容。
2. SQLite 使用 `modernc.org/sqlite` 的无 CGO `database/sql` 驱动；启用 foreign keys、busy timeout、WAL 和 `synchronous=NORMAL`，并限制为一个数据库连接和一个 Control Plane 副本。
3. PostgreSQL 使用 `github.com/jackc/pgx/v5/stdlib`；DSN 只允许来自环境变量、Secret 注入或文件引用，不写入 ConfigMap、命令参数或日志。
4. 两种后端共享 Repository 接口、领域对象和逻辑 schema version，但维护各自明确的 migration SQL；业务 handler 不接触 `database/sql`、SQL 方言或驱动类型。
5. migration 在 Control Plane 对外 ready 前执行。检测到高于当前程序的 schema version 时拒绝启动；migration 失败必须回滚并保持原 version。
6. SQLite 文件必须位于本地持久卷，由单 Pod 独占。禁止多个 Pod 打开同一文件，也不支持将文件直接放到 NFS、SMB 等共享网络文件系统。
7. PostgreSQL 模式允许 Control Plane 多副本；migration 在同一事务内先获取数据库级 advisory lock，再创建/读取 migration 表并应用版本，避免多个新 Control Plane 同时首次启动的 DDL 竞态。
8. PostgreSQL 连接使用有界连接池、连接寿命和连接超时；`statement_timeout` 作为连接级 runtime parameter 约束所有查询，而不只约束 readiness。共享事务使用 `SERIALIZABLE` 隔离级别，并仅对 SQLSTATE `40001`（serialization failure）和 `40P01`（deadlock）执行有界指数退避重试。事务回调必须只包含可重试的数据库操作。
9. SQLite 与 PostgreSQL 之间不通过修改 DSN 自动迁移数据。切换后端必须使用显式、带 schema version 和校验和的 export/import 流程。
10. 逻辑导出采用固定格式版本、精确表/列清单、稳定行顺序和规范化内容 SHA-256；文件记录数据库 schema version、UTC 创建时间、Control Plane 创建版本和源后端，但不序列化任何数据库、OIDC 配置。短期认证事务的 state、nonce、PKCE verifier 和交换码数据明确不导出。
11. 逻辑导入只支持空 PostgreSQL，且必须由操作者提供审计身份和空库确认。格式与校验和在连接目标前验证；目标空库检查、全表写入和 `storage.import` 成功审计事件位于同一个锁表事务，任何失败整体回滚。
12. SQLite 物理备份使用 `VACUUM INTO` 获取一致快照，发布前执行完整性/schema version 校验并计算 SHA-256；物理文件只用于 SQLite 恢复，不作为跨后端迁移格式。

## Secret 与日志边界

- SQLite 路径和 PostgreSQL DSN 只存在 Control Plane Pod；Data Plane 不接收任何数据库环境变量或卷。
- PostgreSQL DSN Secret 不进入 Helm NOTES、Pod annotation、日志或 API 响应。
- 驱动错误以保留 cause、隐藏诊断文本的内部错误包装传递，只向日志/API 暴露稳定操作消息；这样既能按 SQLSTATE 重试，也不会泄露 DSN、schema、表名或查询片段。
- readiness 只返回 `ready` 或 `unavailable`，不返回后端类型、路径、主机、用户名或驱动错误。
- Refresh Token 只持久化慢哈希或不可逆摘要，不保存明文 Token。

## 部署约束

| 后端 | Control Plane 副本 | 更新策略 | 持久化 |
| --- | ---: | --- | --- |
| SQLite | 1 | Recreate | RWO PVC，默认 `/var/lib/kubeloop/kubeloop.db` |
| PostgreSQL | 1 或更多 | RollingUpdate | 外部托管数据库，DSN 来自 Secret |

Helm 模板必须在渲染阶段拒绝 SQLite 多副本和缺少 DSN Secret 的 PostgreSQL 配置。Control Plane 启动时再次校验副本约束，避免绕过 Helm 后形成多写者。

## 后果

- 小规模安装不需要运维外部数据库。
- Control Plane HA 必须使用 PostgreSQL。
- 双方言 migration 和 conformance test 会增加维护成本，但比在业务代码中隐式兼容方言更可控。
- Data Plane 可以独立扩缩容和替换，不受数据库迁移、PVC 或认证 Secret 影响。
