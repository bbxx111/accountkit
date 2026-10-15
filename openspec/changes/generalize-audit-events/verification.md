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
- [实施计划](../../../docs/superpowers/plans/2026-10-16-generalize-audit-events.md)覆盖全部规格及 26 项任务，默认执行方法为项目规定的子代理实施/审查；[盘点](../../../docs/superpowers/plans/2026-10-16-generalize-audit-events-inventory.md)覆盖 26 种动作、具体代码引用、输入边界及锁序。
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
