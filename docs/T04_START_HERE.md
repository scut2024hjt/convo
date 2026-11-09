# T04 网页端接力入口

日期：2026-11-09。本轮任务是全站/分类最新流、游标与通用列表装配，唯一实现契约为 `docs/TASK_T04_LATEST_LIST_ASSEMBLY.md`。T04 尚未实现。

## 固定输入

- 仓库：`https://github.com/scut2024hjt/convo`。
- 专用分支：`codex/t04-input-20261109`，固定交接提交以转交消息中的完整 SHA 为准。
- 实现基线：`7468e2830d87902890d263d807f4db14fabbbb91`。T01/T02/T03 已验收并提交本地 main，main 尚未推送；专用分支只在此基线上追加任务与交接工具。
- 清单：`docs/T04_INPUT_MANIFEST.json`，202 个输入路径，身份 `t03-committed-b6b0c5259968d8ad`。
- 清单规范化 SHA-256：`b6b0c5259968d8ad149f13523cdacd349c55615e99fecb039191b0d703bc080c`。

该输入基于已验收的 201 路径 T03 快照，加入 T03 验收记录并更新定版说明。T03 业务实现和修复不变。ACCEPTANCE 文档中的“待提交”保留为验收当时的历史状态，定版后的事实见 `BASELINE_STATUS.md`。

## 接力方式：连接器读文本，执行容器离线运行

网页端此前能够使用 GitHub 连接器读文件，但执行容器没有 GitHub 网络。请继续使用已经成功过的离线流程，不在容器里反复 clone/curl/requests，也不因 ZIP 未挂载而只返回设计。

1. 通过 GitHub 连接器，从本轮固定交接提交读取本文件、`docs/TASK_T04_LATEST_LIST_ASSEMBLY.md`、清单和 `docs/handoff/t04_offline_bootstrap.py` 的完整文本。
2. 将脚本写到执行容器 `/mnt/data/t04_offline_bootstrap.py`。它为 56118 字节 ASCII；文本截断时按行分段取回，保留全部 Base64 行。
3. 核对脚本 SHA-256：`3807021148543580a46bf159a6ab4c54afcf6676d95dc71e95819633ca556a3a`。不一致时修正文本，不运行受损版本。
4. 执行 `python3 /mnt/data/t04_offline_bootstrap.py`。可加 `--source /上轮T03源码目录` 指明原始 194 文件交付或修复版；默认输出 `/mnt/data/convo-t04-offline`。
5. 脚本只读原目录，在新目录应用开发会话的最终修复，写入定版/验收记录，校验全部 202 文件的字节、长度及权限，再建立本地 Git 和 `t04-input` 标签。
6. 保存生成的 `convo-t04-offline-input-report.json`。看到 status=verified、source_files=202 及上述身份后，在新目录真正实现 T04。

合成本地提交与上游 SHA 不同是允许的，不能再次因恢复不了上游 Git 历史停在设计阶段。任务书可通过连接器阅读，无需先写入生成的代码目录。补丁相对本地 `t04-input`，不混用原始 T03 标签。

脚本支持原始 194 路径、已修复 201 路径、带验收记录版、已提交版以及部分修复状态。清单内未知修改会拒绝；清单外文件留在原目录，不带入干净输出。原目录和已有未知输出不会被覆盖。

如果上轮源码确已从容器消失，应报告准确缺失路径，通过连接器取回固定版本或使用真正可读取的完整源码包；不能把“没有源码”与“容器没有网络”混为一谈。脚本只携带修复与校验数据，不凭空携带全部不变文件。

## 可联网或可读取附件的环境

可 checkout 本轮固定交接提交并按清单校验；完整输入包 `convo-t04-input.zip` 作为备用。六个旧主机/编辑配置和额外交接说明不属于 202 文件清单，完整 Git 树中保留它们，离线快照中的省略不是删除请求。

交接不含用户本地 Compose/监控部署、Docker 本地脚本、产品管理文档和新增观测教学内容。不得用交接源码覆盖这些本地工作。

## 本轮边界和交付

T04 新最新流用 MySQL keyset；点赞仍是 T03 Redis-first，写链路和迁移语义不改。默认 Feed 仍为 pull，保留 push。热榜 T05、缓存保护 T06 与混合 Feed 后续单独安排，不借此重写整个系统。

交付 `convo-t04-delivery.zip`，含相对输入的补丁、完整实现、handoff、checks、脱敏证据和 SHA256SUMS。缺 Go/npm 依赖或 Docker 时，准确标注未运行，仍完成实现及可复跑工具，由开发会话做真实服务验收。

## 本机实际验证

见 `docs/handoff/T04_OFFLINE_CHECKS.json`：原始/已验收/带记录/已提交/部分修复、自动发现、重复执行、未知修改与损坏脚本拒绝均通过；六种输出逐项匹配 202 个文件及干净 Git 输入标签。strace 强制 connect/sendto 失败仍可完成，Python/Git 没有 IP 网络调用。

这些是交接工具验证，不能写成 T04 业务已完成或已经在另一网页端容器执行通过。
