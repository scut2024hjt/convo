# Phase 1 修复验收：当前执行结果（2026-11-04）

基线：上一版 `convo-phase1-implementation.zip`；不是原始 `041abff`。本次仅制作增量修复，不把 Codex 之前在独立环境通过的结果冒称为当前环境复测通过。

| 检查 | 当前执行结果 | 证据或限制 |
|---|---|---|
| `GOPROXY=off go test -race -count=1 ./observability ./pkg/feedcursor` | **PASS** | `observability`（含新的 `other`、阻塞 `ResponseWriter`、既有投票业务结果测试）和 `feedcursor` 均 `ok` |
| `GOPROXY=off go vet ./observability ./pkg/feedcursor` | **PASS** | 仅无需额外模块的两个包 |
| `go test -race -count=1 ./...` | **未通过，依赖不可用** | `GOPROXY=off` 提示多个 module lookup disabled；在线代理重试超时，未能下载 Gin、Redis、Zap 等依赖。不可视为编译失败或成功 |
| `go vet ./...` | **未通过，依赖不可用** | `GOPROXY=off` 同样报第三方模块未缓存，未完成全仓 vet |
| `python3 -m unittest discover -s scripts/phase1 -p 'test_*.py' -v` | **PASS（6 个测试）** | HTTP 200+business_1005 时非零退出、实际样本+错误归档；Feed 深度不足单独报告；合成粉丝 2/501 关系确定性；非法输入；数据库计数不符拒绝清单；mock 计数匹配正常输出无令牌 JSON |
| `python3 -m py_compile scripts/phase1/*.py` | **PASS** | Python 语法和模块导入检查 |
| 用户提供新锁文件与 `web/package.json` | **PASS（静态检查）** | 依赖声明完全一致，`typescript` 有 `tsc`/`tsserver` bin，`vite` 有 vite bin，二者含 integrity；原 `package.json` 不变 |
| Node 22 `npm ci && npm run build` | **未通过（当前网络/依赖不可用）** | Node `v22.16.0`, npm `10.9.2`。`npm ci --offline` 提示缓存缺少 vite tarball (`ENOTCACHED`)；在线 `npm ci` 30 秒超时。后续单独执行 `npm run build` 因缺 `@types/*` 报 TS2688，未生成 `dist`。请 Codex 在可联网且干净环境重跑标准命令，不得视为构建成功 |
| `docker compose build convo_app` | **待 Codex 复测** | 当前容器无 Docker 命令/daemon；需单独记录镜像构建结果 |
| 新增真实 API 账号 + MySQL 2/501 粉丝生成和两次 mixed load | **待 Codex 复测** | 当前无隔离 Compose/MySQL/API；仅有纯 Python/HTTP mock 验证，不能当成真实用户/数据库/性能结果 |

## Codex 必填真实验证

请先 `git apply --check` 并应用增量补丁，再执行 `docs/PHASE1_REPAIR_HANDOFF.md` 的 A、B、C 三段命令；保留以下实测材料：

1. `go test -race -count=1 ./...`、`go vet ./...` 退出码、日志。
2. Node 22 `npm ci && npm run build`、`ls web/dist` 的退出码；Docker 可用时 `docker compose build convo_app` 独立结果。
3. 指标 HTTP 自定义方法仅出现 `other`，慢 `ResponseWriter` 测试通过；原投票业务分类回归通过。
4. depth=2 + 业务码 1005 的 JSON 原样本保留，失败原因为 `request_failed`，不足两页的用例为 `insufficient_depth`。
5. 全新 `convo-p1-*` 项目账号/粉丝数实际查询值：`normal=2`、`popular=501`；两份混合负载结果 `normal.json`/`popular.json`、Git SHA、资源与性能记录。

第二阶段计划实现写时扇出对照，阶段一仍保持拉模式。性能比较和扇出采用决策不能由当前离线测试推断。
