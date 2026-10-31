# v1.0.0 发布验收记录

## 授权与范围

用户明确要求发布 v1.0.0，随后明确指定“先发布 v1.0.0，发布完成后再删除9个v0.x.x 和 GitHub Release”。执行顺序为发布并完成远端消费后，清理九个旧 tag 与现存八个 Release；v0.1.0 没有现存 GitHub Release。该授权取代默认发布前清理顺序，不允许覆盖同名正式 tag 或跳过候选验证。

本次只维护发布文档、记录和版本对象，未修改 Go/SQL/依赖/公开行为。整合归档提交 35a768f 已以独立 merge commit 合入本地 develop；发布准备分支为 `chore/prepare-v1-0-0-release`。此前 c9a3bf2 的完整验收只作历史参考，本候选将独立核验。

## 当前进度

发布说明和工件已准备并独立审查通过；准备快照完整验证已通过。最终 main CI、正式 tag/Release、远端消费和旧版本清理仍待执行。后续各阶段仅在有实际证据后追加结果并勾选 tasks。

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

真实提供方/产品网关/部署及 Windows 权限语义不属于本次发布执行范围。完整 Linux 库/服务 race、必需门禁、恢复三阶段和非 root 权限验证会使用一次性依赖；原始日志保留于 `.test-output/v1-release/`，恢复材料保留在脚本创建的 ignored 目录。本变更不自动归档。
