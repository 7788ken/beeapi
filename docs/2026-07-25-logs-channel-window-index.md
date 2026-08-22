# logs 渠道日窗口索引迁移

`/api/channel/reconcile?summary_only=true` 与带 `channel` 的
`/api/log/stat?...&quota_only=true` 都按
`channel_id + type + created_at` 汇总 `quota`。已有
`idx_logs_channel_type_id (channel_id, type, id)` 服务“最近 N 条”查询，
但无法把日窗口扫描限制在日期范围内；它不能替代本迁移。

目标索引：

```text
idx_logs_channel_type_created_at_quota (channel_id, type, created_at, quota)
```

`quota` 放在索引末尾不是筛选条件，而是让额度汇总无需逐行回聚簇表。
专用查询在 MySQL 使用 `FORCE INDEX`，避免优化器错选
`idx_logs_channel_type_id`，且兼容仓库要求的 MySQL 5.7.8+。
PostgreSQL/SQLite 使用普通 `FROM logs`。

## 迁移边界

- 新建或仍为空的 `logs` 表，应用迁移会创建该索引；若首次建表成功但建索引失败，
  下次启动会在空表上重试。
- 已存在且非空的 `logs` 表不会在 `AutoMigrate`/应用启动期间创建该索引。
- 非空大表必须在独立维护窗口先执行在线 DDL，再部署或启用依赖它的查询。
- 不删除 `idx_logs_channel_type_id`；它仍服务
  `ORDER BY id DESC LIMIT ...`，两个索引用途不同。

## MySQL 5.7.8+

执行前暂停会触发这两条慢查询的任务，并确认没有长时间运行的 `logs`
查询或等待中的 DDL。`LOCK=NONE` 仍需要短暂的 metadata lock；会话级
`lock_wait_timeout` 用于在锁无法及时取得时快速失败，避免 DDL 排队后级联阻塞写入。
若数据库无法在线完成，不应去掉 `LOCK=NONE` 后重试成阻塞式 DDL。

```sql
SET SESSION lock_wait_timeout = 10;

ALTER TABLE logs
  ADD INDEX idx_logs_channel_type_created_at_quota (channel_id, type, created_at, quota),
  ALGORITHM=INPLACE,
  LOCK=NONE;
```

完成后执行：

```sql
ANALYZE TABLE logs;

SHOW INDEX FROM logs
WHERE Key_name = 'idx_logs_channel_type_created_at_quota';
```

## PostgreSQL 9.6+

`CONCURRENTLY` 不能放在事务块中执行：

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_logs_channel_type_created_at_quota
  ON logs (channel_id, type, created_at, quota);

ANALYZE logs;
```

## SQLite

SQLite 建索引会占用写锁，只适用于停写维护窗口：

```sql
CREATE INDEX IF NOT EXISTS idx_logs_channel_type_created_at_quota
  ON logs (channel_id, type, created_at, quota);

ANALYZE logs;
```

## 发布前验证

用实际的渠道、消费类型和一天窗口替换参数，对两条真实查询分别执行
`EXPLAIN`。MySQL 的 `key` 必须是
`idx_logs_channel_type_created_at_quota`，访问类型应为 `range`，
Extra 应包含 `Using index`，`rows` 应接近日窗口行数而不是该渠道全部历史行数。

```sql
EXPLAIN
SELECT COALESCE(SUM(quota), 0) AS quota
FROM logs FORCE INDEX (`idx_logs_channel_type_created_at_quota`)
WHERE channel_id = 1
  AND type = 2
  AND created_at >= 0
  AND created_at <= 0;

EXPLAIN
SELECT channel_id,
       COALESCE(SUM(quota), 0) AS quota
FROM logs FORCE INDEX (`idx_logs_channel_type_created_at_quota`)
WHERE channel_id = 1
  AND type = 2
  AND created_at >= 0
  AND created_at <= 0
GROUP BY channel_id;
```

索引和执行计划验证完成后，再调用只读接口验证同一窗口：

```text
GET /api/log/stat?type=2&channel=1&start_timestamp=...&end_timestamp=...&quota_only=true
GET /api/channel/reconcile?start_ts=...&end_ts=...&summary_only=true
```

确认所有目标节点已使用新索引后，删除被其左前缀完全覆盖的旧
`idx_logs_channel_type_created_at`。不要删除
`idx_logs_channel_type_id`，后者仍服务 `ORDER BY id DESC LIMIT ...`。
