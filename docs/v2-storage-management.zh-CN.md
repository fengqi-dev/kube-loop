# V2 Controller 数据库导出、导入与备份

本文说明 `kubeloop-controller storage` 的离线运维流程。数据库配置只从现有
`KUBELOOP_STORAGE_*` / `KUBELOOP_POSTGRESQL_*` 环境变量读取，命令行不接受
PostgreSQL DSN，避免 Secret 进入 shell history、进程参数或运维输出。

## 操作边界

- 执行导入前必须将 Controller 缩容到 `0`。工具还会对所有业务表加 PostgreSQL
  `ACCESS EXCLUSIVE` 锁，但锁不能替代离线操作窗口。
- 逻辑导入只支持已经创建但没有 KubeLoop 业务数据的 PostgreSQL 目标。
  `schema_migrations` 由工具自动初始化，不算业务数据。
- SQLite 物理备份用于同后端恢复；SQLite 与 PostgreSQL 之间迁移必须使用逻辑
  `export` / `import`，不能复制数据库文件或只修改 DSN。
- 所有输出文件默认权限为 `0600`。物理备份拒绝覆盖现有文件；逻辑导出仅在显式
  指定 `--force` 时替换现有普通文件，并拒绝符号链接目标。

## 命令

### 逻辑导出

SQLite：

```bash
KUBELOOP_STORAGE_TYPE=sqlite \
KUBELOOP_SQLITE_PATH=/var/lib/kubeloop/kubeloop.db \
kubeloop-controller storage export \
  --output /secure-backup/kubeloop-export.json
```

PostgreSQL 使用 Secret 文件引用：

```bash
KUBELOOP_STORAGE_TYPE=postgresql \
KUBELOOP_POSTGRESQL_DSN_FILE=/run/secrets/postgresql-dsn \
kubeloop-controller storage export \
  --output /secure-backup/kubeloop-export.json
```

导出从单个数据库快照读取并按主键稳定排序。文件包含格式版本、数据库 schema
version、UTC 创建时间、Controller 创建版本、源后端、精确表/列清单和整个规范化
内容的 SHA-256 校验和。

OIDC/AD Provider 配置、客户端 Secret、AD 密码和数据库 DSN 从不进入导出格式。
短期登录事务中的 state、nonce、PKCE verifier 和交换码摘要也不会导出；对应表在
格式中保留空清单，使导入仍能验证目标完全为空。Refresh Token 仍只以数据库中已
有的不可逆摘要形式迁移。

### 导入空 PostgreSQL

1. 将 Controller Deployment 缩容到 `0`。
2. 为目标 PostgreSQL 创建空 database/schema 和最小权限账号。
3. 注入目标 DSN Secret，并执行：

```bash
KUBELOOP_STORAGE_TYPE=postgresql \
KUBELOOP_POSTGRESQL_DSN_FILE=/run/secrets/postgresql-dsn \
kubeloop-controller storage import \
  --input /secure-backup/kubeloop-export.json \
  --actor operator@example.com \
  --confirm-empty
```

4. 保存命令输出中的 `checksumSha256` 和 `auditEventId`。
5. 将 Controller 恢复到期望副本数，确认 readiness 后再开放客户端流量。

工具在连接目标数据库之前完成 JSON 大小、未知字段、格式版本、schema version、
表/列清单、单元类型、短期认证表为空以及 SHA-256 校验。随后在一个 PostgreSQL
`SERIALIZABLE` 事务内锁表、再次验证所有业务表为空、按外键顺序写入，并追加
`storage.import` 成功审计事件。任何校验、约束、写入或提交失败都会回滚全部导入
数据；失败信息输出到离线作业日志，不会把半成品或“失败审计行”留在空目标中阻止
安全重试。

### SQLite 一致性备份

```bash
KUBELOOP_STORAGE_TYPE=sqlite \
KUBELOOP_SQLITE_PATH=/var/lib/kubeloop/kubeloop.db \
kubeloop-controller storage backup \
  --output /secure-backup/kubeloop-2026-08-10.db
```

工具使用 SQLite `VACUUM INTO` 生成包含 WAL 已提交内容的一致快照，然后对临时文件
执行 `PRAGMA quick_check`、schema version 校验和 SHA-256 计算，最后以同目录 rename
发布。命令成功前不会暴露部分备份文件。

恢复物理备份时应保持 Controller 为 `0`，先保留当前数据库文件，再将已验证的备份
复制到 SQLite PVC 的配置路径并设置 `0600`，最后启动单副本 Controller。物理备份
不用于 PostgreSQL 导入。

## 自动化门禁

- 默认测试覆盖确定性导出、校验和篡改、错误 schema/截断文件、Secret 排除、SQLite
  一致性备份、权限和拒绝覆盖。
- 真实 PostgreSQL 测试使用隔离 schema 验证 SQLite → PostgreSQL 导入、成功审计、
  非空目标拒绝、PostgreSQL 再导出，以及外键失败后所有表保持为空。
- `scripts/test-postgresql.sh` 可连接外部测试 DSN，也可自动创建并清理临时
  PostgreSQL 17 容器。
