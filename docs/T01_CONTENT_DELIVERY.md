# T01：可靠发布、异步投影与最小 Push Feed

**输入提交：** `ce85efe98747a06f0984d2057256e5b02b5e71a7`。本轮仅实现 T01；T02 的历史补齐、取关再关注任务协调、删帖清理，以及进一步的故障治理、性能对照，**尚未实现**。原有投票 Redis Lua → Stream → RabbitMQ → MySQL 链路保持独立。

## 数据及事务边界

| 表 | 关键约束/索引 | 作用 |
| --- | --- | --- |
| `content_outbox` | `PRIMARY KEY(event_id)`；`UNIQUE(post_id,event_type)` | 不可变 `post.published` 事件；版本 1，作者/社区及 **MySQL 原始 UTC 发布时间** |
| `content_projection_task` | `UNIQUE(event_id,kind)`；按 `(kind,status,next_run_at,lease_until,task_id)` 与到期租约索引 | `index`、`fanout` 独立的 `pending/running/retry/done/failed` 任务；领取次数、连续失败数、下一次执行、实例 owner、租约、`generation`、粉丝扫描上界/进度 |
| `feed_inbox` | `PRIMARY KEY(recipient_id,post_id)`；`(recipient_id,create_time DESC,post_id DESC)` | 仅保存投影引用和原始发布时间；正文与当前关注/可见性由 JOIN 现有表读取 |

`POST /api/v1/post` 在**一个 MySQL 事务**中提交 `post`、一个 outbox 事件及两个种类任务；任何一步失败全部回滚。成功返回 `{code:1000,data:{post_id:"十进制字符串",distribution:"async"}}`，**不等** Redis 索引或粉丝收件箱完成。HTTP 路径继续依赖 Redis JWT Session 和投票状态就绪检查，不能将其表述成完全不依赖 Redis。失败请求不保证自动重试安全：客户端不可因超时自动重发创建请求。

## 领取、租约、重试与进度

- `index`、`fanout` 由两组有限数量的 goroutine 独立轮询 MySQL 8 的 `SELECT ... FOR UPDATE SKIP LOCKED`，领取和增加 `generation` 在**短事务**内完成；Redis 网络 IO 不持有这把事务锁。
- 领取者以 `(task_id,kind,lease_owner,generation,lease_until > DB 当前时间)` 作为 fencing 条件。**完成、重排、让出、分批提交**均先 `SELECT ... FOR UPDATE` 检查有效代次，旧 worker 无权覆盖接管者。
- Fanout 首批读取 `MAX(follower_id)` 固定扫描上界：`NULL` = 尚未初始化；持久化 `0` = 确认没有粉丝。后续用 `(followee_id,follower_id)` 索引进行 `follower_id > last_follower_id AND <= upper_bound` Keyset 扫描，批量大小可配。单批的 Inbox `INSERT ... ON DUPLICATE KEY UPDATE` 与游标更新属于**同一个 MySQL 事务**，崩溃后可重做未提交批。任务完成独立于 index 的状态。
- 任务正常到达执行预算时将状态设回 `pending`，不消耗**连续失败次数**；领取次数仅作诊断。瞬时错误增加 `consecutive_failures`，指数退避至可配上限；重复过期租约也累计失败，达到 `max_attempts` 后设为 `failed` 并保留事件与进度，等待后续版本的人工处理工具。成功提交一批后连续失败数归零。没有“自动丢弃任务”或无限紧循环。
- 每次任务操作均有时间预算；关闭应用后尚未完成的租约超时可被其他实例接管。Worker `owner` 加随机/时间 nonce，仍以 DB 自增 `generation` 作为最终代次裁决。租约、单次 IO 和处理预算需要满足 `io_timeout < lease`、`task_budget < lease`。默认 15 s 租约、5 s IO、10 s 任务预算、500 ms 轮询、100 个粉丝/批、每种 Worker 1 个；这些是初始开发参数，**未经压测定标**。

## Redis 初始化、安全恢复与接口

`dao/redis/content_projection.lua` 用单条 Lua 判断索引 Key 类型、两套全局索引是否一致以及旧投票状态是否存在。仅对**全新**帖子 `ZADD NX` 时间/热度（使用 MySQL 原始时间）并加入社区 Set；重做已存在帖子只补社区成员，**不会**重置时间、score、方向或版本。异常 Key 类型、仅存在一个索引、旧投票存在却索引缺失均报错留给人工恢复，禁止从滞后 MySQL 在线覆盖 Redis 投票状态。

服务启动仍检查 Redis 维护标记以及索引完整性；遍历 MySQL 有效帖子和 Redis 双索引逐个核对。只有具有 `pending/running/retry` **持久化 index 任务**的缺失帖子属于正常发布窗口，允许启动 Worker；无任务依据的缺失、额外索引、重建中标记、`failed` 任务导致的缺失均拒绝启动并要求维护操作。启动校验是全量准确性保护，数据量增大后会增加启动时间；本轮不删掉校验或篡改 ready 标记。

- `GET /api/v1/feed` 或 `?mode=pull`：旧拉模式；旧游标/历史帖子可见性不变。
- `GET /api/v1/feed?mode=push&limit=20&cursor=...`：JWT 用户本人的收件箱，按 MySQL 原始 `create_time DESC,post_id DESC` 分页，`limit` 范围 1–50；`limit+1` 决定下一页。游标版本化且绑定 **push 模式和认证读者**，禁止跨用户/跨模式复用。只 JOIN 当前关注作者且 `post.status=1`、仍存在的作者/社区；更新关注状态后立即按最新状态过滤。尚未投影的发布内容刷新后才会出现。JavaScript 中各类 ID 都按十进制字符串返回。
- `GET /api/v1/post/:id`：详情直接从已提交的 MySQL 查询（允许索引尚未建立）；票数按现有 Redis/DB 逻辑获取。
- `POST /api/v1/vote`：当新帖尚未建索引且 MySQL 有未完成 index 任务时，返回业务码 `1014`、提示“帖子内容索引正在同步，请稍后重试投票”，**不伪造投票成功**。原投票事件及方向版本语义不变。
- React 仅展示发帖已持久化但索引/Feed 可能延迟的提示、待索引投票的人工重试提示；默认关注页面仍走拉模式。

**T01 的竞争边界：** Fanout 读取扫描期间**当前仍存在**且处于固定上界内的关系；发帖时持续关注、在扫描时仍存在的读者应覆盖。新关注 / 取消再关注的历史补齐和后台协调属于 T02；本轮不能把 Push Feed 当作全部历史内容的完整视图。收件箱引用保留且读取实时过滤；本轮不实现删除帖子的后台引用清理。

Redis v6 客户端的 context 本身不约束网络读写。内容投影与启动索引查询使用每次操作独占的连接，拨号遵守 context，取消时关闭该操作的连接，使其他投票、Session 请求继续使用原连接池。已取消的调用直接返回。每次操作建立连接有额外开销，本轮仅验证超时与正确性。写入超时后的执行结果可能未知，任务保留并通过幂等投影重试。

## 监控与日志

- `convo_content_projection_total{kind,result}`、`convo_content_projection_duration_seconds{kind,result}`：完成 / 正常让出 / 重试 / 永久失败 / 失租等固定标签。
- `convo_content_delivered_total`：成功提交的新 Inbox 引用数。
- `convo_content_task_state{kind,status}`、`convo_content_oldest_unfinished_seconds{kind}`：任务数量和最老未完成年龄；`convo_content_queue_sample_ok` 和 `convo_content_queue_last_success_timestamp_seconds` 区分空队列与采集失败/数据陈旧。
- MySQL 全局 Gauge **每数据库只允许单实例采样**：对其他实例设置 `CONVO_METRICS_SAMPLE_CONTENT=false`；不能直接跨副本求和。`event_id/task_id/post_id` 在日志而**不在 Prometheus 标签**。日志不得写正文、JWT 或连接凭据。

## 配置与迁移

`settings/settings.go` 的 `content_delivery` 默认项也通过 `CONVO_CONTENT_DELIVERY_*` 覆盖；`conf/config.yaml` 展示默认值。新增配置在旧文件缺少该节时仍会回退默认。配置启动时校验范围；不同环境请调整 IO、并发与租约预算，但不要将这些开发默认值当作真实吞吐保障。

**对已有数据库升级前，请先备份并核对：** MySQL 8、已应用 `001_vote_persistence.sql` 和 `002_follow_feed.sql`；`post`、`user_follow`、`user`、`community` 均存在；应用停止发布，至少有数据库 DDL 权限；用 `SHOW TABLES LIKE 'content_%'; SHOW TABLES LIKE 'feed_inbox';` 和 `SHOW CREATE TABLE user_follow;` 检查半执行与预期 schema。确保独立迁移成功后再部署会要求新表的应用版本。

```bash
# 在已存在的真实数据库上执行且仅执行一次，按环境提供密码/连接信息：
mysql --host="$DB_HOST" --port="$DB_PORT" --user="$DB_USER" \
  --database="$DB_NAME" < migrations/003_content_delivery.sql

# 迁移后：
mysql --host="$DB_HOST" --port="$DB_PORT" --user="$DB_USER" \
  --database="$DB_NAME" -e 'SHOW TABLES LIKE "content_%"; SHOW TABLES LIKE "feed_inbox";'
```

**MySQL DDL 自动提交，不存在整份迁移的事务回滚。** 部分执行失败时保留已建表和原业务数据，根据 `SHOW CREATE TABLE` 与 `information_schema` 检查已执行到第几张表，人工审核后**仅执行剩余 DDL**。不要对已经存在且有业务数据的新表执行 `DROP`，也不要无条件重复整份 SQL。回退代码时保留新表及事件/任务，旧版没有 T01 Worker，已按新协议发布的内容可能无索引；必须在隔离/维护窗口处理未完成任务，不能直接退回旧程序对外提供有遗漏的 Feed。启动新版本前确认三张表齐备。

**新建、可销毁的隔离数据库**：`init.sql` 已包含三表。为了使用独立 Compose 项目及不会冲突的卷和端口，可执行：

```bash
mkdir -p .t01-local
python3 scripts/t01/prepare_isolated_sql.py --database convo_t01 > .t01-local/t01-init.sql
export CONVO_T01_INIT_SQL="$(pwd)/.t01-local/t01-init.sql"
export CONVO_BIND_HOST=127.0.0.1 CONVO_MYSQL_PUBLISH_PORT=43306 CONVO_REDIS_PUBLISH_PORT=46379
export CONVO_RABBIT_PUBLISH_PORT=45672 CONVO_RABBIT_UI_PORT=45673 CONVO_APP_PUBLISH_PORT=49090
export COMPOSE_PROJECT_NAME=convo_t01_manual
# 只在专用测试环境运行，不要合并生产数据卷/覆盖本地 override：
docker compose -f docker-compose.yaml -f scripts/t01/compose.isolated.yaml up -d --build
# 查三表和任务：
docker compose -f docker-compose.yaml -f scripts/t01/compose.isolated.yaml exec mysql8019 \
  mysql -uroot -p -Dconvo_t01 -e 'SELECT kind,status,COUNT(*) FROM content_projection_task GROUP BY kind,status;'
# 仅删除上述隔离 Compose 项目的容器和专用数据卷：
docker compose -f docker-compose.yaml -f scripts/t01/compose.isolated.yaml down -v
```

上述命令显式传入 `-f`，只合并列出的 Compose 文件。普通 `docker compose` 默认调用会自动发现本地 override，运行前核对有效配置并使用独立 project 和闲置端口。**已有集成测试可能写死 33306/36379 并清理数据**，不要用它们代替下述 T01 隔离测试。也不要在真实数据库上运行 `init.sql`（它会重建表）。`.t01-local/` 生成物仅供本地运行，不提交。

## 验证方式（只限隔离库）

```bash
# 上述 Compose 启动成功之后，独立 SQL 会话运行 T01 集成用例；
# 使用不含真实凭据的本地环境，MYSQL_DSN 必须连接数据库名含 t01 的隔离库：
# MySQL 用例需要独占任务队列，先停止同一专用库上的应用 Worker：
docker compose -f docker-compose.yaml -f scripts/t01/compose.isolated.yaml stop convo_app
export T01_ALLOW_INTEGRATION=YES
export T01_TEST_MYSQL_DSN='root:<local-only-password>@tcp(127.0.0.1:43306)/convo_t01?parseTime=true&loc=UTC&time_zone=%27%2B00%3A00%27'
export T01_TEST_REDIS_ADDR='127.0.0.1:46379' T01_TEST_REDIS_DB=15
go test -tags=integration -race -count=1 ./dao/mysql -run '^TestT01' -v
go test -tags=integration -race -count=1 ./dao/redis -run '^TestT01' -v
docker compose -f docker-compose.yaml -f scripts/t01/compose.isolated.yaml start convo_app

# 全仓静态检查和前端构建：
go build ./...
go vet ./...
go vet -tags=integration ./...
go test -race -count=1 ./...
(cd scripts/verify && go build ./... && go vet ./...)
(cd web && npm ci && npm run build)
python3 -m unittest discover -s scripts/t01 -p 'test_*.py' -v
```

T01 MySQL 集成测试会插入独立高 ID 用户/帖子/事件/收件箱并按引用顺序清理；Redis 测试仅使用测试数据库 15 和独立的帖子 ID，不清空整个 Redis DB。要进行完整双账号 API 演示，使用隔离环境：A 先关注 B，再由 B 发布帖子，等待两种 task 为 `done`，A 查 `?mode=push`、B 立即读详情、再尝试改票和取关过滤。请分别记录实际输出、异常和耗时；没有执行前不能标注通过。后续要补充页面可用性、历史补齐和系统化性能测试，安排 T02 与后续阶段。
