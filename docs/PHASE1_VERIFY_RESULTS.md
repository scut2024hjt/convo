# Phase 1 静态验证记录 / 实机待测项

交付工作区从用户上传的 `convo-phase1-handoff-041abff.zip` 解压，以 `041abff` 为输入基线。不能将当前工作区内的工具检查误称为用户 Windows/Linux 环境的运行结果。

| 命令或检查 | 本次结果 | 局限 |
|---|---|---|
| `gofmt -w`（所有变更 Go 文件） | 执行成功，Go 解析/格式正确 | 不等于 Go 构建、类型检查 |
| `GOPROXY=off go test -race ./observability ./pkg/feedcursor` | **PASS** | 只覆盖不依赖第三方库的 Prometheus 输出与游标解析测试 |
| `GOPROXY=off go vet ./observability ./pkg/feedcursor` | **PASS** | 不代表全仓 vet 已完成 |
| `python -m py_compile scripts/phase1/*.py` | **PASS** | 仅 Python 语法，不代表接通 MySQL / HTTP |
| `python scripts/phase1/generate_feed_sql.py --mode fixture --namespace p1_test_01 --reader-id 9007199254740993 --out /tmp/convo_fixture.sql` | **PASS**，生成 3 个作者、7 帖、2 关注，期望顺序 3/2/1/7 | 仅输出 SQL/manifest，没有导入数据库 |
| TypeScript `transpileModule` 解析 `web/src/*.tsx` 和 `web/vite.config.ts` | **PASS**，语法诊断为空 | 没有进行完整 TypeScript 类型检查和 React 构建 |
| npm Arborist `loadVirtual()` | **PASS**，63 条锁文件依赖记录，未发现断链 | 没有实际下载 npm tarball，当前手工恢复的 lockfile 缺 registry integrity 校验值；需要联网安装后复核并归档 |
| `npm ci --offline --ignore-scripts --dry-run --no-audit --no-fund` | **PASS** | Dry run，不等于 `npm ci` 真正安装或 `npm run build` |
| `tinycss2` 检查 `web/src/style.css` | **PASS**，112 条顶层规则/声明，未发现解析错误 | 不能替代浏览器实际渲染 |
| `go build ./...` / `go test -race ./...` | **未执行成功** | Go 第三方模块不在本地缓存，网络不可用；依赖下载超时 |
| `npm ci`（实际安装）/ `npm run build` | **未执行** | npm registry DNS 不可达，缓存无 React/Vite tarball |
| Compose / MySQL / Redis / RabbitMQ / Grafana / 浏览器实机 | **未执行** | 当前容器无 Docker 服务/可用的第三方运行环境 |

## 需要在 Codex 环境回填的证据

- Git commit hash、Go/Node/MySQL/Redis/RabbitMQ 版本、Docker compose 配置、CPU/内存、原始指标/截图；对于未通过项记录具体失败日志。
- `go test -race -count=1 ./...`、`npm ci && npm run build`、两账号 UI 的桌面/窄屏截图和直接刷新路由结果。
- RabbitMQ 暂停 / 恢复时的 Outbox 长度、最老年龄、主/重试/DLQ ready+unacked；真实恢复完成时限，以及 Redis、MySQL 对每个样本最终方向与 version 的对账。
- 实际 SQL 计划、扫描行数/执行时间、低/高关注场景首页/深页 P50/P95、Feed 错误率、写入 P95、写入可见性，保存重复实验原始 JSON 和对应随机种子。
- 针对上述实测结果填入“满足读时拉取目标 / 哪一项超标 / 第二阶段写时扇出对照基线”的结论；本次不预设性能提升比例。
