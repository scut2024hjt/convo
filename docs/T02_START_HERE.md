# T02 Git 源码交接入口

准备日期：2026-11-08。此次交接改用 GitHub 固定提交，解决网页端附件未挂载导致代码与任务书都无法读取的问题。不要继续要求上传同一个 ZIP，也不要以旧 main 或未经本地修复的 T01 源码替代输入。

**输入与阅读顺序**

- 仓库：`https://github.com/scut2024hjt/convo`
- 专用分支：`codex/t02-input-20261108`
- 固定提交：使用本次交接消息明确给出的完整 SHA；开始实现前先核对 HEAD。
- 已验收的 T01 源码身份：`t01-verified-b1c911989f049f65`。
- 本轮唯一实现契约：`docs/TASK_T02_FOLLOW_LIFECYCLE_DELETE.md`。
- 已完成修复与检查：`docs/T01_ACCEPTANCE.md`。
- 输入校验清单：`docs/T02_INPUT_MANIFEST.json`。
- 后续学习范围参考：`docs/CONTENT_STAGE_TASKS.md`、`docs/FEED_HYBRID_PLAN.md`；不把后续任务一次实现。

main 未因这次交接升级。用户本地 README 产品说明、四处 Compose restart=no、运行覆盖文件和管理路线没有进入交接分支。该分支保留原 r2 包省略的六个已提交编辑/部署文件，不要求删除。

**取得与核对输入**

在具备 Git 和网络的执行环境中克隆专用分支，再 checkout 交接消息中的固定 SHA；分支名用于定位，固定 SHA 用于锁定真正输入。随后核对源码清单：

```bash
git clone --branch codex/t02-input-20261108 --single-branch https://github.com/scut2024hjt/convo.git convo-t02
cd convo-t02
# 按交接消息提供的完整 SHA 执行 git checkout，之后再继续。
git rev-parse HEAD
python3 - <<'CHECK'
import hashlib, json
from pathlib import Path
manifest = json.loads(Path('docs/T02_INPUT_MANIFEST.json').read_text())
for item in manifest['source_files']:
    path = Path(item['path'])
    assert hashlib.sha256(path.read_bytes()).hexdigest() == item['sha256'], str(path)
    assert len(path.read_bytes()) == item['bytes'], str(path)
    assert (path.stat().st_mode & 0o777) == int(item['mode'], 8), str(path)
print(manifest['snapshot_id'], len(manifest['source_files']), 'files verified')
CHECK
git tag t02-input
```

已有执行目录可 fetch/checkout 这个固定提交，不要把旧目录中的未提交源码混入输入。已经有 t02-input 标签时，核对它确实指向固定 SHA，不能无条件覆盖它。

如果执行环境不能 Git clone，先尝试 GitHub 该固定提交的源码归档。页面读取和执行容器网络访问是不同能力：仅能浏览源码页面不等于已经取得可执行代码，不应宣称已构建或已验证。连接失败时记录具体失败 URL、HTTP 状态或 Git 错误；不要归因于本机 Windows 路径，也不要用旧源码继续实现。

**本轮范围**

只实现 T02：新关注历史补齐、关注/取关/重新关注代次协调、存量 bootstrap、本人软删除及可靠清理。沿用 Go/Gin、MySQL、Redis、RabbitMQ、React 与既有监控。当前默认 Feed 保持 pull，push 完成独立验收；推拉混合 T02B 保留为后续进阶任务，具体排期在 T02 验收后决定。

先完成后端核心与必要正确性检查，再补 React 删除操作及状态提示。无需现在给用户增加系统压测、故障演练或测试框架教学。

**生成交付物**

补丁相对固定输入生成，不能相对旧 main 或原网页端 T01 生成。实现完成后：

```bash
git add -A
git diff --cached --check t02-input
git diff --cached --binary --full-index t02-input > ../convo-t02.patch
```

若实现过程中已创建新提交，上述 cached diff 仍应与输入标签比较；最终源码、索引和完整实现 ZIP 必须一致。

返回 `convo-t02.patch`、`convo-t02-implementation.zip`、`convo-t02-handoff.md`、`convo-t02-checks.md`、`SHA256SUMS.txt`。handoff 记录输入完整 SHA、源码身份、数据模型、并发协议与迁移；checks 区分实际执行、失败、未执行。不要打包 .git、JWT、真实凭证、数据库卷、依赖或构建产物，不在用户默认库做破坏性测试。

本文件仅说明交接和验收方法，不代表 T02 已实现，也不代表附件挂载问题已经在平台内部修复。
