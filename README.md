# convo

基于 Go 的社区论坛后端服务。项目重点实现可解释、可验证的高并发投票链路，同时保留用户、社区和帖子等完整业务接口。

## 功能

- 用户：bcrypt 密码哈希、JWT + Redis 单设备会话、退出登录后立即失效
- 帖子：发布、作者编辑、详情 Cache-Aside、延迟双删、按时间或热度分页
- 投票：Lua 原子状态机，同时更新投票状态、热度分、事件版本和 Redis Stream Outbox
- 防刷：用户级滑动窗口限流（ZSET 精确窗口，Lua 保证判重与计数原子）+ 同方向重复投票幂等
- 异步持久化：RabbitMQ durable/persistent、mandatory + Publisher Confirm、手动 ACK、有限重试和死信队列
- 消费一致性：`event_id` 去重，`version` 防止乱序事件覆盖新状态
- 多实例：Snowflake `machine_id` 可由环境变量配置

## 技术栈

| 组件 | 用途 |
| --- | --- |
| Gin | HTTP 框架与路由、中间件 |
| MySQL + sqlx | 业务数据、投票最终状态、消息消费去重 |
| Redis | 投票状态/热度、Stream Outbox、帖子缓存、登录会话 |
| RabbitMQ | 投票事件可靠传输、重试和死信 |
| JWT | 身份凭证；Redis Session 负责主动失效和单设备登录 |
| zap + lumberjack | 结构化日志与日志切割 |
| Viper | YAML 配置与环境变量覆盖 |
| validator | 请求参数校验 |
| Swagger | 接口文档（`/swagger/index.html`） |
| pprof | 运行时性能分析（`/debug/pprof`） |

## 架构

按 `controller` / `logic` / `dao` 分层，业务逻辑与数据访问解耦：

```
main.go              程序入口、依赖初始化
router/              路由注册与中间件装配
middlewares/         JWT 与 Redis Session 鉴权
controller/          HTTP 参数绑定、响应封装、Swagger 注解
logic/               业务逻辑
dao/mysql/           MySQL 数据访问
dao/redis/           Redis Lua、Stream Outbox、缓存与 Session
mq/                  RabbitMQ 拓扑与 Confirm 发布
worker/              Stream relay 与投票持久化消费者
models/              领域模型与请求参数
settings/            配置结构体与加载
pkg/                 jwt、encrypt、snowflake 等基础组件
```

## 接口

| 方法 | 路径 | 说明 | 鉴权 |
| --- | --- | --- | --- |
| POST | `/api/v1/signup` | 用户注册 | 否 |
| POST | `/api/v1/login` | 用户登录 | 否 |
| GET | `/api/v1/community` | 社区列表 | 否 |
| GET | `/api/v1/community/:id` | 社区详情 | 否 |
| GET | `/api/v1/post/:id` | 帖子详情 | 否 |
| GET | `/api/v1/posts` | 基础帖子列表 | 否 |
| GET | `/api/v1/posts2` | 按时间/热度分页 | 否 |
| POST | `/api/v1/post` | 发帖 | 是 |
| PUT | `/api/v1/post/:id` | 编辑自己的帖子 | 是 |
| POST | `/api/v1/vote` | 帖子投票（用户级滑动窗口限流） | 是 |
| POST | `/api/v1/logout` | 退出当前会话 | 是 |

## 运行

```bash
# 1. 准备 MySQL、Redis、RabbitMQ，并导入建表语句
mysql -h127.0.0.1 -P33306 -uroot -p < init.sql

# 2. 按需修改 conf/config.yaml 中的连接信息和 JWT secret

# 3. 启动
go run .
```

也可以在 Docker 环境下运行：

```bash
docker compose up --build
```

启动后访问 `http://localhost:9090/`，接口文档在 `http://localhost:9090/swagger/index.html`，RabbitMQ 管理界面在 `http://localhost:15672/`（本地账号/密码均为 `convo`）。

如果已有旧 MySQL 数据卷，需要额外执行 `migrations/001_vote_persistence.sql`。部署多个应用实例时，每个实例必须设置不同的 `CONVO_SNOWFLAKE_MACHINE_ID`。

环境变量以 `CONVO_` 开头，并把配置层级中的点替换为下划线，例如 `auth.jwt_secret` 对应 `CONVO_AUTH_JWT_SECRET`、`rabbitmq.url` 对应 `CONVO_RABBITMQ_URL`。生产环境应设置 `CONVO_APP_MODE=prod`，此时示例 JWT secret 会被拒绝启动。

## 可靠性边界

投票接口成功表示 Redis 中的实时状态和 Outbox 事件已原子提交。Relay 只有收到 RabbitMQ Confirm 后才删除 Stream 消息；消费者只有在 MySQL 事务成功，或确认事件重复/过期后才 ACK。进程在任一确认点前退出都会导致重投，并由 `event_id` 和 `version` 保证最终结果不被重复或乱序破坏。

已验证的故障行为：

| 故障 | 行为 |
| --- | --- |
| RabbitMQ 不可用 | 投票仍返回成功（实测 7~8ms），事件留在 Outbox Stream；MQ 恢复后自动补偿落库 |
| 消费者进程退出 | 消息留在队列，重启后继续消费；MySQL 用 `event_id` 去重 |
| Redis 不可用 | 投票快速失败（8ms 返回「认证服务暂不可用」），不会写入半成品数据；帖子详情降级直查 MySQL |
| Redis 数据丢失 | **尚未覆盖**：实时投票状态、帖子时间/热度 ZSet 没有从 MySQL 重建的路径，此时详情接口会返回错误的票数。见下方「已知限制」 |

## 已知限制

- Redis 是「帖子时间/热度排序 + 实时投票状态」的唯一持有者，没有冷启动重建或对账任务；`FLUSHDB` 或持久化文件损坏后无法自愈。
- 限流是「用户维度」的，多账号轮换刷票需要注册风控/设备指纹来兜，本项目未覆盖。
- 投票时间是服务端时间，没有对齐客户端时钟；跨机房部署时需要额外的时钟假设。
- `post:vote:version:{post_id}` 与 `post:voted:{post_id}` 没有 TTL，长期运行会随帖子数线性增长。
- `mq_consumed_message` 只增不删，需要额外的归档任务。
- 延迟双删的第二次删除由 `time.AfterFunc` 在进程内调度，进程重启会丢失这一次兜底删除。

## 测试

```bash
go test ./...

# 启动依赖服务后运行 Redis/MySQL 集成测试
go test -tags=integration ./dao/redis ./dao/mysql
```

集成测试跑在 Redis 的 15 号库上，因此可以和本地正在运行的实例共存。如果和实例共用 db 0，
实例里的 relay 会把测试写入 outbox 的事件消费掉，导致事件计数断言随机失败。

## 目录说明

`nginx.conf`、`nginx2.conf`、`nginx3.conf`、`supervisor.conf` 为不同部署场景下的示例配置，本地开发无需关注。

## License

MIT
