# T02 · 关注生命周期、历史补齐与帖子可靠删除

> 代码输入：上游固定提交 `1e2dc8156ca03ad2d6a16657ed6b9b0f9c1a7cb2` 对应的 163 文件已验收源码身份 `t01-verified-b1c911989f049f65`。使用离线 `t02-input` 标签，保留 T01 的接管、预算及 Redis 超时修复。本文是实现和复测说明，不是生产性能承诺。

## 1. 行为契约

- `PUT /api/v1/users/:id/follow`：首次关注或重新关注时，在同一个 MySQL 事务里提交 active 关系新代次与唯一历史补齐任务；重复 PUT 不改变代次或任务进度。关注/发布共享作者的 `user` 行锁。
- `DELETE /api/v1/users/:id/follow`：active→inactive 事务中增加代次并创建针对**已结束代次**的 cleanup 任务，重复删除幂等。pull/push 读时立即排除非 active。
- `GET /api/v1/feed` 或 `mode=pull`：维持原路由、原游标和全历史拉取；`mode=push`：仅查当前 JWT 用户的 inbox，通过 active+匹配代次+`post.status=1` JOIN；游标保持 T01 的独立版本/模式/用户绑定。
- `DELETE /api/v1/post/:id`：只有作者可删；同一作者重复删除成功，不重复创建删除事件。首次 `post.status=0`、`post.deleted` Outbox、`delete_index`/`delete_inbox` 任务共同提交。成功**不代表 Redis/inbox 已清理**；但新详情、列表、编辑、投票以及 Feed 都按数据库状态拒绝不可见内容。
- T02 **不实现混合 Feed**，也不自动更改前端默认关注入口。存量旧帖子仍用原投票版本和真实发布时间；不伪造历史发布事件。

## 2. 表与索引

增量迁移是 `migrations/004_follow_lifecycle_delete.sql`。已存在的 001—003 不改含义。

| 表 | 字段/索引变更 | 不变量 |
|---|---|---|
| `user_follow` | `active TINYINT`, `generation BIGINT UNSIGNED`，沿用 `(follower_id,followee_id)` PK 和 `(followee_id,follower_id)` | active 变更才推进 generation；物理行保留，代次不重置 |
| `feed_inbox` | `author_id`, `follow_generation`，新增 `(post_id,recipient_id)`、`(recipient_id,author_id,follow_generation,post_id)` | `(recipient_id,post_id)` 仍唯一；旧代次 UPSERT 不得覆盖新代次 |
| `follow_delivery_task` | `(follower_id,followee_id,generation,kind)` 唯一；`backfill/cleanup`、`status`、`attempts`、`consecutive_failures`、`claim_generation`、owner/lease、固定上界 `(upper_created_at,upper_post_id)`、进度 `(last_created_at,last_post_id)` | 任务独立、可重领；失败可查询；批次与进度同事务 |
| `follow_bootstrap_state` | 唯一 id=1；固定关系扫描上界、最后处理到的关系键、初始化/完成位 | 显式重复执行有持久游标，不因重启回到起点 |
| `content_projection_task` | 增加 `last_recipient_id`；扩展 kind CHECK 为 `index/fanout/delete_index/delete_inbox` | 发布和删除各自投影独立完成，不互相伪装已处理 |
| `content_outbox` | **结构不变**，新增 `post.deleted` event_type | 事件身份不可变，`(post_id,event_type)` 使每帖删除事件唯一 |

迁移将既有收件箱引用按仍存在且 active 的关系填入作者与 generation=1；无关联或无帖的残留引用清除。新库 `init.sql` 包含同一 004 结构与初始状态。原有 PK、历史帖/用户/投票、事件和版本数据均保留。

## 3. 发布/关注无漏投的提交序列

**先发布后关注**：发布事务先取得 `user(author_id) FOR UPDATE` 并提交新帖/事件/任务；新关注随后持有同一作者行锁，使用 `SELECT ... FOR SHARE` 的 **当前读**确定最新帖子排序上界，历史任务包括该已提交发布。

**先关注后发布**：新关注先持作者锁，提交 active 新代次和历史上界；后来的发布获得作者锁并提交，新帖 fanout 扫描当前有效关系（按 follower ID 分批），即使该读者 ID 小于某个*旧帖子* fanout 的进度，也不会永久遗漏本次新帖子。此处用提交互斥保证覆盖，不把 Snowflake ID 或秒级时间戳误当提交序号。

锁顺序统一为 `author user`（在线关注/发布/存量任务创建时）→ `post FOR SHARE/UPDATE`（如需）→ `user_follow FOR UPDATE`（如需）→ Inbox 写入。两类历史/发布批次锁住相关帖子后再锁关系，防止因关系→帖子与帖子→关系的反向顺序形成死锁，或在删帖清理完成后补写残留。InnoDB `REPEATABLE READ` 下必须在取得作者锁后做 *locking current read*，避免旧一致性快照；MySQL deadlock/timeout 使事务回滚，持久任务之后按有限退避重试，API 的关注/取关幂等允许安全重试。

## 4. 分批、代次与接管

- `ClaimFollowTask` 使用 MySQL 8 `FOR UPDATE SKIP LOCKED` 领取到期任务，`claim_generation+1`；记录租约 owner、截止和累计领取次数。任务 `done/canceled/failed` 不自动再领取。
- 每批对领取代次及有效租约进行 `FOR UPDATE` fence；业务写入与游标推进在同一数据库事务；旧 owner 无法覆盖已被新 owner 接管的批次。没有跨 Redis 的长事务。
- backfill 固定关注时最高有效帖的 `(create_time,post_id)`，以同一完整双字段**倒序** Keyset、有限批次扫描，不设只补最近 N 条。引用 UPSERT 仅在目标 generation **不小于**现有时更新。
- cleanup 仅删除 `recipient_id,author_id,follow_generation=目标旧代次` 的引用；后来的重新关注新代次引用不受影响。数据库唯一约束避免重复投递。
- 任务预算到达时将其正常让出并保留游标，**不增加失败次数**；暂时错误有指数退避与失败上限；租约过期使用新代次接管。错误摘要有限长且无正文/凭据。
- 删除收件箱使用 `(post_id,recipient_id)` 索引和 recipient Keyset 分批删除；被删帖的后台补齐只选 `status=1` 的帖子并持共享锁，删除事务取得排他锁后才能提交，旧扇出同样检查可见性。删除后读时始终过滤状态。

## 5. Redis 删除墓碑、缓存、启动保护

`post_delete.lua` 通过一个 Redis 原子操作：写入 `convo:post:deleted` 墓碑、清理时间/热度 zset、分类集合和目标详情缓存。`content_projection.lua` **首先检查墓碑**，拒绝旧 `index` 任务重新初始化。异常 Redis key 类型会返回可重试错误，不会错误视为成功。**不删除**投票方向集合、版本 Hash、Stream Outbox 或消费去重数据；在途既有投票仍能按原消费协议处理，但新投票在入口 MySQL 可见性检查中被拒绝。

详情缓存命中也先以带超时的 MySQL 查询校验 `status=1`，查库失败时返回忙碌而非使用旧缓存猜测。列表与按 ID 批量查询均过滤 `status=1`；旧社区交集缓存有 60 秒 TTL，**即使未过期也被下游 SQL 可见性过滤**，只会短暂占用空间/导致结果页变短。编辑仍用 T01 原延迟双删；其完整缓存改造不在 T02。

启动时仍需通过 T01 的维护/重建标记以及 active 帖子的真实 Redis 成员校验。对于已删除但 `delete_index` 仍 pending/running/retry 的帖子，允许**对应且确实仍在索引中的成员**解释暂时多出的索引项；不能借此掩盖其他丢失。已 failed/done 的删除任务不能解释不一致。

## 6. 操作与迁移步骤

**旧库：** 先备份，在维护窗口记录 `SELECT VERSION()`, `SHOW CREATE TABLE user_follow`, `SHOW CREATE TABLE feed_inbox`, `SHOW CREATE TABLE content_projection_task`;确认 001—003 已完成，再逐条执行 `migrations/004_follow_lifecycle_delete.sql`。MySQL DDL 会自动提交，**004 不是可整体回滚的事务**。如果中途失败，按 `information_schema.COLUMNS`, `TABLE_CONSTRAINTS`, `STATISTICS`, `TABLES` 检查哪些语句已成功，只补执行尚未完成的语句及 inbox 数据转换；不要从头盲目重跑 ADD COLUMN/CREATE TABLE，也不要用 `init.sql` 覆盖已有库。

**新库：** 只在可丢弃的独立 MySQL 卷初始化 `init.sql`（或 `scripts/t02/prepare_isolated_sql.py` 输出），不要在现有库执行 init，因为其历史表定义含 DROP TABLE。

**明确存量补齐：** `CONVO_MYSQL_DB=... CONVO_MYSQL_HOST=... CONVO_MYSQL_PORT=... go run ./cmd/bootstrap-follow --confirm --batch 50`。此程序只写入待补齐任务和可恢复扫描游标，**不直接把帖子写入 Inbox**；在线 follow 会自行创建任务，bootstrap 可重复执行和中断续跑。先查 `scripts/t02/inspect_progress.sql` 的 bootstrap 完成位与 backfill failed/retry，再在同一固定样本与分页范围核对 push 与 pull 的**可见集合和顺序**。只看 `done` 任务计数不是完整性验收。

Docker 镜像同时包含 `./convo_bootstrap_follow`。完成下节的专用验收环境准备后，可用 `docker compose -f docker-compose.yaml -f scripts/t02/compose.isolated.yaml exec -T convo_app ./convo_bootstrap_follow --confirm --batch 25` 运行；它沿用容器的数据库环境配置，执行前核对目标库和 004 迁移。命令处理完一个批次即提交检查点，中断后重新运行可继续；已完成后重跑不重新创建同代次任务。

**回退边界：** 上线前备份数据库/Redis，备份本次应用版本；运行 004 并写入 T02 数据后，不能直接回滚旧 Go 二进制继续使用旧关系表 SQL（旧版物理 DELETE、旧 fanout 和新代次语义不兼容）。紧急停止新 Worker 与 API 写入并回滚数据库到备份/按评估执行数据向后兼容变更，不能 `DROP` 新表或重置代次当作安全回退。已有 T01 投票在途状态另行保全。

## 7. 隔离验收命令（开发会话运行；禁止默认生产库）

```bash
mkdir -p .t02-local
python3 scripts/t02/prepare_isolated_sql.py --database convo_t02 > .t02-local/t02-init.sql
export COMPOSE_PROJECT_NAME=convo-t02-accept-20261108
export CONVO_T02_INIT_SQL="$(pwd)/.t02-local/t02-init.sql"
export CONVO_BIND_HOST=127.0.0.1
export CONVO_MYSQL_PUBLISH_PORT=43316 CONVO_REDIS_PUBLISH_PORT=46389
export CONVO_RABBIT_PUBLISH_PORT=45682 CONVO_RABBIT_UI_PORT=45673 CONVO_APP_PUBLISH_PORT=49092

docker compose -f docker-compose.yaml -f scripts/t02/compose.isolated.yaml up -d --build
# 在此独立项目上核对服务可用；运行集成测试前暂停 app，避免背景 Worker 干扰独立 DB 断言
docker compose -f docker-compose.yaml -f scripts/t02/compose.isolated.yaml stop convo_app
export CONVO_T02_TEST_DSN='root:123456@tcp(127.0.0.1:43316)/convo_t02?parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27'
go test -tags=integration -run '^TestT02Lifecycle$' -count=1 -v ./dao/mysql
# 如需浏览器联调，再启动 app；此时可分别验收默认 pull 与显式 push
docker compose -f docker-compose.yaml -f scripts/t02/compose.isolated.yaml start convo_app
# bootstrap 命令需要指向上面专用数据库，不要误用 conf/config.yaml 的默认库
CONVO_MYSQL_HOST=127.0.0.1 CONVO_MYSQL_PORT=43316 CONVO_MYSQL_DB=convo_t02 \
  go run ./cmd/bootstrap-follow --confirm --batch 25
# 使用选定 DB 的只读查询观察队列和索引
# 清理前先 docker compose -p 确认确实是本次独立项目
docker compose -f docker-compose.yaml -f scripts/t02/compose.isolated.yaml down -v
```

> `go test -tags=integration ./...` 含老测试中硬编码的共享端口和清理操作，**不能直接在非隔离环境运行**。上例只运行显式要求独立 DSN 且校验数据库名 `_t02` 后缀的 T02 测试；未配置 DSN 时 SKIP 不是通过。MySQL/Redis、真实 API、故障重启与浏览器用例必须由拥有隔离服务的开发会话执行。

## 8. 配置、监控和后续阶段

沿用 T01 的 `CONVO_CONTENT_DELIVERY_*`，增加 `CONVO_CONTENT_DELIVERY_BACKFILL_WORKERS` 和 `CONVO_CONTENT_DELIVERY_CLEANUP_WORKERS`（均默认 1，允许 1—16）。沿用 batch=100、poll=500ms、lease=15s、IO timeout=5s、单次预算=10s、重试上限=12、最长退避=60s 等**开发初始值**；这些数值未经过生产容量测量。`CONVO_METRICS_SAMPLE_CONTENT=false` 同时关闭额外副本的全局 follow 队列 Gauge 采样。

新增指标 `convo_follow_task_total`、`convo_follow_task_duration_seconds`、`convo_follow_task_state`、`convo_follow_oldest_unfinished_seconds`、`convo_follow_rows_total`、`convo_follow_queue_sample_ok`，扩展原 `convo_content_task_*` 的 kind 到两类删除；Grafana 增加 5 个可观测面板。标签只有固定 kind/status/result，用户/帖/事件/任务 ID 只记结构化日志。

T02 不实现混合 Feed、点赞迁移、全部缓存改造、死信回放或通用背压。推拉混合与路由切换放到 T02B；缓存优化等继续按后续学习阶段进行。
