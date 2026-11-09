# T03 点赞语义与维护迁移

输入源码身份 `t02-verified-7416472d5c379890`；此文件描述 T03 的实现及开发会话复测步骤。默认 Feed 仍是 pull；T03 不实现混合 Feed、热榜算法改版或 Redis 故障直接写 MySQL。

## API

全部沿用认证 JWT 和 `{code,msg,data}`、HTTP 200（包括业务错误）。新增：

| 方法 | 路径 | 成功 `data` |
|---|---|---|
| PUT | `/api/v1/post/:id/like` | `{"post_id":"...","liked":true,"version":"1","changed":true}` |
| DELETE | `/api/v1/post/:id/like` | 结构相同，`liked=false` |
| GET | `/api/v1/post/:id/like` | `{"post_id":"...","liked":false,"version":"0","source":"redis","freshness":"realtime"}` |
| POST | `/api/v1/posts/like-states` | `{"items":[...]}`，请求 `{"post_ids":["123","456"]}` |
| POST（兼容） | `/api/v1/vote` | JSON 字符串 `"post_id"`, `"direction":"1"` 或 `"0"`；保留原 `direction/version/changed` 返回字段类型。`"-1"` 参数错误。 |

批量去重后 1..50 个 ID（原始数组最多 500 项、请求体最多 16KiB），按首次出现顺序关联返回；不存在/软删条目为 `{"post_id":"...","liked":false,"version":"","source":"","freshness":"","code":1009}`，索引待投影可返回 `code:1014`，不伪装正常未点赞。任何批次无法取得可靠后端状态应返回服务忙碌。外部 ID 与新增版本为十进制字符串，SQL 内 BIGINT。个人方向/版本由 Redis 单次 Lua 原子读取，批量最多 50 次原子脚本经一条 pipeline 发送；MySQL 回源单条有界 IN 查询，仅明确标记 `source=mysql,freshness=persisted`。登录本身依赖 Redis Session，Redis 全部中断时认证未必能够通过。

`POST /vote` 与新 PUT/DELETE 共享原用户级限流中间件。写成功是 **Redis Lua 原子修改方向/热度/版本并写入 Stream**，不是 MySQL 同步提交。新增 `like.changed.v1` 是**消息体**的业务类型；RabbitMQ transport routing key / exchange / queue binding 继续为 `vote.changed.v1`，避免声明老 retry/DLQ 队列参数冲突。

## 状态机与版本

0=未点赞、1=已点赞。目标状态不变时返回 `changed=false`，不增加 version、不生成 Stream 事件；真正变化 0→1→0→1 版本递增 1→2→3。无记录且目标 0 返回版本 `0`；取消后的零状态仍保留版本 Hash；不能删零状态版本墓碑。历史帖子不再有 7 日点赞截止，热度加分仍为 `432` 每点赞。用户方向从 Redis zset 读取，零状态不占 zset；版本在独立 Hash 中持久保留。最高允许版本 `9007199254740991`（Lua/JavaScript 可精确表示整数边界），Lua 在写之前拒绝越界及不合法 key 类型；返回和 Stream 用十进制字符串。

旧 `vote.changed.v1` 在新消费者中只能执行下列**不推进**的兼容行为：event_id 已消费→duplicate ACK；版本较旧→stale ACK；同版本且 -1 归一化成 0 后一致→same_version ACK；同版本冲突或任何试图推进 MySQL version 的旧消息（包括没有该行）→可靠送 DLQ，发布失败 Nack 重回原队列。新 `like.changed.v1` 只能携带方向 0/1，按原消费去重、版本单调推进，同版本冲突 DLQ；DLQ/重试出版使用既有 publisher confirms。所有结果标签固定值，不含高基数 ID。

## 迁移 / 重建序列：维护窗口，绝不直接对默认库运行

**准备：** 在隔离环境按 `scripts/t03/prepare_isolated_sql.py --database convo_dev_t03` 生成独立 init SQL，并使用独立 Compose project、容器卷、端口、Redis DB（非 0），隔离资源接受测试。已有库从 001→002→003→004→005 顺序升级；不能在已有库执行带 DROP TABLE 的 `init.sql`。`005_like_semantics.sql` 创建状态/游标表，**不会立即收紧**允许 -1 的 CHECK。全新空库用 init 初始化时直接具备只允许 0/1 的 CHECK 和迁移阶段 `ready`，仅空 Redis DB 的首次初始化可以发布新标记。

**维护步骤（执行前必须确认）**：

1. 备份 MySQL、Redis 数据及配置、原进程版本；关闭对外投票入口及所有其他后台写源，但保留旧 Relay/consumer 把已接受的消息处理完。
2. 核对 Stream `XLEN`、`XPENDING` 总数和 Rabbit 主队列 ready/unacked、retry、DLQ 全部严格为零；任何指标读取失败、HTTP 非 200、无字段、超时或非零都停止。DLQ 不可自动清空；先人工确认处理历史遗留消息。
3. **停止所有旧程序实例**（含 Relay/consumer），完成排空后不允许旧进程再启动。仅有状态标记不能阻止已经运行的旧进程继续写。
4. 使用隔离库（如 `convo_dev_t03`）执行 `005_like_semantics.sql`；MySQL DDL 自动提交，若部分失败先通过 information_schema 判断已创建部分再继续，不能盲目从头重复 CREATE。
5. 运行 `./convo_migrate_likes --confirm-maintenance --database convo_dev_t03 --redis-db 11 --batch-size 250 --rabbit-management-url http://127.0.0.1:55673`，需要 `CONVO_MYSQL_DB=convo_dev_t03`、`CONVO_REDIS_DB=11` 及自己的数据库、RabbitMQ/Redis 连接环境覆盖配置。程序在启动及每批提交前检查 drain；必须用 --database 和 --redis-db 明确指定与当前配置一致的目标；漏填或不一致会在连接数据库前拒绝。
6. 该程序按 `(user_id,post_id)` 递增 keyset 每批 1..1000 行，短事务将 `direction=-1` 的状态改为 0、version+1，使用 `WHERE direction=-1 AND version=?` 防重做；同事务保存检查点及本次/累计统计。方向 0/1、版本、`last_event_id` 不变；非法方向、版本 0、超出精确整数上限和负票溢出拒绝继续。全部转换校验后收紧 `chk_post_vote_direction` 为仅 0/1，结束阶段 `converted`。
7. 再**复查 drain**，运行 `./convo_rebuild_redis --confirm-maintenance --database convo_dev_t03 --redis-db 11 --batch-size 500 --rabbit-management-url http://127.0.0.1:55673`，同样要求明确且匹配配置的目标。验收始终使用隔离 *_t03 / 非零 Redis DB。它先把 Redis 状态标记 `rebuilding:likes:v1`，清理 MySQL 派生的时间/热度、方向和版本索引（不清 Session、限流/Stream、T02 删除墓碑）；按活跃帖子 keyset 恢复并重建，使用 MySQL **direction=1 的人数**计算热度，恢复取消后的 version Hash。软删内容不复活索引。
8. Redis 全量索引核验通过后将 MySQL 迁移阶段置 `ready`、Redis marker 发布 `ready:likes:v1`，才启动新二进制、验收并开放写流量。新程序启动时同时验证两个 ready、约束与现有 T01/T02 成员/待投影检查。旧二进制不识别新 marker，无法直接恢复服务。

**注：** 造数/集成测试工具仍只允许可丢弃的测试资源。维护二进制支持实际应用库及 Redis DB 0，但必须先停写、备份并明确指定目标，且通过真实排空检查；本轮验收不会调用默认资源。显式选择目标不等于自动暂停旧进程。

## 断点、失败与回退

批次事务失败不会提交方向或游标；重跑从 `last_user_id,last_post_id` 继续，不二次加版本。DDL 约束已经更改但 phase 未 ready 可以重新执行检查与重建；Redis 中途断开保持 rebuilding，正常入口启动拒绝。MySQL phase ready 但 Redis marker 未就绪同样拒绝；重新执行严格排空的离线重建后再恢复。新 Redis 状态不会用旧 `ready:v1` 冒充。

**新流量开放之前**：只有恢复对应的 MySQL+Redis 备份并使用旧程序、核对旧队列/事件后，才可以回到旧语义。**新流量开放之后**：不能简单回滚旧二进制、方向逆映射或抹掉新点赞事件，否则会丢失已接受版本；需要单独的数据演进/恢复方案。本轮不实现在线无停机迁移、双写回退、通用 DLQ 重放。

## 开发会话独立验收

使用独立 Compose 文件，不加载默认 Compose 或本机 override。所有端口、容器、网络和数据卷按测试项目隔离。需要 Docker Compose v2，先核对 296xx 端口未占用。

```bash
mkdir -p .t03-local
python3 scripts/t03/prepare_isolated_sql.py --database convo_dev_t03 > .t03-local/init.sql
export CONVO_T03_DB=convo_dev_t03
export CONVO_T03_INIT_SQL="$(pwd)/.t03-local/init.sql"
export CONVO_T03_PASSWORD="$(python3 -c 'import secrets; print(secrets.token_hex(16))')"
export CONVO_T03_JWT_SECRET="$(python3 -c 'import secrets; print(secrets.token_hex(32))')"
# 使用明确、唯一的 -p；不要省略它后再执行 down -v。
docker compose -p convo-t03-local-review -f scripts/t03/compose.standalone.yaml config --quiet
docker compose -p convo-t03-local-review -f scripts/t03/compose.standalone.yaml up -d --build
# React: http://127.0.0.1:29690/app/
# RabbitMQ management: http://127.0.0.1:29673
# MySQL / Redis: 127.0.0.1:29606 / 29679
# 完成后只清理这个测试项目：
docker compose -p convo-t03-local-review -f scripts/t03/compose.standalone.yaml down -v
```

初始化 SQL 只用于全新测试卷，含 DROP TABLE。需要更换端口时使用该文件中的 `CONVO_T03_*_PORT` 环境变量。`CONVO_T03_SOURCE` 可指定源码绝对路径；默认构建当前仓库。测试口令使用十六进制值，避免 AMQP URL 特殊字符编码问题。不要把本机这些环境变量和生成文件打进交付包。

在老库迁移测试中，准备一个旧版 001—004 结构和含负票的**独立** *_t03 样本；执行 005 后先运行转换命令，再运行重建，切忌直接把新 init 创建的 ready 库伪装成旧库转换成绩。所需隔离环境变量值和口令留在本机，不写入本交接包。`go test -tags=integration` 含更早固定端口测试，建议限定新 T03 用例并设置显式隔离端口/库环境；SKIP 不能记通过。

## 核对证据与限制

执行 `scripts/t03/inspect_like_semantics.sql` 的查询记录方向分布、version 范围、去重行数、发布 Outbox、active/soft-deleted 计数。结合 Redis `HGETALL/ZCARD/ZSCORE`（只选脱敏 sample）、Stream `XLEN/XPENDING` 与 Rabbit 管理 API 的三队列 ready/unacked 指标，核对迁移前后。正常版 `PostDetail` 继续使用动态点赞人数；页面只显示点赞/取消，返回时按本人状态恢复，网络未知用回读纠正，旧账号与帖子状态不能覆盖当前页。

开发会话的实际验收结果、证据与边界见 `T03_ACCEPTANCE.md`。网页端原始检查保留为历史输入，不代表开发会话最终测试范围。
