# Convo 第一阶段 · 开发实现与 Codex 独立验收手册

> 输入：`convo-phase1-handoff-041abff.zip`（源码基线 `041abff`），`convo-phase1-requirements(1).md`。本手册描述源码实现、复跑方法与未验收风险；**不将静态检查误称为实机验收**。
>
> 交付代码基于上传交接包，而非用户工作区未提交的 `docker-compose.yaml`。交接包里的 `restart: unless-stopped` 保留；合并回用户本地时不要覆盖本地 Compose 的额外修改。

## 1. 变更地图与设计边界

| 模块 | 实现位置 | 本次实现 | 不在本期 |
|---|---|---|---|
| 可观测性 | `observability/`、`middlewares/metrics.go`、`dao/redis/metrics.go`、`worker/`、`monitoring/` | 路由/状态/业务码、投票结果、Relay、Consumer、提交延迟、Outbox 后台采样；RabbitMQ 官方导出、Grafana 面板、Prometheus 规则 | 背压、熔断、DLQ 重放工具、消费批量落库 |
| 关注/Feed | `migrations/002_follow_feed.sql`、`dao/mysql/feed.go`、`logic/feed.go`、`pkg/feedcursor/`、`controller/feed.go` | MySQL 关系唯一约束、幂等接口、读时拉取、双字段游标分页、批量 JOIN 作者/社区 | 写时扇出、跨页快照、无限级深页性能保证 |
| React | `web/`、`router/spa.go`、`Dockerfile` | React + TS + Vite、账户、首页新帖/热榜、发帖、详情、关注流、投票、响应/空/错误状态、窄屏 | 搜索、评论、图片、通知、显示个人既有投票方向 |
| 负载/测试 | `scripts/phase1/`、相关 `*_test.go`、CI | 固定夹具、历史数据生成、Feed HTTP/混合负载、真实投票负载、SQL 计划、故障暂停安全门 | 自动执行危险的 RabbitMQ 故障、实际性能结论（需实机） |

现有的 Redis Lua → Stream Outbox → Relay → RabbitMQ → Consumer → MySQL 写路径没有调整背压和刷盘策略。Feed 只按当前关注关系拉取，未建立收件箱。旧 `/api/v1` 行为、旧 `/` 和 `/static/` 保留；React 独立入口为 `/app/`。

## 2. API 契约

所有新 API 都要求 `Authorization: Bearer <JWT>`，仍然返回 HTTP 200 + `{ "code": 1000, "msg": "success", "data": ... }`；前端不将 HTTP 200 直接当业务成功。

| 方法 | 路由 | 成功 `data` | 错误 |
|---|---|---|---|
| PUT | `/api/v1/users/:id/follow` | `{ "following": true }` | 1001 非法 ID、1003 目标不存在、1013 自己、1006/1007 鉴权、1005 服务故障 |
| DELETE | `/api/v1/users/:id/follow` | `{ "following": false }` | 1001 非法 ID、1006/1007 鉴权、1005 服务故障。不存在的关系也成功 |
| GET | `/api/v1/users/:id/follow` | `{ "following": false }` 或 true | 同上；本人固定 false |
| GET | `/api/v1/feed?limit=20&cursor=...` | `{ "items": [], "next_cursor": null }` | 1001 `limit` 非 1–50/游标无效、鉴权错误、1005 查询失败 |

`FeedItem` 字段：`id`、`author_id`、`author_name`、`title`、`create_time`、`community:{id,name}`。所有新 ID 都是**十进制字符串**。旧详情/列表响应在保留原 `author_id`（数值）的同时添加 `author_id_str`（字符串）；UI 仅使用后者。登录响应的 `user_id` 已经是字符串。

业务码新增 `1013=不能关注自己`；其他错误码保持原有数值。非法 URL ID 拒绝非规范形式、非正数、溢出；Feed 的游标是服务端生成的 base64url JSON `{t:<Unix seconds>,id:"<int64>"}`，作为不透明字符串传回，客户端不可自行重算。`post.create_time` 目前为 TIMESTAMP(0)，通过 MySQL 会话强制 UTC（`dao/mysql/mysql.go` 的连接参数），利用同一个秒级时间戳及帖子 ID 保证稳定顺序。

**分页语义**：第一页面没有游标，`ORDER BY create_time DESC, post_id DESC`；下一页使用 `(create_time < t OR (create_time = t AND post_id < id))`，`limit+1` 判断是否还有下一页。关注变更、内容可见性改变会在下一次查询即时生效；跨页不承诺快照一致性。`status=1` 是首版可见性筛选。

## 3. 数据库迁移（必须先执行）

* **已有数据库**：备份后，仅执行一次 `migrations/002_follow_feed.sql`。不运行 `init.sql`，因为原脚本会 `DROP TABLE`。
* **可丢弃的新测试库**：删除隔离 Compose 项目自身的 MySQL 数据卷后用 `init.sql` 创建；新表和索引已同步添加。
* 新表 `user_follow(follower_id,followee_id,create_time)` 以双字段主键确保并发关注只有一条关系，并用两个外键保证关系两端为存在的用户。取关通过用户 JWT 身份限定 follower，客户端不能指定 follower。
* 索引候选 `idx_feed_scan(status,create_time DESC,post_id DESC,author_id)` 与 `idx_feed_author(author_id,status,create_time DESC,post_id DESC)` 都先建立以便在小/大关注规模下比较；**尚未证明哪个适用生产负载，也未根据实测删除冗余索引**。

在仓库根目录、**已备份并确认目标库**之后：

```bash
# 已存在的本地数据库：根据你的实际容器/凭据调整。
docker compose exec -T mysql8019 mysql -uroot -p123456 convo < migrations/002_follow_feed.sql

# 复核
# SHOW TABLES LIKE 'user_follow';
# SHOW INDEX FROM post WHERE Key_name LIKE 'idx_feed%';
```

该迁移不是可重复执行脚本；若已执行，不要再运行。外键建立可能需等待 DDL 锁，按环境控制维护窗口。

## 4. 本地启动 / 监控隔离 / 停止

```bash
# 旧接口继续使用原 Compose；默认端口不变。
docker compose up -d --build

# 启动可选监控（与应用同一 Compose 项目/网络）
docker compose -f docker-compose.yaml -f monitoring/compose.yaml --profile monitoring up -d --build
# Grafana: http://127.0.0.1:13000 (admin / CONVO_GRAFANA_PASSWORD，默认只供本机演示)
# Prometheus: http://127.0.0.1:19090/targets
# Grafana -> Convo -> Convo · Vote Pipeline & Feed

# 停止监控，不停止应用和数据库：
docker compose -f docker-compose.yaml -f monitoring/compose.yaml stop grafana prometheus
# 重启监控：
docker compose -f docker-compose.yaml -f monitoring/compose.yaml --profile monitoring up -d grafana prometheus
```

Go 应用**另外监听 9091** 指标端点：普通宿主机开发默认 `127.0.0.1:9091/metrics`；Compose 内设置 `CONVO_METRICS_ADDR=0.0.0.0:9091`，但只 `expose` 到 Compose 网络，并不发布 9091 到公网。RabbitMQ 的官方 `rabbitmq_prometheus` 插件使用内部端口 15692，通过 `/metrics/per-object` 采集按队列区分的 ready/unacked，不在宿主端口公开。Grafana/Prometheus 主机端口只绑定 127.0.0.1。生产不应使用示例账号密码。

采样参数：Outbox 每 5 秒读取 `XLEN`、最老 Stream 消息、XPENDING，不进入请求路径；读取失败时 `sample_ok=0`，上次采样值不自动归零，`last_success_timestamp_seconds` 仍保留。`XLEN` 包含 pending，不要把二者累加；Stream 删除已确认转发的消息后，长度代表未转发事件数。MQ 采样失败从 Prometheus 的 `up{job="rabbitmq"}` 观察，而不是显示空队列。多应用实例只让**一个实例**采样全局 Redis Stream，其他实例设置 `CONVO_METRICS_SAMPLE_OUTBOX=false`；多个 HTTP 直方图按实例聚合时必须保留实例标签或做 `sum by(le,route,...)`。

业务指标说明：

| 指标 | 标签/单位 | 解释 |
|---|---|---|
| `convo_http_requests_total` | `method,route,status,code` | 路由是 Gin 模板，业务错误 `code` 可识别 HTTP 200 的 Feed 失败 |
| `convo_http_request_duration_seconds` | 直方图，同上 | 按路由聚合 `histogram_quantile` 得 P50/P95 |
| `convo_vote_requests_total` | `result` | `changed`、`unchanged`、`rate_limited`、`auth_failed`、`business_rejected`、`server_error` |
| `convo_vote_relay_publish_total` | `confirmed`/`failed` | RabbitMQ Publish 确认结果；重发可能重复计数，**不是唯一事件数** |
| `convo_vote_consumer_total` | `persisted`、`duplicate`、`same_version`、`stale`、`retry`、`dlq` | 首次成功推进票态与去重/过时分开 |
| `convo_vote_persist_latency_seconds` | 直方图，秒 | 仅 `PersistVoteEvent` 返回 `persisted` 且事务提交后采样。`occurred_at` 为秒级，测量存在约一秒量化误差；没有成功提交时没有新样本 |
| `convo_vote_outbox_length`/`oldest_age_seconds`/`pending` | gauge | 后台采样积压；采集失败不能作空队列解读 |
| `convo_vote_outbox_sample_ok`/`last_success_timestamp_seconds` | gauge | 分辨成功空 Stream、采样失败和陈旧数据 |

禁止将用户 ID、帖子 ID、事件 ID、游标、URL、JWT、连接凭据、错误原文放入 Prometheus 标签。`event_id` **只进入结构化日志字段**，可由创建 → 发布 → 消费逐条检索。只有线程/实例内计数语义，进程重启后 counters 归零是正常现象。

Prometheus 报警文件 `monitoring/prometheus/alerts.yml`：`ConvoVoteOutboxBacklog` 在最老未发消息年龄超过 30 秒且采样有效，持续 1 分钟后 firing。可在独立测试栈暂停 RabbitMQ、持续发送允许的有效投票，访问 `http://127.0.0.1:19090/alerts` 观察 pending→firing；恢复 RabbitMQ 后 Outbox 排空、告警恢复。仅供本地复现，不要求外部通知。停监控时应用写链路应继续工作；复开后 `up` 重回 1；无流量时 `up=1` 且 rate=0。

## 5. React 前端运行与验收路径

```bash
cd web
npm ci
npm run dev
# 开发环境：http://127.0.0.1:5173/app/  (同源代理 /api/v1 -> http://127.0.0.1:9090)
npm run build
cd ..
# Dockerfile 中已有 Node 构建阶段；Compose 应用 http://127.0.0.1:9090/app/
```

在真实 API 上：账号 B 登录并发帖 → 账号 A 从详情关注 B → A 的 `/app/feed` 出现 B 过去及之后的可见帖子 → A 赞成、改票、取消 → A 取关 B → Feed 重新读取后不再包含 B 的帖子 → 退出登录/切换账号 → 状态和 Feed 缓存不串用。关注按钮等待 GET 状态，不使用估计状态；发送中禁用；失败保留可重试状态。正文由 React 纯文本输出（不注入 HTML）。详情页未展示我的既有投票方向，因为现有服务未提供该查询契约；前端不会假装已知。

注意 `/app/post/<id>` 直接刷新、`/app/feed` 直接刷新，走 `/app/*path` 的 SPA 回退。`/api/v1`、`/swagger`、`/static`、原 `/` 保持原路由。`/app/assets/*` 静态文件缺失时返回 404，不回退 HTML。指标端点在独立监听器上，不通过 SPA 访问。

## 6. 独立测试环境（不碰默认数据卷）

**大规模测试、故障注入不能使用默认 Compose 项目**。以下设置复用同一份 Compose 定义，通过 `-p` 隔离卷名、通过环境变量隔离全部宿主端口（默认 Compose 端口仍与原仓库相同）：

```bash
export CONVO_TEST_PROJECT=convo-phase1-accept
export CONVO_BIND_HOST=127.0.0.1
export CONVO_APP_PUBLISH_PORT=19091
export CONVO_REDIS_PUBLISH_PORT=46379
export CONVO_MYSQL_PUBLISH_PORT=43306
export CONVO_RABBIT_PUBLISH_PORT=35673
export CONVO_RABBIT_UI_PORT=25673
export CONVO_PROM_PUBLISH_PORT=29090
export CONVO_GRAFANA_PUBLISH_PORT=23000
export CONVO_GRAFANA_PASSWORD='set-a-local-test-password'

docker compose -p "$CONVO_TEST_PROJECT" -f docker-compose.yaml -f monitoring/compose.yaml \
  --profile monitoring up -d --build
# 隔离应用 http://127.0.0.1:19091/app/ ; Grafana localhost:23000
```

测试账号优先走 API 创建。对于 SQL 历史数据注入，**必须先**注册登录测试 Reader，取得其字符串 user_id 和 JWT。只将 Reader ID 用在独立项目数据源。SQL 合成作者是无法登录的测试用户。比如：

```bash
python3 scripts/phase1/generate_feed_sql.py --mode fixture \
  --namespace fixture_01 --reader-id '这里填已注册测试Reader的user_id' \
  --out /tmp/convo_feed_fixture.sql
# 必须先排空投票写链路、停止所有应用写入。
docker compose -p "$CONVO_TEST_PROJECT" stop convo_app
# 手工确认输出文件及容器是独立项目后再导入 SQL：
docker compose -p "$CONVO_TEST_PROJECT" exec -T mysql8019 mysql -uroot -p123456 convo \
  < /tmp/convo_feed_fixture.sql
# SQL 直接导入帖子不会维护 Redis 索引，必须在维护窗口离线重建：
docker compose -p "$CONVO_TEST_PROJECT" run --rm --no-deps convo_app \
  ./convo_rebuild_redis --confirm-maintenance
docker compose -p "$CONVO_TEST_PROJECT" start convo_app

# 不要把 TOKEN 打印在终端日志或提交产物；供 Python HTTP 验证脚本读取：
export CONVO_TEST_TOKEN='这里填测试Reader的JWT'
python3 scripts/phase1/verify_feed.py --url http://127.0.0.1:19091 \
  --manifest /tmp/convo_feed_fixture.json
```

导入与停止 App 的顺序以实际中间件状态为准：**绝不在投票 Outbox 或 MQ 尚未排空时做在线 Redis 重建**。本期脚本不会自动执行破坏性的数据库清理。

大数据：选新命名空间，`--mode load --users 100 --posts 1000 --follow-count 5 --seed 20261104` 和 `--users 1000 --posts 20000 --follow-count 200` 两组分别造数、重建 Redis 并复跑。`--reader-id` 指向已有可登录账号。运行长/短关注时使用各自 reader 或新建隔离环境，避免在一个 Reader 上叠加关系造成错误口径。

```bash
python3 scripts/phase1/load_feed.py --url http://127.0.0.1:19091 \
  --concurrency 5 --per-worker 60 --depth 1 --warmup 10 \
  --out /tmp/convo-feed-home.json
python3 scripts/phase1/load_feed.py --url http://127.0.0.1:19091 \
  --concurrency 5 --per-worker 60 --depth 10 --warmup 10 \
  --out /tmp/convo-feed-deep.json

# Reader 必须已关注 Writer；各自 JWT 存在专用环境变量。
export CONVO_TEST_WRITER_TOKEN='另一个测试用户的JWT'
python3 scripts/phase1/mixed_load.py --url http://127.0.0.1:19091 \
  --run-id mixed_01 --seconds 30 --readers 4 --write-interval-ms 1500 \
  --out /tmp/convo-mixed.json

# SQL 计划：先编辑脚本内 reader_id / cursor_ts / cursor_id。
docker compose -p "$CONVO_TEST_PROJECT" exec -T mysql8019 mysql -uroot -p123456 convo \
  < scripts/phase1/feed_plans.sql > /tmp/convo-feed-explain.txt
```

**投票流量必须走真实 API**：用多个测试账号产生独立 JWT、先建帖，然后：

```bash
export CONVO_TEST_VOTE_TOKENS='测试用户1的JWT,测试用户2的JWT,测试用户3的JWT'
python3 scripts/phase1/vote_load.py --url http://127.0.0.1:19091 \
  --post-id '真实API创建的帖子ID' --requests-per-user 20 --interval-ms 150 \
  --out /tmp/convo-votes.json

# 单独终端，仅独立项目允许暂停（退出/中断自动 unpause）：
CONVO_TEST_ALLOW_FAULT=YES ./scripts/phase1/fault_pause_rabbit.sh
```

**资源核对**：依真实环境保存 `docker compose ... ps`、`docker stats --no-stream`、`SELECT VERSION()`、Go 版本、提交 ID、测试账号关注数与作者粉丝数、MySQL 实际数据量。Redis `XLEN convo:outbox:vote`；RabbitMQ `rabbitmqctl list_queues name messages_ready messages_unacknowledged`，必须按主队列/重试/DLQ 记录；MySQL `SELECT user_id,post_id,direction,version,last_event_id FROM post_vote WHERE post_id=...` 与 Redis 版本、方向最终对账。`mq_consumed_message` 行数不能替代 DLQ 数量。

**安全清理**：只删除独立项目时，先核对 `CONVO_TEST_PROJECT` 后运行 `docker compose -p "$CONVO_TEST_PROJECT" -f docker-compose.yaml -f monitoring/compose.yaml down -v`；`-v` **会删除该独立项目的全部测试数据卷**，不要对默认项目执行。

## 7. 压测目标、证据与写时扇出决策

以下是供 Codex 验收使用的**实验目标（不是实测）**：

| 负载 | 数据基线 | 目标（实验） |
|---|---|---|
| 低关注首页/深页 | 100 用户、1k 帖、关注 5 | Feed P95 ≤ 200ms，错误率 ≤ 1% |
| 高关注首页/深页 | 1k 用户、20k 帖、关注 200 | Feed P95 ≤ 300ms，错误率 ≤ 1% |
| 读写混合 | 4 个 Reader 线程 + 1 个 Writer，持续 30s | 发帖 P95 ≤ 300ms，Feed P95 ≤ 300ms，可见性成功率 ≥ 99% |

目标值应根据测试机器与预期用户规模调整，并注明环境。必须固定种子、相同页大小、预热、重复次数、数据分布和样本窗口；分别归档 `/tmp/...json` 原始 HTTP 延迟样本、SQL `EXPLAIN ANALYZE` 计划和主要资源指标。不要用冷启动差异声称索引带来优化。MySQL 查询结果包含作者/社区 JOIN，既不能把深页 OFFSET 与首页游标直接比，也不能把 5 关注者与 200 关注者的结果混为一个 P95。目标关注数＝读者聚合作者数；目标作者粉丝数＝未来写时扇出数量，记录时分开。

**已更新阶段路线：第二阶段计划实现一版写时扇出，与本阶段读时拉取做对照实验；不再以“压测超标”作为是否实施对照版本的前置条件。** 第一阶段依旧交付拉模式及可复跑基线，不在本期实现写时扇出。实施第二阶段时使用相同数据规模、低/高关注读取、普通/高粉丝作者混合读写负载，比较 Feed P95、发帖 P95、可见延迟、写放大、积压与恢复成本。即使拉模式已达标，也照计划完成对照；是否**采用**扇出作为正式架构仍以对照结果决定。普通作者 2 粉丝与高粉丝作者 501 粉丝的自动化造数/验证见 `docs/PHASE1_REPAIR_HANDOFF.md`。

## 8. Codex 必做验收矩阵与当前状态

| 编号 | 独立实机步骤 | 目前状态 |
|---|---|---|
| OBS-1 | Scrape targets、Grafana 真样本、`event_id` 关联三段日志 | 待 Codex 实机 |
| OBS-2 | 隔离栈暂停 MQ，真实投票，多方 Redis/MySQL/队列对账、追平时限 | 待 Codex 实机 |
| OBS-3 | 停/起 Prometheus/Grafana，应用仍能发帖投票、无流量 vs 失联 | 待 Codex 实机 |
| FEED-1 | 鉴权、非法 ID、自己、目标缺失、并发幂等及只操作本人 | 已提供 API/数据库实现及脚本；待实机 |
| FEED-2 | 同时间戳、多页、插新帖、取关后排除、字符串大 ID | Cursor 独立单测通过；其他待实机 |
| FEED-3 | 低/高关注首页深页/读写混合；计划、原始结果、是否立项 | 脚本已提供，数据尚未测 |
| UI-1 | 两账号完整路径、窄屏、直接刷新、会话失效、投票失败重试 | 前端 TS 语法已解析；待浏览器实机 |
| REG-1 | 历史 API、迁移既有库、新库、Redis 恢复、Go race/CI | 待实机 |

本次环境缺 Go 第三方依赖和 npm tarball，无法联外下载；当前 npm lockfile 已固定版本与下载路径，但尚未使用在线 registry 校验完整性哈希，Codex 应在联网安装时复核锁文件，未完成全仓 `go build/go vet/go test -race` 或 `npm run build`；但运行了 Go 标准库独立的 `go test ./observability ./pkg/feedcursor`、Python 脚本语法检查/夹具生成、TS 语法转换与 npm 锁文件依赖图一致性检查。具体命令和结果见 `docs/PHASE1_VERIFY_RESULTS.md`。Codex **必须在网络可用、Docker 可用的机器上真正运行编译、迁移和验收；失败要反馈定位，不应替换为推测结果**。

### 推荐 Codex 运行顺序

1. 对比基线和用户本机未提交改动，尤其 Compose；增量迁移前备份。
2. `gofmt -l .`、`go mod verify`、`go build ./...`、`go vet ./...`、`go vet -tags=integration ./...`、`go test -race -count=1 ./...`；`cd scripts/verify && go build ./... && go vet ./...`。
3. `cd web && npm ci && npm run build`；如锁文件不一致，先记录具体错误、修正并提交锁文件再重跑，不要静默降级为跳过检查。
4. 新库 / 升级库验证、FEED-1/2 功能夹具、UI-1 两账号实际浏览器操作（桌面/窄屏）。
5. OBS-1/3 真实指标与日志；OBS-2 中断/恢复 RabbitMQ 的恢复时限与 Redis/MySQL 版本和方向对账。
6. FEED-3 固定样本压测、候选索引计划、目标判定；保存第二阶段扇出对照实验基线，不在第一阶段实施扇出。
7. 回归历史 `scripts/`，更新验收表、截图和原始数值，只把真实通过的项标记通过。
