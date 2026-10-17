# 提案核验记录

日期：2026-10-15。范围：仅规划工件；未修改 Go、SQL、生成代码或运行配置，未实施、提交、推送或归档。

## 工件与范围

- [提案](proposal.md)、[详细设计](design.md)、[任务](tasks.md) 以及四份规格增量已生成。
- 分支为 docs/generalize-audit-events；工作区新增内容仅位于本 change。
- OpenSpec 状态为全部规划工件 done；这是文件/依赖状态，不代表实现完成。26 项实施任务均为未勾选。

## 实际检查

| 检查 | 结果 | 边界 |
|---|---|---|
| openspec status --change generalize-audit-events --json | 退出码 0；proposal/specs/design/tasks 均 done，isPlanningComplete=true | 仅证明规划工件齐全 |
| openspec validate --all --strict | 退出码 0；19 passed，0 failed | 结构验证不等于代码或运行行为已通过 |
| git diff --check | 退出码 0 | 未跟踪文档另作逐文件检查 |
| 新增 Markdown 空白、末尾换行和相对链接检查 | 无尾随空白、缺失末尾换行或失效本地链接 | 外部链接源于本会话调研，未在本次离线文件检查中逐个重访 |
| 工作区范围与任务检查 | 仅本 change 新增；26 未完成、0 已完成任务 | 其他未归档变更和主规格未改动 |

## 自审结论

- 已将最终选择写入设计：10 列、原生 UUIDv7、VARCHAR(128)、create_time、无公共 schema_version、namespace + id 主体识别、认证专属字段归入 data。
- 明确列出公开 Go 类型、审计资源名与旧游标的破坏性边界；保留管理员授权和非审计 ID/令牌契约。
- 修正现有规格中 e_ 永久不变及最新版本永远为 1 的冲突，保留冻结初始 SQL 和历史基线拒绝要求。
- 已识别 baseline 对 flow_id 的固定依赖、非空历史转换、外部读写错配、迟到审计恢复隐私及通用宿主错误泄露风险，并提供对应任务/验收场景。
- 提案新增的具体工程决策包括历史 staging 预备转换、外部模式显式 source/namespace 和仅写入查询 503；这些属于本次待评审方案，未执行任何数据转换或改变线上行为。

## 未验证项

- 未运行 Go 构建、单元测试、vet、race、sqlc 或数据库测试：本轮无实现改动。
- UUIDv7 具体 Go 依赖、已有合法字段长度、认证事件映射矩阵、锁序证明、JSONB 执行计划及迁移窗口尚待实施阶段核验。
- 未执行真实数据库升级、故障恢复、外部审计服务对接或匿名化并发测试；不得将本记录作为发布验收证据。
- 未归档；后续实施按 [tasks.md](tasks.md) 更新实际证据。

## 2026-10-16 namespace 案例补充

- 在 [设计文档](design.md) 的主体章节补充单 App 自建账号、多产品共享账号中心、直接使用外部 OIDC 主体三个 To C 案例。
- 明确 source 与 namespace 的区别，以及外部登录映射到本地账号后沿用本地主体；示例不引入新的消费者 OIDC 登录能力或角色字段。
- 核对现有 proposal、specs 和 tasks：案例解释现有身份契约，无需调整需求范围或实施任务，26 项任务仍未勾选。
- 实际检查：`openspec validate --all --strict` 退出码 0，19 项通过、0 项失败；`git diff --check` 退出码 0；三个 JSON 示例均成功解析并包含 USER、namespace、id。
- 本轮仅修改设计说明及本核验记录；未修改实现代码，未运行 Go 或数据库测试，未提交或改变原有暂存内容。

## 2026-10-16 提交、合并与 apply 准备

- 按用户指令提交提案：612056b，docs(audit): propose generic audit events。
- 本地 develop 使用 --no-ff 合并提案：8793e8a，docs(audit): merge generic audit proposal；随后创建 feat/generalize-audit-events。未推送远端，未合并实施代码。
- 已按 openspec-apply-change 读取全部 contextFiles；初始进度 0/26，完成实施计划与只读事件盘点后为 2/26（仅 1.1、1.2）。尚未写产品代码或新增行为测试。
- [实施计划](../../../../docs/superpowers/plans/2026-10-16-generalize-audit-events.md)覆盖全部规格及 26 项任务，默认执行方法为项目规定的子代理实施/审查；[盘点](../../../../docs/superpowers/plans/2026-10-16-generalize-audit-events-inventory.md)覆盖 26 种动作、具体代码引用、输入边界及锁序。
- 计划待用户评审；需明确超长已验证主体字段的审计拒绝策略，以及不可变早期 flow 事件的查询关联证据。计划中的锁环是代码推演，未声称已复现或修复。

### 当前实际证据

| 命令/检查 | 结果 | 限制 |
|---|---|---|
| git diff --cached --check（提案提交前） | 退出码 0 | 只检查已暂存提案 |
| openspec validate --all --strict | 退出码 0，19 通过、0 失败 | 规划结构检查 |
| GOWORK=off go test -count=1 ./... | 退出码 0，各包快速测试通过 | 三个数据库连接串均未配置，数据库集成/恢复未验证；不能宣称完整验证 |
| 工具检查 | Go 1.26.5、sqlc 1.31.1、Docker Server 29.8.2 可用 | 未创建数据库或验证容器 |
| git log -3 --oneline | 确认 8793e8a、612056b 及前置提交 | 只完成本地 Git 动作 |

未运行新实现的 build/vet/race/sqlc/迁移与备份恢复；原始盘点工作文件位于被忽略的 .test-output/generalize-audit-events/。

## 2026-10-16 分阶段实施进展

用户确认继续后按已评审计划实施；当前完成通用模型、认证映射和迁移 3A 的独立审查，运行时统一切换及后续锁序/HTTP/完整验收仍未完成。当前分支处于明确的过渡状态，不可发布。

| 阶段 | 提交 | 实际验证/审查 |
|---|---|---|
| 通用模型 | 44a4745、f71b19f | 模型及旧 audit 相关测试/vet 通过；初次全仓快速测试通过；修复累计数字展开预算、精确 JSON 字段绑定和微秒零时间，独立复审通过 |
| 认证映射/config | db83ada、db76d11 | 26 动作映射、UUID/失败观测/历史快照/可选上下文测试、相关 vet 通过；初次全仓快速测试通过；修复 ConfigFromEnv 显式输入和历史 ADMIN_FORBIDDEN 关联，复审通过 |
| 历史迁移 3A | 4a9a228、58e3a91 | 专用真实 PG17 上迁移、准备工具、CLI、模型与映射五包回归通过；构建、vet、历史检查和独立 sqlc 探针通过；SQL 枚举/容量/时间修复独立复审通过 |

真实数据库基线在本任务 PostgreSQL 上运行 `GOWORK=off go test -count=1 ./...` 退出 0，日志 `.test-output/generalize-audit-events/baseline-with-postgres.log`。3A 新 schema 激活后全库 PG+Redis 测试退出 1，所有旧运行时/SQL/夹具过渡失败保留在 `.test-output/generalize-audit-events/task3/full-postgres-failures.txt`，必须由 3B 修复，不据此声称完整通过。

3A 最终真实数据库检查为 `go test -count=1 ./migrations ./internal/auditmigration ./cmd/accountaudit-migrate ./audit/event ./audit/authentication`，退出 0，日志 `task3/green-review-regression.log`。`check-migrations.sh` 通过，日志 `task3/review-history.log`；原报告所列 build/vet 日志缺失后，由主代理在 58e3a91 代码重新执行并保存 `task3/review-build.log`、`task3/review-vet.log`，均记录退出码 0。

执行台账与逐步报告在被忽略的 `.superpowers/sdd/2026-10-16-generalize-audit-events/`。迁移锁原实现提取到内部共享包，公开 Down 仍禁用；新版本开发清理补丁经具体源码边界复核后获自动审查通过。生产生成检查在 3A 范围被阻止，未执行或绕过，留到 3B 查询切换后完成。固定 0001/源快照及 tags 不变。

本阶段仅勾选已验证的准备工具和版本化 baseline 任务，整体进度 4/26。尚未完成根包/领域/Store/Async/管理查询激活、完整生成检查、迟到隐私并发证明、Linux race、pg_dump/pg_restore 和最终发布验收。

## 运行时激活 3B：代码审查通过，流程确认待答复

提交 `4e92a28` 激活通用事件、UUID Store、冻结队列、认证映射、资源读侧和根装配，并适配历史/恢复夹具。此前 3A 的过渡失败已经逐项修复，保留原有非审计、事务回滚和认证断言；不能因此宣称整个变更完成。

- 当前提交的 `GOWORK=off go test -json -count=1 ./...` 使用本任务真实 PG17/Redis，退出 0，35 个包、1694 个测试/子测试通过、0 fail；原始记录为 `.test-output/generalize-audit-events/task3b/full-current.jsonl`、`full-current.exit`，skip 清单为 `full-current-summary.json`。Windows 不运行 Linux 专属服务集成；普通 `TestRecoveryFixture` 仍按原契约 skip。
- 当前代码 build、vet、生成一致性和历史迁移检查均退出 0，对应 `task3b/build-current.log`、`vet-current.log`、`check-generated-current.log`、`check-migrations-current.log`。
- Linux accountsvc 完整真实集成退出 0，耗时 71.540s，记录为 `task3b/linux-accountsvc-final.log`。该运行发生在最后一次 Principal 归因修复之前；不是当前整个分支的 Linux/race 验收。
- 新增 Principal 用例实际 RED：格式合法但不存在的会话被误标为 USER。最小修复把 `VerifiedSubject` 移到成功归属预检之后；定向 GREEN 及修复后上述全库测试通过，分别记录在 `red-unverified-principal.log`、`green-unverified-principal.log`。
- OpenSpec 严格结构检查当前退出 0，19 passed、0 failed；不代表并发、恢复或代码审查完成。

过程限制：本轮部分新增 Store 幂等/脱敏重放、source/namespace/flow、外部装配和迁移预检行为先实现后补测，未满足逐项先失败要求，不能以旧集成失败冒充。按 TDD 技能删除这些新增实现后重写的操作被自动审查拒绝：字符串切片删除不够明确；改为静态补丁后仍被拒绝，理由是会临时移除幂等、查询、装配和迁移保护，缺少明确用户授权。两次删除均未执行，未更换工具绕过。当前保留通过测试的代码，已启动独立审查，并向用户请求是否接受本轮流程偏差；后续新增行为仍要求先测试后实现。

独立审查确认代码范围 Approved，0 Critical、0 Important；1 项 Minor 为直接 NewService 的本地默认策略缺少公开注释，纳入后续接入说明任务。审查实际读取当前全库测试退出码、统计、skip 和日志，并分别核对受控脱敏重放、来源隔离、共享流程边界、单连接预检以及历史数据/令牌兼容。审查没有把已记录的 TDD 顺序偏差视为接受，也没有替代后续验收。

迟到入库锁序、完整生命周期、严格外部读侧/游标校验、接入文档、执行计划、全库 Linux race 和空库恢复演练仍待完成；本轮流程确认等待用户答复。本条记录不改变任务勾选，不归档、推送或合并实施分支。

## 用户最新指令：直接修改原始数据库定义

用户回复“继续，但不需要新增数据库迁移，直接修改原始数据库定义即可。”据此保留已审查的运行时代码，关闭本轮已披露流程偏差的等待确认；不再尝试临时删除安全逻辑补造测试顺序，后续新增行为继续先测试后实现。

数据库方案按明确新指令调整：0001 直接定义通用审计，生产初始化版本为 1；移除本分支新增 0002、staging 准备工具及根预检，更新当前固定初始校验。固定历史源快照、manifest、正式标签及其 SQL 不变；旧同号 v1 审计结构、旧 v2 库均拒绝，不 Force、不自动重放或清库。兼容/恢复测试改为直接装载当前初始结构中的合成历史格式，不声称旧库原地升级。

已同步 proposal、design、tasks、存储/兼容 delta 和实施计划；新增 migration-safety delta 明示本次用户授权的限定初始定义例外，避免与主规格的已发布不可变要求矛盾。原 3.1/3.3 完成标记按新验收条件重新打开；先前 staging/0002 的验证记录仅保留其历史时间语义。当前新方案尚未实施或验证，旧 GREEN 不替代新初始定义验收。

## 原始定义调整 3C 验收

提交 `53aaa53` 按新方案完成初始定义调整：生产仅保留 0001 up/down，空库直接建立十列审计、初始化版本 1；新增 0002、Prepare/CLI 和根 staging 预检均已撤回。完整旧十七列 v1（空/非空）及旧 v2 均拒绝，拒绝前后目录、数据、version/dirty 不变。双锁、dirty 优先、Down 无 I/O、精确确认 UnsafeReset 保持。

真实 PG RED 先观察到旧方案 fresh version=2、单独旧 0001 为 17 列/text，以及旧同号库被接纳/留下 dirty；修改后定向 GREEN，非空重复、并发直装、根单连接、容量/时间域及限定 search_path 写入通过。原始记录位于 `.test-output/generalize-audit-events/task-3c-{red,red-initial,red-root,green,full-test}.log`。全库 Windows PG/Redis 测试退出 0、build/vet 成功及 sqlc generate 无生成差异由原工具终态记录；早期没有现场保存独立 `.exit`，未事后补造。后续 focused、generated、最终 migration history、strict、diff 检查现场记录退出 0；生成检查成功静默，仅有 `task-3c-generated.exit`，没有 `.log` 文件。

当前固定初始 SHA-256：up 为 `ba8c8b363504bd85ea53b538c300e3ed8ae8e160827c0cfa3c95e9e731f02286`，down 为 `270052406120c755f13148ca3f07ebb032d1bee8f5a1c5b1d3ce29f9736cebb7`。固定源快照及精确发布提交不变，历史门禁未放宽。测试专用 legacy_integrated_v1 仅用于拒旧库断言，不嵌入生产迁移；历史/恢复合成数据直接装入当前定义，保留旧非审计密文、摘要与有效凭证，审计明确合成映射，不伪称旧库原地升级。

独立审查 Approved：0 Critical、0 Important、1 Minor。Minor 为历史格式测试使用空管理员揭示身份，使该夹具的正常审计被校验拒绝并产生安全 WARN；已具体归因，交给后续生命周期/观测任务补投递断言和完整合成管理员，不放宽生产主体校验。审查核对原 RED/GREEN、全库日志、最新 focused/history/strict 即时退出码；未重跑套件。早期工具终态无法从本地文件独立重读，保留为原运行记录限制，最终完整 Linux 验收另行提供新证据。

依据模型、映射、运行时及 3C 的累计实际验证和独立审查，任务现为 11/26 完成。UUID 并发生成补充、隐私锁、生命周期观测、严格 HTTP、示例文档、Linux race、required gate、执行计划和恢复仍未完成；此记录不是发布验收。

## 并发隐私 4 及历史关联修复验收

提交 `c51e56c` 建立整批账号/流程/审计顺序、逐事件脱敏、清理前锁定完整集合及 retention 候选 UUID 锁序。首次独立审查发现 1 项 Important：合法通用 USER/FLOW 历史关联可能不在 live owner 中，关联本身迟到也可能扩大已删除账号的清理范围。原实现者以真实 PG RED 复现两条路径及并发顺序，提交 `fa90e62` 修复；独立 scoped 复审确认 I1 ADDRESSED，0 新 Critical/Important/Minor。

最终写入按完整资源关系合并 live、历史和批内账号，排序锁用户、实际流程范围 advisory 键、流程行，再按合并的 UUID 次序处理旧行隐私和新事件。历史集合变化完整回滚后重试；关联迟到会同事务处理旧相关行。受控更新保留原 ID、create_time、稳定引用、事件事实和业务 Data。现存但过期/未知流程、缺失/已删除账号保守脱敏，匿名无相关引用及外部 namespace 不被连带处理；Reader 授权未扩大。

额外真实 RED 证明了未知流程随后首次创建的竞争和 Repeatable Read 旧快照问题。内部 advisory 锁绑定实际 identity_flow OID 与 source/namespace/flow，实际键排序、事务结束释放；Store 与仅 softDelete/purge 的私有事务显式 RC，其他 Repo/宿主事务默认不变，回调继续同 Tx。相同实际表的不同 search_path、取消/连接复用和公开 Repo 仍继承 RR 的回归通过。无 schema、迁移、固定 hash 或公开 API 改动。

修复后日志及现场退出码位于 `.test-output/generalize-audit-events/task4/fix1/`：

| 当前修复检查 | 实际结果 |
|---|---|
| native audit 全包 | exit 0，audit 15.878s |
| native user 清理/Repo/完成流程覆盖 | exit 0，16.930s |
| Linux audit 全包 race | exit 0，16.288s |
| Linux user 同范围 race | exit 0，12.477s；runner exit 0 |
| 六组对应真实 RED 与四组 focused GREEN | RED 均 1、GREEN 均 0，含两个历史序列、双顺序、整体事务重开、混合 UUID、未知流程首建及 RR 边界 |
| vet、sqlc、check-generated | 均现场 exit 0；独立复审读取日志和退出码 |

初次修复前阶段的 Windows 全库、Linux audit/user 和最终 retention 后 audit/maintenance race 均通过，报告明确其时间边界；它们不代替修复后完整发布验收。**此前记录中的 Windows“PG/Redis”只表示使用真实 PG 并配置了 Redis URL；accountsvc 真实进程用例在 Windows skip，user 使用 miniredis，不能据此宣称当前代码真实 Redis 装配已验证。** 3B 曾有真实 Linux accountsvc 结果，但不是当前最终代码；Task 8 必须重新运行完整 Linux 服务/Redis、required gate 和恢复演练。

OpenSpec 5.1、5.2 完成，累计 13/26；5.3 的本地 retention 核心已验，外部模式切换、旧本地副本继续清理与宿主组件不被删除的根装配测试接任务五，在实际通过前保持未勾选。连续四次重试耗尽、大数据量历史查询/清理/advisory 容量仍是未动态验证项，纳入最终验收边界；不以静态循环或排序冒充相应动态证明。

## 宿主装配、生命周期及并发 UUID 5 验收

提交 `2783717` 验证真实注册/认证/Reveal/管理查询的装配矩阵、New 无 I/O、宿主组件与连接所有权、稳定 scope 模式切换后的本地旧副本 retention/匿名化及外部副本不被库删除。队列满、关闭、后端错误/超时均不改变认证动作结果，秘密错误文本未进入日志；非法后端分类按整批 Failed 处理。16 个并发生产者生成 1024 个实际认证事件，UUIDv7 版本/variant/唯一性及重投递 ID 保持通过；2001/2099 occur_time 与 UUID 生成时间独立，不宣称严格全局顺序。

历史夹具两项缺口均先真实 RED 后 GREEN：空管理员导致合法 Reveal 事件丢失，改为完整合成 Issuer/Subject 并断言实际投递；合成旧记录漏 actor_kind 导致 Projection 不可读，仅在合成 seed 添加与既有 USER actor 一致的受控值，断言旧 Type/Actor/UserID/Result。未改固定源输入、生产主体校验或凭证构造。Task 3C 的该 Minor 已关闭。

Windows 全库 JSON 记录 `task5/windows-all.json.log`、`.exit=0`：636 顶层 PASS、0 FAIL、33 个有测试的包 PASS；实际 47 个测试级 skip 和 6 个无测试包已列 `windows-skips.txt`，真实 Redis 专项、Linux 服务进程及普通恢复 fixture 未执行。聚焦回归、vet、新并发 scope Linux race 均有现场退出 0。上述全库发生于后续监听器修复之前，不能代替最终验收。

补跑 accountsvc 的原 `TestRuntimeStartupOrderAndFailureCleanup/#00` 失败保留为 `task5/maintenance-accountsvc.exit=1`，20 次成功重跑没有被用作修复证明。受控 listener/logger 屏障随后取得真实 RED：net/http 已取消监听器登记但 Close 尚未完成时，库已关闭或 runConfig 已返回。提交 `39c9f44` 仅在既有有界 closeLibrary 回调内先关闭服务自建 listener 再调用 app.close，保留 nil、排空顺序、总预算和异常退出语义。新 RED/GREEN、Runtime/Shutdown 覆盖、vet、Linux 同范围 race 均现场退出 0（`listener-*.log/.exit`）；无新公共接口或生产测试 hook。

独立整体审查 Approved，0 Critical/Important、2 Minor：listener 测试末尾 channel 接收可加 deadline；统一测试正则产生已披露的无命中 warning，不视为验证通过。前者交最终测试收尾，后者在后续命令按实际包范围运行。原 3B 的 raw env/defaults/reader-only 跨任务证据由其已完成的独立审查保留，本轮矩阵回归也通过。

现累计 17/26 完成。严格 HTTP、接入示例/兼容文档、最终 required gate、Linux 真实服务/Redis、执行计划/锁容量和独立空库恢复仍未完成。原始日志统一在 `.test-output/generalize-audit-events/task5/`；无推送、实施合并或归档。

## 管理查询及严格外部边界 6 验收

提交 `200f885` 完成整页（含 lookahead）严格 ASC occur_time/UUID、唯一 UUID、After 范围、RecordView.CreateTime、source/完整资源 namespace 和认证投影校验。Live 要求整个 Evidence 零值并独立核对领域归属；历史证据需完整合法存储记录、认可成功动作及 USER/FLOW 引用。合法资源 Name 不影响身份，背景 session 和删除后历史引用可读；业务 Data 不输出。审计游标独立，旧 e_ 和跨 scope 拒绝，用户列表游标未改；Result nil 输出 null，已知结果字符串保持。非法领域 ID 在任何 I/O 前拒绝；依赖故障仅固定消息与受控原因日志。

真实行为 RED/GREEN 和现场退出码位于 `.test-output/generalize-audit-events/task6/`：`red-behavior.exit=1`、`green-focused.exit=0`。原来正确的旧游标/授权优先级等明确记为已有 GREEN 回归，未删保护造 RED。最终 `full-windows.exit=0`，651 顶层 PASS、33 测试包 PASS、0 FAIL；真实 PG 及 Redis challenge/key-rotation 专项通过。17 测试级 skip：普通 RecoveryFixture 预期 skip，另 16 为 Windows 不支持的 Linux 服务 TLS/SIGTERM 项。相关 vet、严格 OpenSpec、差异/格式检查退出 0。默认 sandbox cache/CLI PATH 失败属于环境失败，不算行为 RED 或通过。

独立审查 Approved：0 Critical/Important、1 Minor（测试默认 logger 的预期 WARN 噪声，交最终测试卫生收尾）。Root 核对提交差异统计、实际 full-windows/vet 退出码和成功日志。Linux race、真实服务进程/TLS/SIGTERM、恢复三阶段及最终 sqlc/历史/required gate 明确留最终步骤，未据 Windows 推断通过。累计 20/26，未推送、实施合并或归档。

## 实施裁决及代价（顺序记录）

以下对应实施 ledger 中全部 Ruling；包含被用户后续指令替代的历史阶段，不代表仍保留 staging/0002。最终收尾如新增裁决继续追加。

1. 先实现叶子模型/认证映射，再统一切换存储与领域。 保持阶段可编译；代价为少量别名及集中整合工作。

2. 通用 Scrub 仅清理 name 和客户端上下文，认证 subject_hint 由认证层组合。 避免认证侵入通用模型；代价为存储必须选择正确清理投影。

3. 历史管理员缺 issuer/sub 时以 UNKNOWN 表达，并保留受控历史快照。 避免伪造已知身份；代价为历史投影多一条受控分支。

4. FlowLink 增加 Live，由领域回调独立核对；历史流程依赖真实 Evidence。 补齐接口表达；代价为额外归属查询，直接适配器须提供回调。

5. 新审计环境配置保留原始空值，New 才补默认，Validate 不回写。 保留显式配置来源；代价为 ConfigFromEnv 新字段可为空。

6. 原 Task3 拆为结构准备和运行时激活，两次独立审查。 控制任务体积；代价为历史中间阶段不可发布，此阶段已被当前初始定义方案替代。

7. Store 增加 NewStoreWithOptions，并保留本地默认构造。 支持 source/namespace 隔离；代价为多一个构造入口和配置责任。

8. 曾提取共用迁移锁供 staging Prepare 复用。 避免复制锁算法；代价为关键锁路径重定位及并发复验。Prepare/staging 后按用户指令移除，锁复用边界仍核验。

9. flow 查询只补无明确账号归属的早期事件，B-only 不借共享 flow 返回 A。 保证账号隔离；代价为每个账号分别查询其专属事件。

10. retention 按 source 和 occur_time，不附加消费者 namespace。 匿名/管理员事件也应到期；代价为同 source 共享保留策略，独立实例须用不同 source。

11. ReplaceIdentity 只在 preflight 证明真实资源归属后记录 VerifiedSubject。 格式合法的 Principal 不是认证证据；代价为部分早期拒绝保守记录 UNKNOWN。

12. 按最新用户指令直接修改当前 0001/hash，撤回本分支 0002/staging，历史快照和正式标签不变。 用户明确覆盖原冻结约束；代价为没有旧库自动升级，需新资源或独立人工数据规划。

13. 本地完整 USER 引用但账号缺失时保守脱敏。 缺失行不能加锁；代价为尚未入库的合法账号引用可能丢失可选名称/客户端信息。

14. 受控 subject_hint 仅对认可认证 action 的 accountkit 对象清理，业务 Data 保留。 统一 Go/SQL 通用契约；代价为自定义 action 的业务隐私由宿主管理。

15. 隐私清理和迟到写入均使用同源完整 USER/FLOW 历史闭包，迟到关联扩大范围时同事务清理旧行。 仅 live owner 不足以覆盖通用引用；代价为历史查询和额外行锁，须实际性能验收。

16. Store 写事务显式 READ COMMITTED。 锁后必须看见新提交关联；代价为不继承宿主更强默认隔离级别。

17. 仅库内 softDelete/purge 清理事务也显式 READ COMMITTED，公开 WithTxRaw 和其他事务不变。 真实 RR 回归证明 Store-only 不足；代价为这两条清理回调同 Tx 的隔离级别由库决定。

18. Store 在用户锁后、流程行前获取按实际键排序的事务 advisory 锁，绑定实际表 OID/source/namespace/flow。 不存在流程后续首次创建也需串行；代价为额外查询、锁容量和 hash 碰撞导致的过度串行。

19. Task5 纳入实际发现的 accountsvc 自建 listener 关闭竞争，做最小修复。 已有生命周期契约和真实失败要求处理；代价为触及可选服务，需要最终 Linux SIGTERM/排空验证。

## 接入文档与示例 7 验收

提交 `a27aa8a` 提供 `examples/audit` 的 local/host/write-only 三模式，复用稳定 scope，宿主控制自建 Async；完善 README、兼容/迁移/accountsvc 文档、AGENTS 和公开 NewService 注释。外部审计须持久化清理、重试和完成核验，USER_PURGED 不是完成证据，旧本地副本责任继续。公开类型、UUIDv7、nullable result、旧游标和范围证据、旧库拒绝、固定历史边界均与当前实现定向核对一致。Task3B 注释 Minor 已关闭。

两个示例独立 GOWORK=off build、相关 vet、真实 PG Windows 普通套件工具终态成功，日志在 `task7/`；没有独立 .exit sidecar，未补造。Root 读取实际 commit/stat、普通套件成功日志；实际服务启动、Linux/race/Redis/恢复不由该轮证明。构建/vet 空日志无法单独重建终态，最终 Linux 全库构建会提供现场新证据。独立审查 Approved，0 Critical/Important、1 Minor：示例 Shutdown 超时后的依赖关闭边界，交最终收尾处理；其余 CannotVerify 与最终验证逐项对应。累计 22/26，最终四项尚未完成。

## 性能验收补充裁决

20. 最终验收允许按实际性能证据最小改写历史闭包/清理 SQL，并重新生成 sqlc；不提高 timeout 或强制索引来宣称解决。10 万条数据的清理锁查询超过 30 秒，单改索引未必解决嵌套重复扫描。代价为最终阶段涉及查询实现，必须保留完整 source/namespace/逐事件关联集合和锁顺序，并补真实回归、并发及最终 Linux 验证。

## 最终运行验收 8（独立全分支审查进行中）

实现提交 `a614f94`，性能方案说明提交 `f6a1b3f`。新增 77 个实际必需顶层测试，旧门禁未删，共 574 条均在最终 JSONL 有通过记录。测试补齐连续四次 live/history 归属扩张的实际不同事务、整批回滚/无新增行/锁释放及后续成功；原有正确预算测试首次 GREEN，单列临时副本变异 RED 仅证明敏感性，不冒称原实现 TDD 缺失。三项 deferred Minor 已处理：listener 观察有界等待、测试默认 logger 丢弃预期日志、示例就近明确 Shutdown 超时不保证排空的限制。

真实 20k 数据旧 lock/scrub 查询各在5秒取消，构成性能 RED；改写后同回归得到精确六条 actor/target/related 用户及早期 flow 闭包，分别16.8/17.7ms。sqlc generate 和全部 audit/user race 通过。MATERIALIZED 单次历史集合及完整 JSONB 候选保留原清理范围/锁序，include_user=false 只 live；无新索引/0001/hash/迁移改动，Reader授权及retention保持。

100k 合成数据（80k同source、10k用户、20kflow、约110.7MB含索引）ANALYZE后，数据库 EXPLAIN ANALYZE BUFFERS 实测：

| 路径 | 改写前 | 最终数据库 Execution Time |
|---|---:|---:|
| 历史 USER/FLOW | 218.791ms | 5.437ms |
| 清理候选 UUID | 148.263ms | 0.895ms |
| 清理前整体锁定 | 超30s取消 | 26.108ms |
| live flow scrub | 552.667ms | 12.341ms |
| 用户/历史 scrub | 超30s取消 | 5.572ms |
| retention | 3.801ms | 4.206ms |
| Reader 时间线（未改） | 91.934ms | 168.650ms |

自然选用现有actor表达式/targets及related GIN、flow索引及UUID主键；未强制索引或提高超时。单次合成测量与部分回归并行，缓存/JIT/调度会影响结果，Reader仍扫描较多同源候选，以上不是SLA或吞吐保证。完整原计划、后计划/行数/buffers/参数及装载脚本在 `task8/measure.go`、`<QueryName>.log`、`after-<QueryName>.log`。

实际事务 advisory 锁样本1/32/100/1000/6400/8192个均获授予，回滚后零锁；8192逐键串行调用约697.616ms。6400为默认100条批次×每条最多64资源的保守样本，不代表全局容量或无碰撞证明，锁池与其他事务共享。混合旧/新UUID的真实Store测量在100k历史上处理2新+5旧、22查询调用约48.979ms，七条隐私全部清理；单条真实受控UPDATE走主键5.522ms。

最终 `scripts/verify.sh` 在Go1.26.5/Linux、gcc14.2、PG客户端17.11、CGO=1、GOWORK=off、ACCOUNTSVC_TEST_RACE=1运行，真实本任务PG/Redis：`task8/full-verify.exit=0`。历史检查/build/vet/全库race/574必需门禁/pg_dump及pg_restore均成功；663顶层PASS、33测试包PASS、0FAIL，普通套件仅按规定跳过RecoveryFixture。真实SIGTERM、TLS及HTTP服务用例实际通过，子服务构建也启用race。单独生成检查和严格OpenSpec均现场退出0。Root直接读取退出文件/日志并独立解析 `.test-output/tests.jsonl`，结果一致。

恢复前实际集群+库身份不同且均user_objects=0；`.test-output/recovery.Bz7M2l/` 保存dump/state及seed、verify、source-unchanged三阶段JSONL。Root独立核对三阶段实际Test及包均PASS、0FAIL、0SKIP。覆盖当前初始version1、认证记录/identity-flow快照、解密、有效access/refresh、源库保持；完整套件历史格式直接载入/明确旧审计映射亦通过，不表示旧库原地升级。恢复库现非空，重做必须新独立空资源。

早期本机Go cache失败、首个性能夹具非法SYSTEM主体、OpenSpec初次PATH启动失败均明确不算产品RED或成功检查；实际重试结果有现场.exit，无旧结果补造。当前独立全分支审查进行中，最终四任务未勾选，尚未宣称最终批准。原始日志保留在忽略的 `.test-output/generalize-audit-events/task8/`，无推送/合并/归档。

## 最终审查及补充裁决

独立全分支审查0 Critical、2 Important、0新增Minor：合法业务共表会阻断认证管理时间线；Async冻结期间关闭后仍可能入无人消费队列漏计。原运行证据可信但不能替代修复验证，最终四任务继续未勾选。7个无测试文件的包级skip与唯一普通RecoveryFixture测试级skip分别统计。全部原延后项已关闭。

21. 管理查询增加可选通用动作筛选，规范化去重、SQL分页前生效，认证集合从既有枚举唯一导出，审计v2游标绑定动作范围并拒绝v1；无筛选的通用Reader仍可读业务。代价为宿主Reader新增过滤契约及本分支未发布UUID游标失效，需要迁移说明及回归。

22. 用短同步串行 Async 最终入队与 Close/ctx取消状态转换，Freeze、I/O、日志在同步区外。真实探针证明排空后仍可能入队漏计。代价为短暂准入锁竞争，需保留非阻塞有界队列和既有关闭预算。

23. 接受审查的非目标1：保持尽力投递和宿主持久清理契约，不新增outbox/跨库原子架构。代价为可靠性及外部清理完成仍由宿主实现。

24. 接受非目标2：不全面重建直接领域Principal可信来源，仅保留已确认的ReplaceIdentity归属修复。代价为其他领域调用继续依赖既有宿主认证边界。

25. 接受非目标3：示例保留就近Shutdown超时限制，不升级成完整生产服务器。代价为生产宿主仍须实现真实排空/终止策略。

26. 接受非目标4：未Start时Close仍同步逐批刷新，Inserter仍需遵守context；只修复准入竞争。代价为未Start关闭可能受批数或不响应context的后端阻塞，不宣称所有状态统一硬截止。

27. 接受非目标5：不额外重构导入/helper或历史“四份/五份”措辞。代价为少量风格和历史口径保留，当前五份规格验收仍明确。

审查CannotVerify逐项处置：修复后的证据由单次fixwave补齐；真实生产分布/SLA、实际外部设施清理、任意旧产品转换及独立动态时钟回拨未验证，分别属于测量边界、宿主实施、明确不自动升级和锁定UUID依赖的静态分支核对，不冒称通过；最终资源清理待复审后执行。

## 最终修复、复审与交付

提交 `427d073` 关闭最终I1/I2。Query.Actions可选规范副本，在SQL LIMIT前筛选，认证集合从现有枚举唯一导出，通用无筛选仍可读业务；外部Reader接收动作条件并受到整页复核，Actions/After副本保护可信范围。审计v2游标绑定规范动作集合，旧v1 UUID与e_均400从头分页，用户列表游标不变。Async最终发送与Close/worker取消使用短准入同步，Freeze/日志/后端I/O在锁外，关闭交错不再留下漏计事件。

真实RED分别复现业务混合/纯业务503、未过滤结果、旧v1被接受，以及未Start/startedClose/workerCancel三模式Freeze后Stats全零且queued=1。修复后真实PG针对性回归与相关全包race均通过；新11项顶层回归全部加入门禁，没有删旧条目。规范化副本、动作范围/归属证据、Reader参数改写、相同时间UUID分页及并发计数均有覆盖。最终原始材料位于 `.test-output/generalize-audit-events/final-fix/`。

最终同字节完整Linux `scripts/verify.sh`、外层Docker、generated、严格OpenSpec19/0及diff检查均现场退出0。Root和独立复审核对退出/日志，Root独立解析674顶层PASS、33测试包PASS、0FAIL，585必需条目无缺项；普通仅RecoveryFixture测试级skip，7无测试文件包级skip另计。Linux真实PG/Redis、TLS/SIGTERM和子服务race通过。新独立空恢复库 `accountkit_finalfix_recovery_source/target` 实际核验后使用，原非空库未清空复用；`.test-output/recovery.3wc7XA/` 三阶段Test及包均PASS、0FAIL/0SKIP。

唯一最终scoped复审对 `a614f94..427d073` 确认I1/I2 ADDRESSED，0新Critical/Important/Minor，无新增范围外观察。与此前全分支审查合并，全部阻断项已关闭；原先阶段GREEN与历史偏差仍如实保留。根再次核对生产仅0001 up/down，当前完整hash与3C记录一致，固定source-baseline差异为空。

26/26任务完成，变更实施完成但未归档；实施分支保留，未推送/合并/tag/发布。仍不宣称生产SLA、跨实例锁池上限或无碰撞保证、任意宿主外部清理已完成、任意旧产品数据转换、动态时钟回拨演练或未Start关闭统一硬截止。上述明确边界不因测试通过消失。

收尾实际清理：核验容器挂载和网络成员后，仅删除本任务PG/Redis两个容器及其匿名数据卷、专用network和两个cache volume，三条清理命令现场退出0（`cleanup-*.log/.exit`）。四个恢复库和规模测试schema随本任务PG容器销毁，合成dump/日志继续保留于被忽略的.test-output；既有验证镜像及其他容器/网络/卷未操作。实施流程报告、审查差异和全部27项裁决保留于 `.test-output/generalize-audit-events/workflow-final/`；永久摘要及代价在本记录中。

## 2026-10-17 索引命名维护

用户补充要求“索引名称的规范也参考 duopandian，并根据规范调整已有的索引，不新增迁移”。据此直接调整当前未发布 0001 的 26 个独立索引，普通和唯一索引均为 `idx_<表名>_<字段或用途>`，5 个业务表主键名称保持。同步 AGENTS、迁移说明、查询及测试引用，由 sqlc 生成 Go 注释；本次不增加迁移，也不自动改名已有同号数据库。up SHA256 更新为 `e91aedbe39fd2aae4ea6fe5e1e2c0314e329dfa3c151dd638fc3fff0a99b4732`，down 和固定历史源不变。

实际验证环境为 Go 1.26.5 / Windows、本任务独立 PostgreSQL 17.5 容器，`GOWORK=off`。原始材料位于仓库根目录 `.test-output/index-naming/`：

- 将改名前后的 SQL 装入独立测试 schema，PostgreSQL 目录双向差集核验 26 个独立索引与 5 个主键索引：唯一性、列序、表达式、谓词及访问方法完全一致，`catalog-compare.exit=0`。独立审查另对 SQL 全文仅消除索引名后比较，全文相同；所有新名称最长 39 字节。
- `sqlc generate` 后再次生成并比较 user/db、audit/db 全部 Go 文件哈希，无变化。`go build ./...`、`go vet ./...` 以及补充测试适配后的 `go vet ./migrations` 均退出 0。
- `go run ./internal/migrationcheck` 退出 0，当前固定哈希、全部可达正式标签及冻结源快照检查通过；生产目录仍只有 0001 up/down，历史夹具没有修改。
- 首轮真实数据库 `go test -count=1 -json ./...` 唯一失败为 `TestInitialSchemaPreservesNonAuditCatalog`：该测试将历史索引旧名与当前新名逐字比较。仅在该测试的临时历史 schema 中执行确定的索引改名，再保留原完整目录比较；未归一化当前 schema、未移除字段/约束/索引检查。单项回归退出 0，独立补充审查确认仍能发现当前索引名错误、缺失/新增或定义变化。
- 最终 `go test -count=1 -json ./...` 退出 0：33 个测试包通过，660 个顶层测试通过，0 失败；15 个顶层测试跳过，含子测试共 47 个跳过。除普通套件按约定跳过的 RecoveryFixture 外，其余跳过来自未配置真实 Redis 的验证码及 accountsvc 服务集成路径，不能据此声称完整发布门禁通过。
- `openspec validate --all --strict` 为 19 通过、0 失败；`git diff --check` 通过。首次 PowerShell 调用将 psql NOTICE / OpenSpec 进度输出误视为错误，重新调用后取得实际成功退出码；不将首次调用计作完整成功证据。
- 两轮独立只读审查均为 0 Critical、0 Important、0 Minor，第二轮专门复核历史目录对照测试适配未放宽门禁。

本次未运行 race、真实 Redis 服务集成、pg_dump/pg_restore 恢复演练或完整 `scripts/verify.sh`；此前完整验收记录不视为本次执行结果。任务 3.6 已按上述实际证据完成，归档、提交和推送未执行。

核验任务标签、精确容器名及 AutoRemove 后，已停止本任务 PostgreSQL 容器并随之删除匿名数据卷，`cleanup.exit=0`；既有 postgres/redis 容器未操作，原始日志继续保留。

## 2026-10-17 唯一索引前缀优化

用户随后明确优化为“普通索引用 idx_，唯一索引用 uk_”，本节取代上一节普通与唯一索引同前缀的最终约定；上一节的执行证据按历史保留。本轮仅将 4 个独立唯一索引从 idx_ 改为 uk_，22 个普通索引及主键名称保持；UNIQUE 约束及其所属索引也采用 uk_ 规范。继续直接修改当前未发布 0001、不新增迁移，当前 up SHA256 为 `11af9574d5f109516b867ccfefeb1e887038c38b81c0956106378ce3f2aaf887`。

查询及 sqlc 生成注释、历史目录对照测试的临时旧库名称转换、规范和相关工件均已同步。历史测试将旧 `_uidx` 改为 `uk_`、旧 `_idx` 改为 `idx_`，仍完整比较当前数据库目录，不改历史源 SQL。新旧初始 SQL 全文仅消除索引名后完全相同，索引定义没有变化。

本轮实际检查（Go 1.26.5 / Windows、GOWORK=off、本任务独立 PostgreSQL 17.5）材料在 `.test-output/index-prefix/`：

- `go test -count=1 -json ./migrations ./internal/migrationcheck`：退出 0，2 个包、42 个顶层测试通过，0 失败、0 跳过，覆盖真实数据库的完整目录对照、唯一性、初始化、重复执行和迁移锁等既有用例。
- `go vet ./migrations ./internal/migrationcheck`、`go run ./internal/migrationcheck` 均退出 0；当前固定校验及历史标签/源快照检查通过。
- `sqlc generate` 后再次生成并比较全部生成 Go 文件哈希，无差异；`openspec validate --all --strict` 为 19 通过、0 失败；`git diff --check` 通过。

本轮未重跑全量 Go 套件、race、真实 Redis 服务集成或恢复演练；上一节结果不视为本轮重跑。未提交、推送或归档。

本轮一次性 PostgreSQL 容器已核验任务标签、名称及 AutoRemove 后清理，`cleanup.exit=0`；既有容器未操作。

本轮独立只读复审通过：0 Critical、0 Important、0 Minor；确认四处改名及固定校验一致，历史测试保留完整比较，规范和工件一致。

## 2026-10-17 归档及合并前核验

用户明确要求“存档，合并”。全部 27 项任务已完成，五份 delta 已同步到主规格：新增 audit-host-integration、generic-audit-events，更新 embedded-auth-package、migration-safety、versioned-auth-storage；逐项核对增量需求全文及旧场景均保留，未涉及的主规格需求保持。随后使用正常 CLI 归档到当前目录，`--skip-specs` 仅因主规格已经完成独立同步，未跳过验证。CLI 归档退出 0；关于超过十项 delta 的提案拆分提示不影响已完成变更的归档。另一个活跃变更 refine-verification-code-delivery 保持原位。

README、兼容指南、实施计划及本记录的相对链接已按归档位置修复，实际检查本变更和引用文档的本地 Markdown 链接全部有效。当前源码在新的独立 PostgreSQL 17.5 / Redis 7 容器上重新运行 `GOWORK=off go test -count=1 -json ./...`，退出 0：33 个测试包、664 个顶层测试通过，0 失败；11 个顶层 skip 为 Windows 不执行的十项真实服务用例及普通套件按规定单独运行的 RecoveryFixture。`go build ./...`、`go vet ./...`、当前迁移历史及固定哈希检查均退出 0，归档后 `openspec validate --all --strict` 为 20 通过、0 失败。

本次合并前检查未重跑 Linux race、真实服务进程测试、pg_dump/pg_restore 或完整发布入口；既有完整验收仍作为历史证据保留，不能代替本轮执行。原始状态、归档输出、测试 JSONL 和独立退出文件保留于 `.test-output/archive-audit/`。合并使用独立 merge commit，不进行 squash、rebase、tag、发布或产品部署。

提交前再次运行 sqlc 并比较生成文件哈希，结果稳定；归档收尾独立只读审查为 0 Critical、0 Important、0 Minor，确认五份主规格及旧场景完整、归档内容保持、链接与锚点正确、其他活跃变更未动。最终严格规格校验仍为 20 通过、0 失败。
