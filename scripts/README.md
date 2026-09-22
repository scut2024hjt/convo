# scripts/ · 可复现的验证脚本

这些脚本用来**复现 README 与验证报告里的结论**。面试官或你自己在本地把中间件起起来后，
可以直接跑出同样的结果，而不是只看到一段文字描述。

## 准备

```bash
# 1. 起中间件（MySQL / Redis / RabbitMQ）
docker compose up -d redis507 mysql8019 rabbitmq

# 2. 如果本机 5672 被占用（Windows 保留端口段常见），用本目录的示例文件
cp scripts/compose.override.local.yaml.example docker-compose.override.yaml
# 它会由 Docker Compose 自动加载（已 gitignore），把 RabbitMQ 映射到 35672
```

> `go run` 相关的脚本用宿主机的 Go 运行，所以需要 `CONVO_RABBITMQ_URL` 指向
> override 之后的端口，本目录的脚本已默认设为 `amqp://convo:convo@127.0.0.1:35672/`。
> 如果你的环境没有端口冲突，用 `CONVO_RABBITMQ_URL=amqp://convo:convo@127.0.0.1:5672/` 覆盖即可。

## 脚本清单

| 脚本 | 验证什么 | 前置 |
|---|---|---|
| `verify_recovery.sh` | **Redis 灾难恢复完整链路**：建帖投票 → 排空 → FLUSHDB → 拒绝启动 → 离线重建 → 列表/票数/方向/version 恢复 → 老帖改票 version 连续增长 → MySQL 收敛 → DLQ 为空 | 中间件在跑 |
| `verify_ratelimit.sh` | **限流**：双实例并发不超发、用户维度隔离、窗口恢复 | 中间件在跑；会自动起两个应用实例（9090/9091） |
| `fault_test.sh` | **MQ 故障解耦**：停掉 RabbitMQ 后投票仍成功、事件留在 Outbox、恢复后自动收敛 | 中间件在跑 + 应用实例 |
| `multi_instance_test.sh` | **多实例**：Snowflake ID 不冲突、跨实例投票与计数一致 | 两个应用实例 |
| `verify/e2e/main.go` | **端到端**：注册登录、建帖、幂等、200 并发投票、缓存一致性、限流、单设备登录 | 应用实例在 9090 |

## 跑法

```bash
cd convo

# 恢复链路（会自己管理应用进程）
./scripts/verify_recovery.sh

# 限流（会自己起两个实例）
./scripts/verify_ratelimit.sh

# 端到端
cd scripts/verify && go run ./e2e

# MQ 故障注入 / 多实例（需要应用已在跑）
cd ../.. && ./scripts/fault_test.sh
```

退出码 `0` 表示全部通过，非 `0` 表示有断言失败。

## 关于 compose override

`compose.override.local.yaml.example` 是一份**本机特定的**示例：某些 Windows 环境下
`5672` 落在 Hyper-V 保留端口段里，Docker 无法把它发布到宿主，所以改成 `35672`。
**项目本身的 `docker-compose.yaml` 没有被这个环境妥协** —— override 是本地文件，
已加入 `.gitignore`，不会进入仓库。
