# convo

基于 Go 的社区论坛后端服务，支持用户注册登录、社区/帖子管理、发帖、投票，以及按时间或热度分页的帖子列表。

## 功能

- 用户：注册、登录，JWT 鉴权，密码加盐哈希存储
- 社区与帖子：社区列表/详情、帖子详情、发帖、按时间与热度两种分页
- 投票：帖子赞成/反对，使用 Redis 记录用户投票状态，避免重复投票
- ID 生成：Snowflake 生成用户与帖子 ID
- 通用能力：令牌桶限流、结构化日志、参数校验与中文错误提示、统一响应码

## 技术栈

| 组件 | 用途 |
| --- | --- |
| Gin | HTTP 框架与路由、中间件 |
| MySQL + sqlx | 用户、社区、帖子的持久化 |
| Redis | 投票状态缓存、帖子热度排序（ZSet） |
| JWT | 无状态鉴权 |
| zap + lumberjack | 结构化日志与日志切割 |
| viper + fsnotify | 配置加载与热更新 |
| validator | 请求参数校验 |
| Swagger | 接口文档（`/swagger/index.html`） |
| pprof | 运行时性能分析（`/debug/pprof`） |

## 架构

按 `controller` / `logic` / `dao` 分层，业务逻辑与数据访问解耦：

```
main.go              程序入口、依赖初始化
router/              路由注册与中间件装配
middlewares/         JWT 鉴权、令牌桶限流
controller/          HTTP 参数绑定、响应封装、Swagger 注解
logic/               业务逻辑
dao/mysql/           MySQL 数据访问
dao/redis/           Redis 投票与热度排序
models/              领域模型与请求参数
settings/            配置结构体与加载
pkg/                 jwt、encrypt、snowflake 等基础组件
```

## 接口

| 方法 | 路径 | 说明 | 鉴权 |
| --- | --- | --- | --- |
| POST | `/api/v1/signup` | 用户注册 | 否 |
| POST | `/api/v1/login` | 用户登录 | 否 |
| GET | `/api/v1/community` | 社区列表 | 是 |
| GET | `/api/v1/community/:id` | 社区详情 | 是 |
| GET | `/api/v1/post/:id` | 帖子详情 | 是 |
| GET | `/api/v1/posts` | 帖子列表（按时间/热度分页） | 是 |
| POST | `/api/v1/post` | 发帖 | 是 |
| POST | `/api/v1/vote` | 帖子投票 | 是 |

## 运行

```bash
# 1. 准备 MySQL 与 Redis，并导入建表语句
mysql -h127.0.0.1 -P3306 -uroot -p < init.sql

# 2. 按需修改 conf/config.yaml 中的 MySQL、Redis 连接信息

# 3. 启动
go run main.go conf/config.yaml
```

也可以在 Docker 环境下运行：

```bash
docker-compose up
```

启动后访问 `http://localhost:9090/`，接口文档在 `http://localhost:9090/swagger/index.html`。

## 目录说明

`nginx.conf`、`nginx2.conf`、`nginx3.conf`、`supervisor.conf` 为不同部署场景下的示例配置，本地开发无需关注。

## 待完善

- [ ] 写链路（投票、评论）接入消息队列异步落库
- [ ] MySQL 读写分离与帖子分库分表
- [ ] 缓存与数据库一致性方案、布隆过滤器防穿透
- [ ] 投票链路的 Lua 原子化与接口幂等
- [ ] Feed 流与热度分衰减
- [ ] Prometheus 指标采集与压测数据
- [ ] 升级 `jwt-go` 至 `golang-jwt/jwt/v5`，消除已知 CVE

## License

MIT
