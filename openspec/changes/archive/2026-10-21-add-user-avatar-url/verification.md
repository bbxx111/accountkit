# 头像属性验收记录

日期：2026-10-21。变更：`add-user-avatar-url`；分支：`feat/add-avatar-url`。当前记录随本次验证更新，不引用历史日志作为本轮通过证据。

## 范围和授权

用户确认仅新增 `avatar_url`，并明确要求“不新增 0002 迁移，改现有 0001”。当前初始结构只增加可空头像列，历史标签、发布 SQL 与冻结源分别保留。实现和验收完成后，用户另行明确要求提交，功能提交为 `4e84a78`；随后明确要求“存档，提交”，授权本轮同步规格、归档及本地提交。合并和推送另行授权。

## 实际执行

**最终结果：** 定版 `bash scripts/verify.sh` 退出 0，包含 `GOWORK=off` 构建、vet、全量 race、793 个必需测试门禁及真实 pg_dump/pg_restore。44 个有测试包、894 个顶层测试、2760 个含子测试条目通过；0 失败、0 data race warning。最终审查通过。全库 OpenSpec strict 的既有三项长度问题仍未通过，见下文。

- 改动前 `GOWORK=off go test -count=1 ./httpapi/enduser ./httpapi/admin ./internal/migrationcheck` 通过。
- TDD：实现前头像 PATCH 返回 `400 MALFORMED_BODY`，新 schema 不含头像列，新增用例按预期失败，修复后通过。非法 UTF-8/孤立 surrogate、U+2028 首尾空白、恢复 seed 缺头像及错误 reason 不一致也分别观察到失败后修复，定向通过。
- `bash scripts/check-generated.sh` 通过，sqlc 生成结果一致。
- `GOWORK=off bash scripts/check-migrations.sh` 通过，输出 `Migration history is immutable; new versions are strictly increasing`。
- 实现定向及完整受影响领域包测试覆盖头像边界、两字段原子拒绝、并发不丢更新、锁后状态复核、公开隐藏、恢复、匿名化回调内 NULL 与失败回滚。`go vet` 受影响包通过。
- 全库普通首轮已执行：41 个包通过，3 个失败用例为 `TestSessionExpiryEndToEndAgainstRealDB`、`TestProfileAttributesConsolidatedV1Repeat`、`TestServiceIntegrationSessionExpiry`，均为新增头像后的旧 DTO 数量/catalog 夹具预期。已修正并定向通过，最终完整 race 也实际覆盖三项通过；服务用例以 Linux 实际执行为准，Windows skip 不计通过。首轮日志保留于 `.test-output/add-user-avatar-url/tests-normal.jsonl`。
- 任务独立审查无 Critical/Important；唯一 Minor 为非字符串头像的 reason 不一致，补 red/green 后修复，限定复审通过。报告为 `.test-output/add-user-avatar-url/task-review.md`、`task-rereview.md`。
- 最终全变更独立审查通过，无遗留发现，见 `.test-output/add-user-avatar-url/final-review.md`。
- Linux `TestServiceIntegrationSessionExpiry` 实际执行通过，25.71 秒，无 skip；日志保存为 `.test-output/add-user-avatar-url/service-expiry-linux.jsonl`。
- 容器 root 下的 `TestProfileAttributesUnreadableFile` 会按用例约定 skip；另用 UID/GID 65534 执行已编译测试二进制，仅运行该用例，实际通过，无 skip。首次参数传递错误未执行用例，修正 PowerShell 参数后通过。
- 普通/race 套件中的另一个 skip 是预期的 `TestRecoveryFixture`；完整恢复脚本强制执行 seed、verify、source-unchanged 三阶段，全部实际通过。头像 seed、独立预期值、恢复后匿名化 NULL 和源库不变断言均执行；恢复材料保留在 `.test-output/recovery.q6bOxU/`。
- `git diff --check` 通过，变更 Go 文件 `gofmt -l` 无输出；JSON 文档可解析，改动文档本地文件链接目标存在。当前 0001 up SHA-256：`491068d7577ca33a94dc9d3f74b02acdbc95662aa954c693b63a3fd7b196b12c`；down、冻结源、主规格和依赖文件均无 diff。
- `openspec validate --all --strict`：本 change 通过；24 项通过、3 项失败。失败均为本次未修改主规格的既有正文长度超过 500 字符：`migration-safety` 的 requirements[4]、`user-profile-extensions` 的 requirements[3]、`versioned-auth-storage` 的 requirements[0]。本次没有放宽验证或改写这些既有规格。

## 测试环境

使用本任务独立 PostgreSQL 17 容器和 Redis 容器；另建不同的空源库、恢复库。Linux 验证镜像实际为 Go 1.26.5、PostgreSQL 客户端 17.11、GCC 14.2。最终设置 `GOWORK=off`、`GOMAXPROCS=4`、`GOFLAGS=-p=4`，容器内独立可写模块及构建缓存；race 由 verify.sh 启用，未禁用任何门禁。原始日志保存于忽略的 `.test-output/add-user-avatar-url/`，最终日志为 `verify.log`、`tests-race.jsonl`、`summary.json`。

完成后按创建时的容器 ID 核验并清理本任务三个容器及匿名测试卷；任务构建缓存和临时执行目录已清理，实施报告及进度记录复制到上述证据目录。没有操作其他既有容器或产品数据库。

## 验证限制与保留证据

修正前 race 运行已停止并保留日志 `verify-before-review-fix.log`、`tests-race-before-review-fix.jsonl`，不将其计作通过。全库普通首轮失败日志保持原样，以修正后定向测试及最终全量 race 为最终行为证据。三个未改主规格的 OpenSpec strict 长文本问题保留为既有验证限制，本次 change 自身严格校验通过。

未运行在线 CI、产品部署、真实产品数据库升级或旧代码混跑/回退测试；本次明确不提供旧数据迁移。头像图片上传、可用性、内容和宿主删除策略不在库验证范围。

## 归档核验（2026-10-21）

- 工件全部完成，6 个实施任务均已完成；归档前逐项核对三个 delta 与主规格的同步结果。
- 新建 `user-basic-profile` 主规格（4 个要求），更新 `host-account-contracts` 的批量公开资料要求，向 `versioned-auth-storage` 增加头像初始定义及旧结构拒绝要求；保留未涉及的场景和历史授权。
- 归档位置为 `openspec/changes/archive/2026-10-21-add-user-avatar-url/`；保留 `.openspec.yaml`，同步修复 AGENTS、迁移手册和实现计划的链接。
- `openspec validate --specs`：27 项通过，退出 0。`openspec validate --all --strict`：24 项通过、3 项既有长文本问题失败，与归档前一致。日志分别保留为 `.test-output/add-user-avatar-url/openspec-archive-specs.txt` 和 `openspec-archive-strict.txt`。
- `openspec list --json`：活动变更为空；文档路径、主规格结构及差异检查通过。本轮仅规格/文档归档维护，没有重复执行 Go、数据库或恢复测试，前文实施证据仍对应功能提交前的定版代码。

## 发布收尾规格复核（2026-10-21）

用户要求处理剩余收尾，并确认当前没有正式部署、无需旧数据库迁移。三处过长要求通过精简正文及将精确细节移至既有场景修复；历史授权、v0.4.0 pin、旧结构无写拒绝、资料隐私及本人访问规则均保留。`openspec validate --all --strict` 当前 27/27 通过、0 警告，独立只读审查 PASS。本节更新此前记录的文档限制状态，不追改旧校验输出，也未修改 Go/SQL 或重跑运行时测试。
