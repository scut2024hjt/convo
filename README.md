# convo

> 基于 Go 的社区论坛后端。把「投票」这条高频写链路做成**可解释、可验证、可恢复**的完整闭环。

[![Go](https://img.shields.io/badge/Go-1.20%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![CI](https://github.com/scut2024hjt/convo/actions/workflows/ci.yml/badge.svg)](https://github.com/scut2024hjt/convo/actions/workflows/ci.yml)

投票接口写 Redis 后立即返回，落库交给消息队列异步完成；Redis 只持有**可重建的派生状态**，
MySQL 是权威存储。围绕这两条主线，项目实现了：

**原子计票** · **写链路解耦** · **消息不丢不重** · **缓存一致性** · **离线重建** · **多实例正确性**

---

## 目录

- [它解决什么问题](#它解决什么问题)
- [核心特性](#核心特性)
- [架构](#架构)
- [投票写链路](#投票写链路)
- [快速开始](#快速开始)
- [接口](#接口)
- [配置](#配置)
- [关键设计取舍](#关键设计取舍)
- [可靠性边界](#可靠性边界)
- [Redis 状态恢复](#redis-状态恢复)
- [测试与验证](#测试与验证)
- [已知限制](#已知限制)
- [Roadmap](#roadmap)
- [贡献](#贡献)
- [License](#license)

---

## 它解决什么问题

论坛的投票是典型的**高频小写入 + 高并发读热度榜**场景，直接写库会遇到三个问题：

1. **写入成为瓶颈**——每次投票一次事务，热点帖子上锁竞争严重；
2. **计数会算错**——「读旧值 → 判断 → 写新值」在并发下会丢更新；
3. **缓存与库不一致**——更新完库忘了处理缓存，或并发读写把旧值写回缓存。

项目的做法是：**投票状态和热度收进 Redis，用一段 Lua 保证「判重—计数—计热度—记事件」原子完成；
事件先进 Redis Stream Outbox，再由 relay 可靠投递到 RabbitMQ 异步落库。**
接口不等待数据库，Redis 里的状态又始终可以从 MySQL 重建。

---

## 核心特性

| 能力 | 实现要点 |
|---|---|
| **原子计票** | `vote.lua` 单次执行完成：校验时间窗 → 判重 → 更新投票方向 ZSet → 更新热度 ZSet → 递增事件版本 → 写 Outbox Stream |
| **写链路解耦** | 投票只写 Redis 即返回；落库由 RabbitMQ 异步消费，**MQ 停机时接口仍可正常返回** |
| **消息不丢不重** | Redis Stream Outbox + Publisher Confirm + durable/persistent + 手动 ACK + TTL 重试队列 + 死信队列；消费端 `event_id` 去重 |
| **乱序防护** | 事件携带单调递增 `version`，MySQL 侧 `IF(VALUES(version) > version, ...)` 保证旧事件不覆盖新状态 |
| **防刷** | 用户级**精确滑动窗口**限流（ZSET 记录请求时间戳，Lua 原子判重与计数）+ 同方向重复投票幂等 |
| **缓存一致性** | 帖子详情 Cache-Aside，更新走「先写库 → 删缓存 → 延迟 500ms 再删一次」 |
| **离线重建** | Redis 派生状态全丢后，停写 + 排空 Outbox，从 MySQL 分批重建帖索引、热度、投票方向与**版本 tombstone** |
| **带病拒绝启动** | 启动时校验 `MySQL 帖子数 == Redis 索引数` 与状态标记，不一致直接**拒绝服务**并给出修复命令 |
| **多实例** | Snowflake `machine_id` 可配置；限流 ZSET member 用 128 位随机 nonce，跨实例同毫秒不冲突 |

---

## 架构

```mermaid
flowchart LR
    C["Client"] -->|HTTP| API["Gin API"]
    API --> L["logic"]
    L -->|"投票 / 发帖 / 会话"| R[("Redis")]
    L -->|"读写"| M[("MySQL")]

    R -.->|"XREADGROUP"| RELAY["worker: vote_relay"]
    RELAY -->|"Publish + Confirm"| MQ[("RabbitMQ")]
    MQ -.->|"Delivery"| CONS["worker: vote_consumer"]
    CONS -->|"事务 + 幂等去重"| M
```

```
main.go                 入口：依赖初始化、启动完整性校验、优雅停机
cmd/rebuild-redis/      停写后的 Redis 派生状态重建命令（独立二进制）
router/                 路由注册与中间件装配
middlewares/            JWT + Redis Session 鉴权、用户级投票限流
controller/             HTTP 参数绑定、统一响应、Swagger 注解
logic/                  业务逻辑（含启动状态校验与重建编排）
dao/mysql/              业务数据、投票最终态、消费去重、恢复快照查询
dao/redis/              vote.lua、Stream Outbox、帖子缓存、Session、限流、重建
mq/                     RabbitMQ 拓扑声明与 Confirm 发布
worker/                 Stream relay 与投票持久化消费者
models/                 领域模型与请求参数
settings/               Viper 配置加载与校验
pkg/                    jwt / encrypt / snowflake
scripts/                可复现的验证脚本（见「测试与验证」）
```

---

## 投票写链路

```mermaid
sequenceDiagram
    autonumber
    participant C as Client
    participant A as API
    participant R as Redis
    participant Y as Relay
    participant Q as RabbitMQ
    participant W as Consumer
    participant M as MySQL

    C->>A: POST /api/v1/vote
    A->>R: EVALSHA vote.lua
    Note over R: 校验时间窗 · 判重 · 计数<br/>更新热度 · 递增版本 · XADD<br/>一次原子提交
    R-->>A: changed, version
    A-->>C: 200 立即返回

    R-->>Y: XREADGROUP 消费组
    Y->>Q: Publish Confirm + mandatory
    Q-->>Y: Ack
    Y->>R: XACK + XDEL

    Q-->>W: Delivery
    W->>M: 事务写去重表并 UPSERT 投票态
    W->>Q: 手动 Ack
```

**语义约定**：投票接口返回成功，表示 Redis 实时状态与 Outbox 事件已**原子提交**，
不表示已落库。任一确认点之前进程退出都会导致重投，由 `event_id` 与 `version` 保证最终结果不被重复或乱序破坏。

---

## 快速开始

### Docker Compose（推荐）

```bash
docker compose up -d --build
```

启动后：

| 服务 | 地址 |
|---|---|
| API | http://localhost:9090 |
| Swagger | http://localhost:9090/swagger/index.html |
| pprof | http://localhost:9090/debug/pprof/ |
| RabbitMQ 管理台 | http://localhost:15672 （`convo` / `convo`） |

自检：

```bash
curl -s localhost:9090/ping                     # pong
```

### 本地 Go 运行

```bash
# 1. 起依赖并导入建表语句
docker compose up -d redis507 mysql8019 rabbitmq
mysql -h127.0.0.1 -P33306 -uroot -p123456 < init.sql

# 2. 按需修改 conf/config.yaml（连接信息、JWT secret）

# 3. 启动
go run .
```

> 若 RabbitMQ 的 `5672` 无法映射到宿主机（Windows 上常见，落入 Hyper-V 保留端口段），
> 可复制 `scripts/compose.override.local.yaml.example` 为 `docker-compose.override.yaml`
> 改映射到 35672 —— Docker Compose 会自动加载，无需 `-f`。

### 走一遍完整流程

```bash
BASE=http://localhost:9090/api/v1

curl -s -X POST $BASE/signup -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"password123","re_password":"password123"}'

TOKEN=$(curl -s -X POST $BASE/login -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"password123"}' | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["token"])')

POST_ID=$(curl -s -X POST $BASE/post -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN" \
  -d '{"community_id":1,"title":"hello","content":"world"}' | python3 -c 'import json,sys;print(json.load(sys.stdin)["data"]["post_id"])')

curl -s -X POST $BASE/vote -H 'Content-Type: application/json' -H "Authorization: Bearer $TOKEN" \
  -d "{\"post_id\":\"$POST_ID\",\"direction\":\"1\"}"

curl -s "$BASE/post/$POST_ID"
curl -s "$BASE/posts2?order=score&page=1&size=10"
```

---

## 接口

| 方法 | 路径 | 说明 | 鉴权 |
| :-- | :-- | :-- | :-: |
| POST | `/api/v1/signup` | 用户注册 | |
| POST | `/api/v1/login` | 用户登录 | |
| POST | `/api/v1/logout` | 退出当前会话（只失效本次 token） | ✅ |
| GET | `/api/v1/community` | 社区列表 | |
| GET | `/api/v1/community/:id` | 社区详情 | |
| GET | `/api/v1/post/:id` | 帖子详情（Cache-Aside） | |
| GET | `/api/v1/posts` | 基础帖子列表 | |
| GET | `/api/v1/posts2` | 按时间/热度分页（`order=Time\|score`，支持 `community_id`） | |
| POST | `/api/v1/post` | 发帖 | ✅ |
| PUT | `/api/v1/post/:id` | 编辑自己的帖子 | ✅ |
| POST | `/api/v1/vote` | 投票（`direction`: `1` / `0` / `-1`） | ✅ |

响应统一为 `{"code":1000,"msg":"success","data":{...}}`，业务错误码见 `controller/code.go`。

---

## 配置

`conf/config.yaml` 提供默认值，所有键都可用环境变量覆盖：
前缀 `CONVO_`，层级点号换成下划线。

| 配置项 | 环境变量 | 默认 |
| :-- | :-- | :-- |
| `mysql.host` / `mysql.port` | `CONVO_MYSQL_HOST` / `CONVO_MYSQL_PORT` | `127.0.0.1` / `33306` |
| `redis.host` / `redis.port` | `CONVO_REDIS_HOST` / `CONVO_REDIS_PORT` | `127.0.0.1` / `36379` |
| `rabbitmq.url` | `CONVO_RABBITMQ_URL` | `amqp://convo:convo@127.0.0.1:5672/` |
| `auth.jwt_secret` | `CONVO_AUTH_JWT_SECRET` | 示例值，生产必须覆盖 |
| `snowflake.machine_id` | `CONVO_SNOWFLAKE_MACHINE_ID` | `1`（多实例必须各不相同） |
| `ratelimit.vote_max_requests` | `CONVO_RATELIMIT_VOTE_MAX_REQUESTS` | `10` |
| `ratelimit.vote_window_milliseconds` | `CONVO_RATELIMIT_VOTE_WINDOW_MILLISECONDS` | `1000` |

设为 `CONVO_APP_MODE=prod` 时，示例 JWT secret 会被拒绝启动。

---

## 关键设计取舍

面试与 review 时最常被追问的几处，把「为什么」写在这里。

<details>
<summary><b>为什么用 Lua 而不是分布式锁？</b></summary>

投票要改多个结构（投票方向、热度分、事件版本、Outbox），必须原子完成。

- 分布式锁：至少 2 次网络往返（加锁 + 解锁），还有锁超时、误删他人锁、持锁进程崩溃等问题；
- Lua：Redis 单线程执行脚本，天然串行，**一次 RTT** 完成全部操作，没有锁泄漏风险。

代价是脚本内不能做慢操作，所以预热帖子时间、参数全部由客户端算好传进去。
</details>

<details>
<summary><b>为什么用 Redis Stream 做 Outbox，而不是 MySQL 本地消息表？</b></summary>

两者都是解决「本地事务 + 消息投递」的经典方案，这里选 Stream 是因为：

- 投票状态**本来就在 Redis**，把事件 `XADD` 放进同一段 Lua，业务写入与事件产生**天然同一次原子提交**，不需要额外的事务边界；
- 省掉一次数据库写入，投票路径完全不碰 MySQL；
- 消费组语义自带：`XREADGROUP` 保证同一条消息只投给一个 consumer，`XACK` 确认，`XCLAIM` 接管宕机实例遗留的 pending。

MySQL 本地消息表则需要业务写库 + 插消息表放在同一事务里，再起定时任务扫描投递，链路更长。
</details>

<details>
<summary><b>为什么不做在线自动对账，而要求停写 + 离线重建？</b></summary>

因为**正常运行时 Redis 可能领先于 MySQL**：投票先写 Redis，落库是异步的，
消息可能还在 Outbox 或 RabbitMQ 里没消费。

此时若用 MySQL 去「修正」Redis，等于把**合法的新投票回滚掉**，制造出比原问题更严重的不一致。

所以恢复流程被设计成显式的运维动作：**停写 → 排空 Outbox 与队列 → 从 MySQL 重建**，
并且重建期间把状态标记为 `rebuilding`，应用拒绝使用不完整状态。
`--confirm-maintenance` 是刻意的第二道闸门，没有它命令直接拒绝执行。
</details>

<details>
<summary><b>重建为什么要恢复「版本号」，而不只是票数？</b></summary>

这是最容易踩的坑：如果只恢复票数和方向，用户的 `version` 会从 1 重新开始，
而 MySQL 里已经是 `version=3`，消费端的 `IF(VALUES(version) > version, ...)` 恒为假 ——
**用户之后的投票会被静默丢弃**，形成永久性单向不一致。

所以重建时对每一个 `(user_id, post_id)` 都恢复最高版本号，
包括 `direction = 0`（已取消投票）的记录 —— 它虽然不在投票 ZSet 里，
但版本必须保留，相当于一个 tombstone。
</details>

<details>
<summary><b>为什么限流的 ZSET member 要用随机 nonce？</b></summary>

滑动窗口用 ZSET 记录每次请求的时间戳，member 必须唯一。

最初的实现是「毫秒时间戳 + 进程内自增序号」。单机没问题，**多实例下会出错**：
两个实例可能在同一毫秒生成相同的 member，`ZADD` 把两条记录合并成一条，
`ZCARD` 少算，**实际放行数就会超过额度**。

改为「毫秒时间戳 + 128 位随机 nonce」后，跨实例同毫秒也不会碰撞。
这是「单机测试通过 ≠ 分布式正确」的典型案例。
</details>

<details>
<summary><b>为什么删缓存而不是更新缓存？</b></summary>

并发更新时，两个请求的「写库 → 更新缓存」顺序可能交错，后写缓存的可能是旧值。

删缓存则让下一次读回源拿最新值。为了兜住「删缓存之后、读请求把旧值写回」的窗口，
再加一次延迟删除（本项目 500ms）。这不是强一致方案，只是**把脏缓存窗口压到很小**，
最终由缓存 TTL 兜底 —— 这个边界在「已知限制」里也如实写了。
</details>

---

## 可靠性边界

投票接口返回成功 = Redis 实时状态与 Outbox 事件已原子提交。
Relay 只有收到 RabbitMQ Confirm 后才删除 Stream 消息；消费者只有在 MySQL 事务成功、
或确认事件重复/过期后才 ACK。

| 故障 | 行为 | 验证 |
| :-- | :-- | :-- |
| **RabbitMQ 不可用** | 投票仍返回成功（实测 7~10ms），事件留在 Outbox Stream；MQ 恢复后**自动补偿落库**，实测 10 秒内收敛 | 真机故障注入 |
| **消费进程退出** | 消息留在队列，重启后继续消费；MySQL 用 `event_id` 去重 | 真机验证 |
| **Redis 不可用** | 投票快速失败（8ms，不写半成品数据）；帖子详情降级直查 MySQL | 真机验证 |
| **Redis 数据丢失** | 详情回源 MySQL 返回正确票数；列表/发帖/投票拒绝使用不完整状态；重启时完整性检查**拒绝带病启动**，排空后可用重建命令恢复 | 真机演练（含版本连续性断言） |
| **重复投递 / 乱序事件** | `event_id` 去重；`version` 保证旧事件不覆盖新状态 | 集成测试 + DLQ 空断言 |

---

## Redis 状态恢复

Redis 中的帖子时间、热度、用户投票方向和版本号**全部可由 MySQL 重建**。

```bash
# 1. 从网关摘除实例或停止外部请求（阻止新增投票），暂时保留 consumer 运行
# 2. 等待所有队列归零
docker compose exec rabbitmq rabbitmqctl list_queues name messages_ready messages_unacknowledged

# 3. 停掉全部应用实例，再次确认已排空
docker compose stop convo_app

# 4. 重建（不带 --confirm-maintenance 会被拒绝执行）
docker compose run --rm convo_app ./convo_rebuild_redis --confirm-maintenance

# 5. 恢复服务
docker compose up -d convo_app
```

或本地 Go 环境：

```bash
go run ./cmd/rebuild-redis --confirm-maintenance
```

命令分批读取 MySQL，按 `create_time + SUM(direction) × 432` 重算热度，
恢复社区索引、投票方向与每个用户的最高版本。
**重建中途失败会保留 `rebuilding` 标记，应用不会启动**，排除故障后重新执行即可。

---

## 测试与验证

```bash
# 单元测试
go test ./...

# 集成测试（需要 MySQL / Redis 在跑）
go test -tags=integration ./...

# 竞态检测
CGO_ENABLED=1 go test -tags=integration -race ./...

# 静态检查（注意 integration 标签要单独 vet，否则测试文件不会被编译）
go vet ./... && go vet -tags=integration ./...
```

集成测试跑在 Redis 的 **15 号库**，可与本地运行中的实例共存。
若与实例共用 db 0，实例里的 relay 会消费掉测试写入的 Outbox 事件，导致断言随机失败。

### 可复现的验收脚本

`scripts/` 下的脚本把上面的结论做成**一条命令出结果**，方便自己或面试官复现：

| 脚本 | 验证内容 |
| :-- | :-- |
| `./scripts/verify_recovery.sh` | 灾难恢复完整链路：建帖投票 → 排空 → FLUSHDB → **拒绝启动** → 离线重建 → 列表/票数/方向/版本恢复 → 老帖改票 version **连续增长** → MySQL 收敛 → DLQ 空 |
| `./scripts/verify_ratelimit.sh` | 双实例并发**不超发**、用户维度隔离、窗口结束后额度恢复 |
| `./scripts/verify_resume_features.sh` | 注册登录、发帖查询、投票幂等、热度排序、单设备登录等接口逐个打一遍 |
| `./scripts/fault_test.sh` | 停掉 RabbitMQ 后投票仍成功、事件不丢、恢复后自动收敛 |
| `./scripts/multi_instance_test.sh` | Snowflake ID 不冲突、跨实例投票一致 |
| `cd scripts/verify && go run ./e2e` | 端到端：200 并发投票、缓存一致性、限流、单设备登录 |

退出码 `0` 表示全部通过。详见 [`scripts/README.md`](scripts/README.md)。

> ⚠️ `verify_recovery.sh` 会执行 `FLUSHDB`，只在测试环境运行；脚本会自己走完重建把状态修回来。

---

## 已知限制

项目**刻意不做**超出当前需求的东西，也不宣称未验证的能力。以下是明确的边界：

**分布式与一致性**

- Redis 恢复是**显式维护流程**，不是在线自动对账（原因见[设计取舍](#关键设计取舍)）。
  在「AOF 未落盘且事件也未进入 RabbitMQ」的最后窗口内产生的投票无法从 MySQL 恢复。
- 发帖仍是 MySQL → Redis 的跨存储写入：Redis 初始化带 3 次重试与明确日志，
  持续失败需执行重建命令；项目**不宣称跨存储强一致**。
- 投票时间取服务端时间，未对齐客户端时钟；跨机房部署需要额外的时钟假设。

**存储增长**

- `post:voted:{id}` 与 `post:vote:version:{id}` **没有 TTL**，随帖子数线性增长。
  加 TTL 会让投票方向失效，需配合重建能力一并设计，暂未做。
- `mq_consumed_message` 只增不删，需要额外的分批归档任务
  （保留窗口必须大于 MQ 最大重投窗口）。

**功能范围**

- 限流是**用户维度**，多账号轮换刷票需要注册风控/设备指纹，本项目未覆盖。
- 没有评论、点赞、关注等社交功能；没有读写分离与分库分表。
- 延迟双删的第二次删除由 `time.AfterFunc` 在进程内调度，进程重启会丢失这次兜底删除
  （最终由缓存 TTL 兜底）。

**可观测性**

- 有结构化日志（zap）、pprof、Swagger；**未接入 Prometheus / Grafana**。

---

## Roadmap

按「追问深度 ÷ 实现成本」排序：

- [ ] **压测数据**：用 wrk / k6 补核心接口的 QPS 与 P99，如实标注环境
- [ ] 缓存防护：详情缓存加**随机 TTL** 防雪崩，加**布隆过滤器**防穿透
- [ ] 可观测性：暴露 `/metrics`（投票 QPS、缓存命中率、MQ 堆积、限流拒绝数）并配 Grafana 看板
- [ ] 熔断降级：自研滑动窗口熔断器
- [ ] 冷数据治理：结合重建能力，为冷帖子的投票状态设计 TTL
- [ ] 消费去重表分批归档任务

---

## 贡献

欢迎提 Issue 与 PR。提交前请确保：

```bash
gofmt -l .                                  # 应无输出
go vet ./... && go vet -tags=integration ./...
go test -race ./...
```

---

## License

[MIT](LICENSE)

本项目基于一个开源 Go Web 教程项目二次开发，逐步演进为当前的异步投票链路与恢复能力；
原始版权声明按 MIT 协议要求保留。
