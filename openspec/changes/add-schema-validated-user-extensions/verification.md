## 当前单列修订验收（2026-10-21）

本轮相对 `9619ff5` 的修订只保留 `profile_attributes JSONB`，移除逐行 Schema 版本、指纹及独立更新时间。Config 仅 Schema、View 仅 Data，删除 SchemaMismatch；行锁内按当前 Schema 校验完整合并结果，实际变化复用账号 update_time，无变化不写入。以下旧验收原文全部保留，仅证明其对应旧实现。

### Behavior Evidence

- 有效 RED：旧 API 下仅兼容调整 Schema 即返回 SchemaMismatch；更新后同一场景 GREEN。SchemaEvolution 覆盖注解、可选字段、放宽约束、收紧后完整候选拒绝（含空补丁）、历史资料读取、失败时间不变、同次补丁修复及未知属性 null 删除拒绝。
- 当前 Schema 一致性由宿主部署保证；库不再绑定历史定义、不自动转换或丢弃旧字段。规范化容量、精确数值、离线编译、声明目录及其他安全限制保持。
- 真实 PostgreSQL 覆盖账号行锁并发、状态竞争、无变化、账号时间、回调失败回滚、关闭或不兼容 Schema 下匿名化及隐私。审计只保留既有服务 actor、目标、请求关联和事件时间，不携带资料或 Schema 元数据。
- 未发布 0002 沿当前文件名改单列，0001/固定源/已发布历史保持。完整旧四列/五列、任一元数据或约束残留、混合结构及 v1 残留无写拒绝，dirty 优先，基础账号结构及五个原时间列精度检查保持。

### Final Checks

| 检查 | 本轮实际结果 |
|---|---|
| `GOWORK=off go build ./...`、`go vet ./...` | 通过 |
| 全量普通测试及 Linux race | 各 44 个有测试包、870 个顶层测试、2637 个含子测试条目通过；0 失败、0 race warning |
| 普通及 race 必需门禁 | 各 769/769 通过；原 768 个入口保留，新增 SchemaEvolution，无重复 |
| 定向真实数据库与 JSON/schema 单元测试 | 通过；恢复 JSON 精确大整数及账号 update_time 扰动检测通过 |
| sqlc 1.31.1 生成一致性、历史迁移检查 | 通过；SQL 生成模型匹配单列，无手改生成结果 |
| `bash scripts/verify.sh` | 通过，含 PG 17.11 dump/restore，seed/verify/source-unchanged 三阶段全部通过 |
| OpenSpec strict | 25 项通过、0 失败 |
| 格式与差异 | 23 个改动 Go 文件 gofmt 检查通过，差异空白检查通过 |
| 独立审查 | 无 P0/P1/P2 实现问题；唯一 P3 测试覆盖退化已修复并限定复审 ADDRESSED，无新增发现 |

普通/race 套件唯一测试级 skip 为预期的 TestRecoveryFixture，恢复脚本分别强制其三阶段运行。恢复演练检查单列 JSON、精确 `9007199254740993`、账号 update_time、恢复后匿名化及源库不变；材料保留在 `.test-output/recovery.aB2xXw`。

验证期间生产源码未变。28 个 Go/SQL/门禁及恢复脚本源哈希中，只有审查后补强的生命周期测试发生变化：将 mismatch 分支改为真正不接受旧资料的 Schema，先断言空补丁失败，再验证 purge 成功。修复仅涉及测试文件，另行执行该顶层四分支的普通测试及 race，均通过；独立限定复审确认关闭 P3，没有重新运行整套验证。

原始证据：

- [全量状态](../../../.test-output/profile-single-column/status.log)、[原始 JSON 汇总](../../../.test-output/profile-single-column/evidence-summary.json)、[完整验证与恢复输出](../../../.test-output/profile-single-column/full-verify.log)。
- [RED](../../../.test-output/profile-single-column/red.log)、[实现报告](../../../.test-output/profile-single-column/implementation-report.md)、[恢复夹具报告](../../../.test-output/profile-single-column/recovery-report.md)。
- [最终审查](../../../.test-output/profile-single-column/final-review.md)、[唯一修复及普通/race 证据](../../../.test-output/profile-single-column/fix-report.md)、[限定复审](../../../.test-output/profile-single-column/re-review.md)、[源哈希核对](../../../.test-output/profile-single-column/source-after.json)。
- [资源清理](../../../.test-output/profile-single-column/cleanup.log)：先核对标签，再删除本任务三个容器、网络及两个缓存卷；宿主日志、合成备份与状态保留。

### Decisions and Limits

实施裁定：随删除的存储元数据一并移除配置 SchemaVersion、View 元数据和审计元数据，View 包装类型保留以减少方法签名变化；若宿主以后需要诊断标签，可另行增加，不隐含历史绑定。修订未发布 0002 而不自动删除旧临时 v2 的列；若已有未报告的开发部署需要保留数据，仍需单独评审迁移。

未验证在线 CI、生产部署、真实宿主资料、旧应用与新单列结构混跑/回退或宿主不兼容 Schema 数据迁移。本轮未提交、推送或归档。下文为历史验收原文。

# Verification

## 文件命名修订验收（2026-10-21）

- 统一资料属性实现、测试、示例、内部包及未发布 0002 的文件命名；通用 JSON 工具包为 `internal/boundedjson`。SQL 字节、迁移 version 2、测试名与公开行为保持。
- 名称映射及逐项内容核对：`.test-output/profile-filenames/rename-map.json`；实施说明：`.test-output/profile-filenames/implementation-report.md`。
- 实际检查：`GOWORK=off go build ./...`、`go vet ./...` 通过；`go test -count=1 ./internal/boundedjson ./internal/profileattributes ./internal/migrationcheck` 通过；迁移无 I/O 测试通过；`scripts/check-generated.sh` 与迁移历史检查通过，生成代码未变化。
- OpenSpec 最终严格校验：25 项通过、0 失败；差异空白检查通过。
- 独立审查 PASS：28 项逐一与 HEAD 原文件对照，仅指定命名替换；两份 0002 SQL 字节完全相同；必需清单仍为 768 条，只更新 20 条包路径，测试名与顺序保持。实现、测试、示例、内部包以及当前文档引用已同步。
- 本次为纯命名维护，没有重新运行真实数据库、race 或恢复演练；前次完整行为验证仅作为其对应提交的历史记录，不宣称本次又执行了完整验收。没有新增提交、推送或归档。
- 当前日志见 [检查状态](../../../.test-output/profile-filenames/status.log)、[单元测试](../../../.test-output/profile-filenames/unit.log)、[独立审查](../../../.test-output/profile-filenames/review.md)。本节以下保留此前验收原文，其中原文件名仅用于历史追溯。


> **证据界限：本轮结果见紧接的“本轮修订验收”。从“Scope”起的原有验收和日志属于 54afa4b 的旧五列/revision 实现，保持原文，仅证明旧实现。**

## 本轮修订验收

- 已确认：仅用户资料属性；数据库账号行锁内最新读取、Merge Patch、完整 Schema 校验和提交，无 revision/expected_revision/Conflict 或 Redis 锁。
- 保留：Schema 版本、指纹及独立更新时间；最终四列；0001、冻结源及历史发布不变；未发布 0002 文件名保持。
- 范围：本轮相对 `54afa4b` 的未提交修订；公开接口为 ProfileAttributesConfig/View/Patch、Config/Deps.ProfileAttributes、GetProfileAttributes/PatchProfileAttributes。没有增加 HTTP、JWT 或内省资料字段。
- TDD：原接口下两个相同 revision、不同属性补丁的测试先因版本冲突失败；改为新接口后两次成功并保留两项。新增确定锁序测试使用数据库 gate 和 blocking PID 链，覆盖同字段、嵌套、数组、父删除及完整结果失败；冻结、注销、匿名化先执行后写入拒绝与原子回滚均有真实数据库覆盖。
- 迁移：最终四列及三个基础 CHECK；旧五列 v2、新旧混合残留、v1 残留无写拒绝且 dirty 优先；原结构、时间精度及发布历史保护保留。测试清单 26 项逐项改名，新增两个锁行为入口，无删除必需保护。

| 当前检查 | 实际结果 |
|---|---|
| `GOWORK=off go build ./...`、`go vet ./...` | 通过 |
| 全量普通测试、Linux race | 各 44 个有测试包、869 个顶层测试、2644 个含子测试条目通过；0 失败、0 race warning |
| 普通与 race 必需门禁 | 各 768/768 通过，清单无重复 |
| 真实 PostgreSQL 相关测试 | user、migrations、根包 ProfileAttributes 测试通过，包含临时旧 v2 拒绝、精度和确定锁序 |
| sqlc 1.31.1 生成一致性、迁移历史 | 通过；0001 和固定源未改 |
| `bash scripts/verify.sh` | 通过，含真实 PG 17.11 dump/restore 和 seed/verify/source-unchanged 三阶段 |
| `openspec validate --all --strict` | 25 项通过，0 失败 |
| 独立文档及最终代码审查 | PASS，无 P0–P3 问题；恢复成功提示随后一行术语修正独立复核 |
| 验证前后源文件哈希 | 32 个已改 Go/SQL/必需清单文件完全相同 |

普通与 race 套件唯一测试级 skip 是预期的 `TestRecoveryFixture`，恢复脚本单独强制执行三阶段全部通过。恢复检查完整四列资料、精确数值、Schema 标识与时间、恢复后匿名化及源库不变。全量验证之后仅将恢复成功提示由旧 `identity/revision/time` 改为 `identity/time` 并明确 profile attributes，执行 `bash -n` 和差异检查；没有改变恢复逻辑，也未重复使用非空恢复库。

原始输出目录为 `.test-output/profile-attributes-row-lock/`：

- [当前状态](../../../.test-output/profile-attributes-row-lock/status.log)、[原始 JSON 核对摘要](../../../.test-output/profile-attributes-row-lock/evidence-summary.json)、[完整验证输出](../../../.test-output/profile-attributes-row-lock/full-verify.log)。
- [RED](../../../.test-output/profile-attributes-row-lock/red.log)、[相关 GREEN](../../../.test-output/profile-attributes-row-lock/related-green.log)、[实现与测试映射](../../../.test-output/profile-attributes-row-lock/implementation-report.md)、[最终审查](../../../.test-output/profile-attributes-row-lock/final-review.md)。
- [验证后源哈希](../../../.test-output/profile-attributes-row-lock/verified-source-after.json)；恢复材料位于 `.test-output/recovery.x2rlsT`，保留 dump、合成 state 与三个阶段 JSON。
- [任务资源清理](../../../.test-output/profile-attributes-row-lock/cleanup.log)：逐项核验任务标签后删除三个容器、专用网络及两个缓存卷，宿主日志和备份保留。

实施裁定：只移除用户已决定的 revision，Schema 版本、指纹和独立时间继续保留；若需进一步简化，应另行调整这些字段的契约。本 change 的 0002 尚未发布，沿原文件名修订并拒绝旧临时五列 v2；如果已有未报告的开发部署需要保留数据，须另行评审迁移，不能自动转换或清库。

未验证：在线 CI、生产部署、真实宿主资料、旧应用与新结构混跑/回退、宿主业务 Schema 数据转换。本轮不新增提交、推送或归档。以下章节为历史记录。

## Scope

- 日期：2026-10-21。
- 本次为创建规划工件：proposal、design、四份增量 specs 和 tasks；随后按继续指令完成详细实施计划。
- 分支：`feat/schema-validated-user-extensions`，从本地 develop 创建。
- 用户确认计划后完成实现及综合验收，23/23 项任务完成；逐步审查、最终整分支审查和最终修复复审均已通过。

## Planning Checks

使用已安装 OpenSpec CLI 1.14.3 执行。全局 CLI 位于沙箱限制目录，通过环境批准执行；没有安装或升级工具。

| 检查 | 实际结果 |
|---|---|
| `openspec validate add-schema-validated-user-extensions --strict` | 通过 |
| `openspec validate --all --strict` | 25 项通过，0 失败 |
| `openspec status --change add-schema-validated-user-extensions --json` | `isPlanningComplete=true`，proposal/specs/design/tasks 均 done；仅表示规划工件完整 |
| 文档检查 | 8 个 Markdown 文件、4 份 capability delta；本地链接存在，无尾随空白和占位符 |
| 既有场景保留检查 | 所有 MODIFIED requirement 均能匹配主规格，原场景名称完整保留 |
| 首次创建时任务状态检查 | 23 项任务全部未勾选；后续完成计划后为 1/23 |
| `git diff --check` | 通过；另以文件内容检查覆盖本次未跟踪工件 |
| `git diff --exit-code -- migrations/0001_init.up.sql migrations/0001_init.down.sql tests/testdata/source-baseline` | 通过，无改动 |

原始输出保存在被忽略的 `.test-output/add-schema-validated-user-extensions/`：

- [变更严格校验](../../../.test-output/add-schema-validated-user-extensions/validate-change.txt)
- [全量严格校验](../../../.test-output/add-schema-validated-user-extensions/validate-all.txt)
- [规划状态](../../../.test-output/add-schema-validated-user-extensions/status.json)

## Implementation Planning

- [详细实施计划](../../../docs/superpowers/plans/2026-10-21-schema-validated-user-extensions.md) 将剩余工作安排为六个依赖清楚的实施单元，包含文件职责、接口、行为测试、检查命令及逐步审查。
- 已自查四份规格覆盖、23 项任务映射、包间接口及共享写入文件；默认顺序子代理实施，避免查询/生成文件和文档冲突。
- 发现并澄清现有 `SELECT *` 行投影：新增扩展列可能进入 sqlc 行，但不新增认证查询或校验、不进入公开 DTO。说明已写入 design，不承诺列级排除。
- 规划交付时目录是普通 checkout 的任务分支；用户随后确认在当前分支以子代理实施，未创建额外 worktree。
- 计划写入后重新执行 `openspec validate --all --strict`：25 项通过、0 失败；apply 进度为 1/23，剩余 22 项。
- 计划文件六个任务单元、本地引用、占位符及空白检查通过，`git diff --check` 通过。
- 详细计划经用户确认后按既定子代理方式执行；规划完成与代码实施/验收分别记录。

## Implementation Progress

### Task 1 — 精确 JSON 与候选校验器

- 固定 `github.com/santhosh-tekuri/jsonschema/v6 v6.0.3`；验证了精确数值、format 断言、离线 loader、Go regexp、默认注解不写入和并发复用。
- `internal/extensionjson` 提供严格 Unicode/重复键/尾随内容拒绝、精确数值规范化、深拷贝和容量/深度/节点限制。
- 独立审查发现聚合数字展开重复使用总预算，已通过 RED 分配回归修复为解析时共享剩余预算；同一回归从 8461315 bytes/op 降至约 33975 bytes/op，修复后独立复审通过，无遗留阻塞项。
- 最终 scoped 单元测试、vet、Linux race 通过；首轮及修复后 10 秒 fuzz 分别执行 749063、741329 次，无失败。修复后未重复全仓测试，最终综合验收另行执行。
- 日志：[修复 RED](../../../.test-output/add-schema-validated-user-extensions/task1-fix1-red.log)、[修复 GREEN](../../../.test-output/add-schema-validated-user-extensions/task1-fix1-green.log)、[race](../../../.test-output/add-schema-validated-user-extensions/task1-fix1-linux-race.log)、[fuzz](../../../.test-output/add-schema-validated-user-extensions/task1-fix1-fuzz.log)。

### Baseline and Environment

- 本地 `GOWORK=off go test -count=1 ./...` 基线通过；数据库未配置的用例跳过，不记为集成通过。默认 GOCACHE 受限，改用任务目录缓存。
- 专用 Linux runner、PostgreSQL 17、Redis 及不同的恢复源/目标空库已建立，资源清单位于被忽略的 `.test-output/add-schema-validated-user-extensions/resources.json`；sqlc 1.31.1 可用。
- 真实数据库基线 `go test -count=1 ./migrations ./user` 通过（46.246s / 103.481s），日志 [baseline-db.txt](../../../.test-output/add-schema-validated-user-extensions/baseline-db.txt)。

### Task 2 — Schema 与 Merge Patch

- 实现固定属性目录、条件/组合约束、无环本地引用及预算，完整结果校验、精确数值相等与最多 16 条安全错误；补丁包括未知 null 操作及数组对象字段检查。
- 两项设计解释已同步 design：纯约束/布尔 Schema 可在验证片段复用；属性声明按主树展开位置计费，重复安全扫描不重复收费。这些解释若需收紧，会影响支持范围和接入文档，未改变存储格式。
- 独立审查确认并修复两项 P2：未使用定义中的 additionalProperties 子 Schema 可绕过支持范围，以及未使用定义计费依赖 map 顺序。修复后保留 probe 隐藏写法拒绝，连续 Compile 100 次全部成功；确定性顺序测试与原 128/1024 边界保持通过。
- 修复后独立复审通过，无遗留阻塞项。最终 scoped unit/vet/Linux race 通过；[probe](../../../.test-output/add-schema-validated-user-extensions/task2-review1-probe.log)、[unit](../../../.test-output/add-schema-validated-user-extensions/task2-review1-final-unit.log)、[race](../../../.test-output/add-schema-validated-user-extensions/task2-review1-final-race.log)。

### Task 3 — 生产迁移及查询

- 新增生产 `0002_add_user_extensions`，五列及四项基础约束；`0001` up/down 哈希分别保持 `e1c5f1232136ed655e4963411f972e64493290998c5de27f88e511b94dde710e` / `2f26c7dc5f5c5cfe2ee5a9967884fc70c7f213418a602fb2535621904f251f9b`。
- 预检保持原流程、审计及会话检查，新增账号基础列/主键/ID 格式检查，再区分 v1 残留与合法 v2 列、默认值及 validated 约束。测试后续 probe 仍使用 latest+1。
- 非空精确 v0.4.0 升级、旧数据与时间、实例隔离、历史 v1/v2 无写拒绝、dirty/取消及 CAS 已在真实 PostgreSQL 验证；新顶层测试四项已加入必需清单。
- v0.4.0 精确 tag pin 及其移除/移动/不可达/哈希错误回归已补齐；旧固定源与标签不变。
- 独立审查 PASS/PASS，无问题。最终全部 migrations/history 单测、全部 TestRepo、build、相关 vet、定向 race、生成一致性和历史检查通过。
- 日志：[迁移与历史](../../../.test-output/add-schema-validated-user-extensions/task3-all-migrations-history-final.log)、[仓储](../../../.test-output/add-schema-validated-user-extensions/task3-repo-final.log)、[race](../../../.test-output/add-schema-validated-user-extensions/task3-race.log)、[最终隔离](../../../.test-output/add-schema-validated-user-extensions/task3-isolation-final.log)、[生成检查](../../../.test-output/add-schema-validated-user-extensions/task3-generated-final.log)、[历史检查](../../../.test-output/add-schema-validated-user-extensions/task3-history-check-final.log)。

### Task 4 — 宿主接口与协议边界

- 配置及公开 Go Get/Patch 已接入；输入和 Config() 输出深拷贝，每个 Service 初始化编译一次。锁后校验状态、revision、元数据和 Schema 身份，无变化不推进时间/版本或审计，实际修改在提交后记录安全服务 actor 事件。
- 真实并发覆盖同 revision、较高宿主隔离级别、实际 Freeze/AdminDeleteUser 双向竞争及取消。提交阶段断连仅断言错误和无成功事件，不承诺确定回滚。
- PostgreSQL JSONB 表示边界已 RED/GREEN 验证：compact 16313 字节读回为 16413 字节；存储解码允许有界表示，规范化资料仍限 16 KiB，原始补丁限制不变。
- 原根包范围测试暴露两处完整 Up 后仍期待 version 1 的断言；确认其非历史 v1-only 夹具后最小调整为 version 2，定向复验通过，原数据与凭据断言保留。
- 真实消费者/管理 HTTP、刷新后实际 JWT、批量公开资料及真实内省处理器均检查扩展隐私，普通 Authenticate/内省分别为零 SQL；tracer 先确认能观察写入及详情查询再清零。
- 独立审查 PASS/PASS，无遗留问题。相关领域/HTTP/internal 范围测试、根修正定向测试、示例构建、vet 和新增用例 Linux race 已通过；最终完整套件仍待 Task 6。
- 日志：[JSONB RED](../../../.test-output/add-schema-validated-user-extensions/task4-red-jsonb.log)、[JSONB GREEN](../../../.test-output/add-schema-validated-user-extensions/task4-green-jsonb.log)、[根修正](../../../.test-output/add-schema-validated-user-extensions/task4-root-recheck.log)、[领域 race](../../../.test-output/add-schema-validated-user-extensions/task4-race-final.log)、[真实协议隐私](../../../.test-output/add-schema-validated-user-extensions/task4-step5-e2e.log)、[协议 race](../../../.test-output/add-schema-validated-user-extensions/task4-step5-race.log)。

### Task 5 — 生命周期与隐私

- PurgeUser 在原事务中清空资料及 Schema 标识，曾使用 revision 递增、上限保持，未使用资料不初始化时间；清理不调用当前 Schema。宿主回调同事务观察已清空，失败恢复完整账号行及元数据。
- Freeze/Unfreeze、冷静期/恢复保留资料，nil/不兼容配置、MaxInt64、曾写后清空、重复维护已覆盖。导入默认值和提交后独立 Patch 保持原 caller transaction、状态及外部动作契约。
- 新增扩展路径的验证/UPDATE/COMMIT 错误与 cause 链不带资料或未知键；purge 不自动附加资料到宿主错误，旧 purge/import cause 契约保持。晚到的安全审计首次入库不重现资料。
- 独立审查 PASS/PASS，无遗留问题。新扩展真实 PostgreSQL/race、旧 purge/import/audit 回归、vet 和生成检查通过。
- 日志：[清理 RED](../../../.test-output/add-schema-validated-user-extensions/task5-red.log)、[生命周期 GREEN](../../../.test-output/add-schema-validated-user-extensions/task5-green-lifecycle.log)、[隐私 GREEN](../../../.test-output/add-schema-validated-user-extensions/task5-green-privacy.log)、[race](../../../.test-output/add-schema-validated-user-extensions/task5-race.log)、[旧导入](../../../.test-output/add-schema-validated-user-extensions/task5-import.log)、[旧审计](../../../.test-output/add-schema-validated-user-extensions/task5-audit.log)。

### Task 6 — 恢复、上限与完整验证

- 恢复状态经公开接口保存两次实际修改后的完整扩展视图，覆盖 `9007199254740993`、Schema 版本/指纹、revision=2 及时间；五列逐项变更可被现有整行快照检测。
- 真实同账号 16 KiB 并发只提交一次，另一请求冲突；不同账号在另一个账号仍持锁时完成。用实际锁等待观测证明调度，不设置脆弱的耗时 SLO。
- 六组合法性能夹具及相邻超限样本均验证成功/拒绝符合边界。三轮 benchmark 中位数：代表性 Apply 22.669 μs、128 属性 439.315 μs、1024 节点 225.395 μs、重复引用 399.406 μs、16 KiB 277.009 μs、深度 8/512 值节点 792.588 μs；仅为本机 Go 1.26.5 Linux/amd64 观测，不代表生产 SLO。完整时间、B/op 和 allocs/op 见 [benchmark 日志](../../../.test-output/add-schema-validated-user-extensions/task6-bench.log)。
- 必需清单保留全部旧条目，Task 3 加 4 项、Task 6 加 43 项，合计 766 项，无重复或删除。恢复 Fixture 不加入普通清单，由恢复脚本每阶段单独强制执行。
- 全量测试额外发现三处当前完整安装仍期待 v1 的断言，已仅改当前版本为 2；旧 v1 拒绝用例保持。恢复清理夹具曾只移动 delete_time，已按实际到期条件同步移动 purge_time，保持 15 天间隔并使用有效合成管理员。旧失败证据均保留。
- 旧恢复库非空时 guard 拒绝复用；后续均新建独立空源/目标库，没有清空旧库或 Force。最终恢复证据见下表。

### 最终审查及精度保护

- 最终整分支审查仅发现一项 P2：时间类型相同但低精度的列可绕过预检并丢失微秒。真实探针确认后，新增原五个账号时间列及扩展时间列的精度 6 检查。
- 33 个真实数据库案例覆盖 v1/v2 的精度 0/3/6；22 个低精度案例在修复前 RED，11 个显式精度 6 案例保持可用。修复后重复 Up 无写拒绝、catalog/数据/version/dirty 保持，通过迁移/历史、vet、race 和历史门禁。
- 单次最终修复波次仅修改预检与对应测试两份 Go 文件；最终 scoped 复审确认 P2 已关闭，无新增问题，无 deferred/parked 待办。
- 日志：[精度 RED](../../../.test-output/add-schema-validated-user-extensions/final-fix-red.log)、[迁移及历史 GREEN](../../../.test-output/add-schema-validated-user-extensions/final-fix-migrations-history.log)、[精度 race](../../../.test-output/add-schema-validated-user-extensions/final-fix-race.log)。

## Final Verification

精度修正后再次冻结代码并执行完整验证，旧 Task 6 通过记录保留为修正前证据；最终结果以 `postreview/` 为准。主代理重新解析原始 JSON，核对所有必需条目及测试级 skip。

| 检查 | 最终实际结果 |
|---|---|
| `GOWORK=off go build ./...`、`go vet ./...` | 通过 |
| `go test -json -count=1 ./...` | 44 个有测试包、2626 项测试通过；0 fail |
| `go test -race -json -count=1 ./...` | 同样 2626 项通过；0 fail、0 race warning |
| 普通及 race 必需测试 gate | 各 766/766 通过 |
| `bash scripts/check-generated.sh` | 通过，生成结果一致 |
| `bash scripts/check-migrations.sh` | 通过，历史不可变与增量检查通过 |
| `bash scripts/verify.sh` | 通过，含 race、必需检查及真实备份恢复 |
| `openspec validate --all --strict` | 25 项通过、0 失败 |
| Go 格式、差异空白、本地工件链接 | 通过 |
| 最终修正文件哈希 | 完整验证前后相同 |

普通/race 各 867 个顶层测试、2626 个含子测试项通过；8 个无测试包的 package skip 不算测试跳过。唯一测试级 skip 为普通调用下预期的 `TestRecoveryFixture`；恢复脚本单独强制其 seed/verify/source-unchanged 三阶段全部通过。

- [最终状态](../../../.test-output/add-schema-validated-user-extensions/postreview/task6-full-status.log)
- [主代理原始 JSON 核对摘要](../../../.test-output/add-schema-validated-user-extensions/postreview/final-evidence-summary.json)
- [普通测试 JSON](../../../.test-output/add-schema-validated-user-extensions/postreview/task6-unit.jsonl)、[race JSON](../../../.test-output/add-schema-validated-user-extensions/postreview/task6-race.jsonl)
- [完整验证及恢复输出](../../../.test-output/add-schema-validated-user-extensions/postreview/task6-verify.log)
- [最终文件哈希](../../../.test-output/add-schema-validated-user-extensions/postreview/reviewed-source-hashes-after.json)
- 恢复材料：`.test-output/recovery.eFs6Mg`，另复制至本任务 `postreview/task6-recovery-evidence/`；含 state、dump、三个阶段 JSON 与必需 gate，目标清理后仍验证源库不变。
- [资源清理](../../../.test-output/add-schema-validated-user-extensions/resource-cleanup.log)：核验任务标签后删除三个任务容器、专用网络和两个缓存卷；宿主日志、备份及工作树保留。

## Review

- 方案包含受支持的 Schema 使用范围，不能把动态字段、外部引用或复杂未知关键字静默当作已支持。
- 补丁属性目录校验与完整结果校验分离，未知字段的 null 删除不能绕过白名单。
- Schema 版本和资料 revision 独立；正常写入不自动升级旧定义，宿主负责一致部署及专门数据迁移。
- 匿名化独立于当前配置，revision 上限不阻断隐私清理；提交结果不确定不被表述为确定回滚。
- 合法新 version 2 与历史撤回 version 2 通过结构区分；全部已发布 SQL 和固定源保持。

## Not Verified

- 旧版应用在 v2 数据库上的混跑和回退兼容性；本次不宣称支持，按迁移说明另行核验。
- 在线 CI、真实宿主业务数据、外部媒体/IdP 生产接入及产品部署，不属于本次本地实现验收证据。
- 新 Schema 版本的业务数据转换仍需单独评审的宿主迁移，不提供自动转换或零损失恢复承诺。
