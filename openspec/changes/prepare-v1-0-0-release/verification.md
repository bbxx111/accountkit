# v1.0.0 发布验收记录

## 授权与范围

用户明确要求发布 v1.0.0，随后明确指定“先发布 v1.0.0，发布完成后再删除9个v0.x.x 和 GitHub Release”。执行顺序为发布并完成远端消费后，清理九个旧 tag 与现存八个 Release；v0.1.0 没有现存 GitHub Release。该授权取代默认发布前清理顺序，不允许覆盖同名正式 tag 或跳过候选验证。

本次只维护发布文档、记录和版本对象，未修改 Go/SQL/依赖/公开行为。整合归档提交 35a768f 已以独立 merge commit 合入本地 develop；发布准备分支为 `chore/prepare-v1-0-0-release`。此前 c9a3bf2 的完整验收只作历史参考，本候选将独立核验。

## 当前进度

发布说明和工件已准备并独立审查通过；准备快照完整验证、最终 main CI、正式 tag/Release、两个远端消费者和旧版本清理均已完成。当前文档已补记为已发布；补记分支集成结果见后续记录。

## 准备审查与静态门禁

- 独立 reviewer 核对发布范围、Go API 替换、数据库及回退边界、两份 0001 SHA256、Go 版本、24/11 HTTP 操作和证据阶段划分。首次发现工件仍残留先清理顺序及任务引用不一致；修正后复核全部关闭，结论可合并，无剩余 Critical/Important/Minor。首次 Python 中文文本替换未匹配成功，改用明确补丁并逐文件核对，未执行任何错误顺序的远端动作。
- `openspec validate --all --strict`：41 pass、0 fail；本发布纯文档/执行变更的 skip_specs 有效。
- sqlc 1.31.1 下 `bash scripts/check-generated.sh` 退出 0、无生成差异；OpenAPI 检查 24/11 操作及会话上下文边界通过；`git diff --check` 通过。
- 对 c9a3bf2 核对所有 Go、SQL、go.mod/go.sum 无差异；本次范围仅文档与 OpenSpec 记录。初始 up/down 哈希与发布说明一致。

## 准备快照完整 Linux 验证

对象为归档合并基点 `ba46565cbc2437a5518458d518ff8d61d8317c88` 加本次发布文档工作树；运行期间只修正了不参与运行时的发布顺序工件，未改 Go/SQL/依赖。当前 tests/Dockerfile 构建 `accountkit-verify:v1-release`，镜像 ID `sha256:d7764c3b2a1f08436ba084b5a4f9360327fee0fb6d12b0981380adfa45fb7f74`，Go 1.26.5 linux/amd64、GCC 14.2.0、GOWORK=off、CGO_ENABLED=1、PostgreSQL 17、Redis 7、pg_dump/pg_restore 17.11。

| 检查 | 结果 |
|---|---|
| 完整库 `bash scripts/verify.sh` | 退出 0；迁移历史、build/vet、全仓 race、981 项必需门禁和恢复演练通过；顶层 1192 pass、0 fail、2 skip，含子测试 3552 pass。 |
| 服务 `bash scripts/verify-accountsvc.sh` | 退出 0；build/vet、真实进程/race 和 62 项必需门禁通过；顶层 203 pass、0 fail、1 skip，含子测试 820 pass。 |
| 恢复三阶段 | 新建 recovery_source/recovery_target，脚本写前验证实际不同且均为空；seed、verify、source-unchanged 各 1 pass、0 skip，真实 dump/restore 完成。材料 `.test-output/recovery.d1adV2/`。 |
| 非 root 权限补验 | 同镜像编译 accountsvc 测试二进制，runuser -u nobody 执行 TestProfileAttributesUnreadableFile：1 pass、0 skip，退出 0。 |
| 文档引用 | 当前修改文档及新文档的 84 个本地/仓库 blob 目标路径检查通过；未来 v1.0.0 blob 链接按本地对应路径核对，不冒称 tag 已存在。 |

套件 skip 仅为普通套件不执行的 TestRecoveryFixture 与 root 下的文件权限拒读用例，均已分别补验；无缺少 PostgreSQL/Redis 的 skip，未放宽必需清单。串行 runner 退出 0。日志和 summary.json 保存在 `.test-output/v1-release/`，库 race 为 02:27:02–02:29:33 UTC，服务为 02:29:49–02:33:00 UTC。

只清理带 `accountkit.task=v1-release-20261031` 标签的本轮 runner、PostgreSQL、Redis 容器及匿名卷/专用网络；备份、日志、既有测试和产品容器、命名 Go 缓存均保留。该结果证明准备快照，不替代下一阶段最终 main SHA 的在线 CI。

## 验证边界

真实提供方/产品网关/部署及 Windows 权限语义不属于本次发布执行范围。完整 Linux 库/服务 race、必需门禁、恢复三阶段和非 root 权限验证已使用一次性依赖完成；原始日志保留于 `.test-output/v1-release/`，恢复材料保留在脚本创建的 ignored 目录。本变更不自动归档。

## 最终候选与正式发布

- 发布准备提交 `d6fe357`，develop 合并提交 `e7775ac54ab60f8903ab63380bd35ada92d9cf69`，最终 main 候选为 `e1b93da7d050bce6e94766412a81f5c11d994b1f`；两次合并均保留独立 merge commit，已推送。main 与已验证准备分支的文件树一致，发布前工作区干净。
- [main 候选 CI 41160615369](https://github.com/bbxx111/accountkit/actions/runs/41160615369) completed/success，headSha 精确匹配上述候选；生成代码、完整库及恢复、accountsvc 真实依赖与进程信号步骤全部 success。[develop CI 41160615371](https://github.com/bbxx111/accountkit/actions/runs/41160615371) 同样 success。CI JSON 与原始日志留在 ignored 验证目录。
- 创建前本地和远端均无 v1.0.0。annotated tag 对象 `f497dec1dd487f18cb52e5ee3df85f438bde1831`，本地及远端 peeled commit 均为 `e1b93da7d050bce6e94766412a81f5c11d994b1f`。
- [GitHub Release](https://github.com/bbxx111/accountkit/releases/tag/v1.0.0) 于 `2026-10-31T02:47:09Z` 发布，draft=false、prerelease=false；正文采用 `docs/releases/v1.0.0.md`，发布后同文件补记对象及结果，tag 原内容保持不动。

## 远端消费与旧版本清理

两个独立临时模块分别为 `example.com/accountkit-v1-root-consumer` 和 `example.com/accountkit-v1-direct-consumer`，均在 Windows/amd64 Go 1.27.1 下设置 `GOWORK=off`、无 replace，从远端 `go get github.com/bbxx111/accountkit@v1.0.0`，随后 `go build ./...`、`go run .` 全部退出 0。根门面编译 `EndUserHandler` 及本次清理后的公开符号；直接适配器编译 `httpapi/enduser.New` / `Router`。Linux Go 1.26.5 的验证证据在上节，不能把本消费环境误记为 1.26.5。

两者 `go list -m -json` 均报告 v1.0.0，无 Replace；模块 Sum 均为 `h1:9t00kLrLyu9lopEnOTssxRWbYSnyRaFGbcUftLNRTIk=`，下载的 0001 up/down SHA256 分别为 `ef2d1f229fa15b660cc7ecb4f417615b76866c19494641c96faf25bab24188b6` / `812541721471025056edd1f655d876b2f4d938fffd8f4f7ee2cfa9a97ff490d5`，与正式候选及冻结值一致。模块文件和 `consumers.json` 留在 `.test-output/v1-release/`。

完成发布和消费后，核对备份与现存对象一致，删除 `v0.1.0、v0.2.0、v0.3.0、v0.4.0、v0.5.0、v0.6.0、v0.7.0、v0.7.1、v0.7.2` 九个本地/远端 tag，以及 v0.2.0 至 v0.7.2 的八个 Release（均无附件；v0.1.0 无 Release）。远端 tag 删除使用原子 push，之后核验远端仅剩 v1.0.0 的对象/peeled 两行，Release 列表仅剩 v1.0.0。历史提交及归档文档保留，未操作其他分支或正式 tag。

删除前元信息为 `tags-before.txt`、`releases-before.json`，删除后为 `tags-after.txt`、`releases-after.json`。清理脚本首次因 Windows PowerShell 数组读取导致预检查中止，未执行删除；修正解析后重新核对所有对象并成功完成，不涉及权限拒绝或放宽前置条件。

## 发布补记校验

发布后补记独立审查通过，无 Critical/Important/Minor；仅改文档和任务/证据记录，Go/SQL/go.mod/go.sum 无差异。`openspec validate --all --strict` 为 41 pass、0 fail，OpenAPI 的 24/11 操作及会话上下文边界检查通过，155 个变动文档的本地路径/实际仓库 blob 引用核验通过，`git diff --check` 通过。GitHub Release 正文已同步此份发布说明，target 元数据明确为正式候选 SHA；正式 tag 不移动。补记合并与推送后追加具体集成提交。
