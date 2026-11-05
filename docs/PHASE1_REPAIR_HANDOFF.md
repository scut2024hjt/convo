# Convo Phase 1：Codex 验收修复增量补丁（基线为上一版 Phase 1 完整交付）

## 目标与改动

此修复**只针对**前端构建、HTTP 指标方法基数和 scrape 写出锁竞争、Feed 深页压测失败归档、2/501 粉丝作者的拉模式混合负载。没有添加写时扇出，没有修改原有业务接口/Schema。`web/package-lock.json` 精确替换为用户本轮上传的锁文件，`web/package.json` 保持不变。请把补丁应用到之前的 `convo-phase1-implementation.zip` 对应工作区，**不要**应用到最早的 `041abff` 源码。

| 变更文件 | 行为 |
|---|---|
| `web/package-lock.json` | 采用已含 `typescript`/`vite` 的 `bin`、registry integrity 的新锁文件；Node 22 标准 `npm ci && npm run build` |
| `observability/metrics.go`, `metrics_test.go` | HTTP 标准方法白名单，其余归 `other`；锁内完成 Prometheus 内容渲染，锁外向 `ResponseWriter` 写出；阻塞写出并发测试、方法基数测试 |
| `scripts/phase1/load_feed.py`, `test_load_feed.py` | 业务错误即停止该 worker，分清 `request_failed` 与 `insufficient_depth`；汇总并持久化所有已采集样本后再非零退出 |
| `scripts/phase1/setup_mixed_fans.py`, `test_setup_mixed_fans.py` | API 注册、登录可发帖的 reader/normal/popular；reader 通过 API 关注两作者；隔离 MySQL 生成合成粉丝、查询真实粉丝数、输出不含认证秘密的 JSON；固定 seed、namespace |
| `scripts/phase1/mixed_load.py` | 可读取验证后的粉丝报告，输出 writer 身份、实际粉丝数、随机种子、工作负载参数、发帖/可见延迟（保留逐请求记录） |
| `docs/PHASE1_HANDOFF.md`, `docs/PHASE1_VERIFY_RESULTS.md` | 明确**第二阶段计划实现一版写时扇出并做对照**；第一阶段仍为拉模式，不以超标作为做对照版本的前置条件 |

## A. 本地静态与针对性验证

```bash
# 在仓库根目录执行
GOPROXY=off go test -race -count=1 ./observability ./pkg/feedcursor
GOPROXY=off go vet ./observability ./pkg/feedcursor
python3 -m unittest discover -s scripts/phase1 -p 'test_*.py' -v
python3 -m py_compile scripts/phase1/*.py

# Codex 有网络、Go 依赖可下载时，重新完整执行：
go test -race -count=1 ./...
go vet ./...

# 仅 Node 22 + 干净环境；不要跳过 npm ci 或只做语法解析
cd web
node -v
npm ci
npm run build
test -f dist/index.html
cd ..

# Docker 可用时单独验收镜像构建，报告退出码及完整输出：
docker compose build convo_app
```

指标锁测试通过自定义 `ResponseWriter` 阻塞写出，要求后续 `RecordHTTP` 不被阻塞；多个扩展 HTTP 方法只产生一个 `method="other"`，沿用 Gin `FullPath()` 的静态路由模板。原投票业务状态分类测试必须保留。`/metrics` 仍应只对本地/监控网络提供。

深页错误模拟测试运行 HTTP 200 + JSON `code=1005`，`--depth 2 --per-worker 2 --warmup 0`：脚本退出非零，JSON 包含 `completed=false`、`failure_reason="request_failed"`、`error_counts.business_1005=1`、`requests=1`、`observations` 的真实耗时。正常业务成功但缺少 `next_cursor` 的独立测试报告 `insufficient_depth`，同样归档样本并退出非零。真实压测时先查 `completed` 字段，**不可**把未完成轮次计算为有效深页 P95。

## B. 全自动生成普通/高粉丝账号与关系（仅隔离 Compose 栈）

必须使用**新建、可丢弃**的 `convo-p1-*` Compose 项目，且所有服务端口与现有项目隔离。以下端口只是示例；端口已占用时统一调整，禁止直接操作开发/生产现有卷。

```bash
# 仓库根目录；确保不是现有项目
export CONVO_TEST_PROJECT=convo-p1-fans-01
export CONVO_BIND_HOST=127.0.0.1
export CONVO_APP_PUBLISH_PORT=19091
export CONVO_MYSQL_PUBLISH_PORT=43306
export CONVO_REDIS_PUBLISH_PORT=46379
export CONVO_RABBIT_PUBLISH_PORT=45672
export CONVO_RABBIT_UI_PORT=45673

docker compose -p "$CONVO_TEST_PROJECT" up -d --build
# 第一次启动，等数据库和 API 可用后再继续，先核对项目及端口：
docker compose -p "$CONVO_TEST_PROJECT" ps
docker compose -p "$CONVO_TEST_PROJECT" port convo_app 9090

# 只在当前 shell 设置一次密码，脚本不打印/写入 JWT、密码
export CONVO_TEST_ACCOUNT_PASSWORD='password123'
mkdir -p artifacts/phase1-fans
python3 scripts/phase1/setup_mixed_fans.py \
  --url http://127.0.0.1:19091 --project "$CONVO_TEST_PROJECT" \
  --namespace trial01 --seed 20261104 \
  --normal-fans 2 --popular-fans 501 \
  --out artifacts/phase1-fans/trial01.json
```

脚本自动：从 API `/signup` 和 `/login` 建立 3 个**可以实际登录发帖**的账号；读者用 `/users/:id/follow` 关注两作者；生成 SQL 并导入当前 Compose 的 MySQL；从 `user_follow` 独立 `COUNT(*)` 校验普通作者 2 名、高粉丝作者 501 名，不符即报错并禁止输出成功 JSON。粉丝数包含**一个 API 读者**，另外 1 + 500 个粉丝为合成用户。生成器以命名空间派生不与真实账号重叠的 BIGINT ID，用 seed 决定每位合成粉丝所属作者；合成用户密码不可用于登录。

结果 `trial01.json` 含已核对的 `actual_fans`、`seed`、作者和读者的字符串 ID、生成的合成用户数；`trial01.sql` 留存可审计的生成 SQL（不包含令牌和有效密码）。如中途失败，**不要**直接对原环境重跑 SQL；使用新命名空间或重建隔离项目，不能对非隔离数据执行 `down -v`。

## C. 两位作者的混合读写对照

重新登录三个测试账号取 JWT，令牌**只存在于当前 shell 环境变量**；不要将它们写入测试 JSON、屏幕截图或提交日志。示例使用 `jq`：

```bash
export CONVO_API=http://127.0.0.1:19091
export FANS_MANIFEST=artifacts/phase1-fans/trial01.json

login_token() {
  curl -fsS "$CONVO_API/api/v1/login" \
    -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg u "$1" --arg p "$CONVO_TEST_ACCOUNT_PASSWORD" '{username:$u,password:$p}')" \
    | jq -er '.data.token'
}
export CONVO_TEST_TOKEN="$(login_token "$(jq -r '.accounts.reader.username' "$FANS_MANIFEST")")"

# 普通作者，粉丝数实测 2（若不为 2，setup 已拒绝报告）
export CONVO_TEST_WRITER_TOKEN="$(login_token "$(jq -r '.accounts.normal.username' "$FANS_MANIFEST")")"
python3 scripts/phase1/mixed_load.py --url "$CONVO_API" \
  --run-id trial01_normal --fans-manifest "$FANS_MANIFEST" --writer-role normal \
  --seconds 30 --readers 4 --write-interval-ms 1500 --community-id 1 \
  --out artifacts/phase1-fans/normal.json

# 高粉丝作者，粉丝数实测 501
export CONVO_TEST_WRITER_TOKEN="$(login_token "$(jq -r '.accounts.popular.username' "$FANS_MANIFEST")")"
python3 scripts/phase1/mixed_load.py --url "$CONVO_API" \
  --run-id trial01_popular --fans-manifest "$FANS_MANIFEST" --writer-role popular \
  --seconds 30 --readers 4 --write-interval-ms 1500 --community-id 1 \
  --out artifacts/phase1-fans/popular.json
```

两份 JSON 都包含 `fans.actual_fans`、`fans.seed`、`fans.writer_id`、运行参数、读请求数与延迟、`post_p50_ms`/`post_p95_ms`、`visibility_p50_ms`/`visibility_p95_ms`、未在超时窗口可见次数，以及逐请求 `write_observations` (`ms`/`visible_ms`/`error`)。发帖在对应作者真实 JWT 下走 API、读者从同一个拉模式 Feed 查询新帖。请保存报告与 `docker stats --no-stream`、Git SHA、运行环境；不要把高粉丝规模视为已实现扇出或推测出性能收益。

如需核对 MySQL 原始关系，也可从清单取真实字符串 ID 并运行以下**现成命令**，无需手写 SQL：

```bash
for role in normal popular; do
  author_id=$(jq -r ".accounts.$role.id" "$FANS_MANIFEST")
  printf '%s author_id=%s followers=' "$role" "$author_id"
  printf 'SELECT COUNT(*) FROM user_follow WHERE followee_id = %s;\n' "$author_id" | \
    docker compose -p "$CONVO_TEST_PROJECT" exec -T mysql8019 sh -c \
      'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot convo --batch --skip-column-names'
done
```

## D. 阶段结论和待 Codex 填写的真实记录

第一阶段保持**读时拉取**。第二阶段将实现写时扇出**对照版本**，即使拉模式已达实验指标也进行同等负载比较；之后依据 P95 收益与写放大、回补、去重、可见性延迟、积压恢复成本决定最终技术选型。两种作者粉丝数是写负载规模，不等同于读者的关注作者数。

Codex 应分别附上：`go test -race ./...`、`go vet ./...`、Node 22 `npm ci && npm run build`、`docker compose build convo_app` 的退出码和日志；本地阻塞写入测试、`business_1005` 测试；SQL 真实粉丝数截图/输出、`normal.json` 与 `popular.json`、环境资源、是否达到实验目标。不要用本地 mock 结果充当 DB 实测结果。
