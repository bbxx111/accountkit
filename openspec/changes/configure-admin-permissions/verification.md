# 验证记录

## 2026-11-16：ff 工件准备

范围：仅创建 [proposal.md](proposal.md)、[design.md](design.md)、[tasks.md](tasks.md) 与三份增量规格，未修改实现、部署文件或主规格，未提交、推送或归档。

- [管理员认证规格](specs/accountsvc-admin-authentication/spec.md)：权限目录、角色绑定、scope边界、通配符和错误/审计行为。
- [服务运行规格](specs/accountsvc-runtime/spec.md)：环境变量、残留校验、启动快照、迁移隔离与部署说明。
- [嵌入宿主规格](specs/embedded-auth-package/spec.md)：新权限能力、旧公开接口兼容及拒绝不回退。

### 已执行

- OpenSpec CLI `1.14.3`；`openspec status --change configure-admin-permissions --json`：proposal/specs/design/tasks均为done，规划工件依赖闭包齐备。CLI的isComplete仅表示此阶段工件齐备，不代表实施完成。
- `openspec validate --all --strict`：46项通过、0项失败（45份主规格与本变更）。
- 独立builder-check审查：已阅读提案、设计、三份增量规格及相关装配代码，结论为 `nothing important left`；未发现需用户决定的实质问题。已复核公开RequireRole兼容、新权限路径优先、空策略不回退、无accountsvc内置授权之间的边界。

### 最终文件检查

- 本轮内联Node只读检查通过：7份Markdown、10处本地链接全部存在；8个MODIFIED Requirement均对应现有主规格，原有18个Scenario标题全部保留；19项任务均未勾选。
- 逐文件末尾换行、行尾空白、实际路径与无符号链接检查通过；所有新文件位于本变更目录。`git diff --check`通过；额外逐文件检查覆盖了Git尚未跟踪的新工件。
- `git status --short --branch`确认分支为 `feat/configure-admin-permissions`，仅本变更目录未跟踪；无已跟踪文件差异、无暂存改动。Git读取用户级ignore时有权限警告，但命令成功，文件范围另由直接遍历核对。

### 未执行与后续边界

- 全部19项实施任务保持未勾选；未执行Go构建/vet/单元/数据库/race、SDK或平台构建验证。本轮只有规划文档变化，不将历史CI或规格校验当作实现通过。
- 实施阶段须逐项补充TDD、真实服务、兼容宿主、SDK夹具、共享认证回归及审查证据，按tasks记录缺依赖与未验证项。
- 未修改已发布Go接口、迁移、HTTP协议或运行配置；BREAKING指拟实施的accountsvc显式角色映射迁移，发布和部署仍需后续步骤。

## 2026-11-16：实施与验证

实施分支 `feat/configure-admin-permissions`，起点 `16419c30fc4569df66282a5433a6349b4be6a8c1`。本节为实施验收时的结果；上节仅是ff时的历史状态。验收时尚未提交、推送、归档或发布；后续本地提交见本记录末尾。

### 实现与兼容

- 10项类型化管理权限覆盖11条路由；验证器具备可选权限能力时全部使用新路径，新拒绝和空策略不回退。旧嵌入宿主的RequireRole接口与角色常量保留。
- accountsvc只使用显式角色绑定，移除内部super-admin继承。策略按已验证JWT角色取并集，scope不参与；资源通配符仅按管理目录展开，未来同组操作在新版本启动时加入。
- 新环境变量采用别名列表及NAME/PERMISSIONS；严格检查空项、未知权限/通配符、重复别名/角色和包括空值在内的孤立字段。角色名校验在环境与Go构造边界复用；migrate不解析这些运行变量。
- 更新两种SDK测试宿主的显式策略、部署示例、迁移/回退说明及OpenAPI权限元数据。SDK API、DTO、版本不变，HTTP方法和路由不变，无数据库迁移。
- reveal在路由授权前设置no-store；accountsvc外层认证失败及库Unconfigured的503也覆盖，避免只保护业务handler成功响应。

### TDD与审查

- 核心策略：`core-red.log`记录缺少策略/目录实现时失败，`core-green.log`、`core-vet.log`通过。路由与JWT补充测试最初在实现后加入，没有独立先红证据，不将其报告为完整TDD。随后reveal修复有`core-reveal-red.log`和修复绿测；库Unconfigured修复的红绿证据见`task-1-report.md`追加段。
- 环境配置：`env-red.log`为功能缺失的有效红测，`env-green.log`及`env-final-focused.log`通过；`env-reveal-red.log`复现外层401/503缺no-store，修复后通过。`env-suite-final.log`和`env-vet.log`记录accountsvc与SDK夹具范围测试/vet通过。
- 独立核心审查 `task-1-review.md` 与全分支审查 `full-review.md`：规格及质量通过；发现的reveal授权/未配置响应no-store与README权限表问题已修复并复核。审查不代替执行测试。
- 最终定点回归 `final-focused.log`通过（管理路由、管理员验证器和配置），覆盖后续no-store修复与变量名构造整理。

### 重复字面量与逻辑整理

| 所属模块 | 整理结果 |
| --- | --- |
| httpapi/admin | 权限类型、10个稳定常量和独立目录集中；路由引用具体常量；保留旧公开角色常量仅作兼容 |
| adminauth | 默认角色/用户名claim、角色名256字节限制具名复用；ValidRoleName共用；RequireRole/RequirePermission共用可信上下文、拒绝审计和错误处理，保留原文案；策略只编译一次 |
| accountsvc配置 | 复用envPrefix、唯一CSV校验、别名格式；动态角色变量前缀、字段名和完整名称构造集中，权限去重与别名拒绝重复分别保持 |
| oidcjwt/httpcredentials | ProfileDefault/ProfileRFC9068与SchemeBasic/SchemeBearer在所属模块定义，解析和调用方复用，协议值不变 |
| serviceauth | 三个服务权限常量与ValidPermission精确目录共用，配置和端点复用；不增加服务通配符，不改变scope/主体/错误顺序 |

只用一次的错误文案、JSON标签和路径保留字面量；不同语义的3秒/1分钟等限制不因相同数值合并。测试保留明确协议字符串，避免与生产常量同错同过。

### 已取得的验证证据

- Go 1.26.5 windows/amd64，GOWORK=off：全模块build、vet、`go test -count=1 ./...`通过（普通测试日志`full-windows.log`，未提供数据库连接，不当作数据库集成证据）。共享oidcjwt/httpcredentials/serviceauth/introspection/purgefeed/identityresolve六包回归通过（`shared-regression.log`），最终管理/共享范围回归通过（`shared-final.log`）。
- OpenAPI检查通过：24个消费者、11个管理操作；SDK覆盖36操作通过。覆盖登记未改变操作或模型映射，因此`sdks/http-coverage.json`保持原内容。
- Compose：使用任务目录复制的compose.yaml和合成.env，config解析通过且含下划线的CUSTOM_SUPPORT动态变量完整透传；未读取或覆盖部署目录真实.env，未启动Compose产品实例。
- 独立Go消费：本地临时Git快照 `a15808ec7e9365f298f518a6b60c7f5f749b1e13`，本地代理版本 `v1.1.1-0.20261115164355-a15808ec7e93`。两个独立module在GOWORK=off、无replace下tidy/build/run成功，分别实现旧角色接口与新权限接口。完整记录`go-consumers/verification.json`。该临时快照只用于公开API编译消费，不是正式版本或源分支提交；后续no-store收尾不改变这些公开签名，运行行为由最终代码测试覆盖。
- SDK工具：Node24.21.0、pnpm11.24.0，锁定安装通过；TS typecheck、55项单元测试，以及真实accountsvc的全部11个公开管理操作集成测试通过（`sdk-verification.log`）。测试宿主只使用任务专用数据库/Redis，验证完成已停止。
- Linux真实新增授权用例：`TestServiceIntegrationAdminPermissions`和`TestServiceIntegrationAdminPolicyRestart`通过，覆盖11路由成功/拒绝、11次拒绝审计、运行快照、重启撤权、空策略及凭据隔离。
- 容器本地文件系统上的 `bash scripts/verify.sh` 完整通过：迁移历史、build、vet、全库race、必需测试检查和真实pg_dump/pg_restore恢复演练通过。JSON证据记录56个包pass、1285个顶层测试pass、0失败；恢复seed/verify/source-unchanged三阶段通过，使用同一任务集群内两个不同的一次性空数据库和PostgreSQL17客户端。
- `bash scripts/verify-accountsvc.sh` 独立通过：11个包pass、274个顶层测试pass、0失败，服务子进程使用race构建；必需测试检查通过。原始证据在`linux-output/accountsvc-tests.jsonl`和`linux-output/admin-permissions/accountsvc-linux.log`。
- 两套Linux输出中的Compose示例因镜像缺Docker跳过，已在宿主Docker环境执行`TestConfigServiceClientComposeExamples`补验通过（`compose-examples-host.log`）。不可读资料文件场景因Linux root可读而跳过，已用同一Linux测试二进制、UID/GID65534补验通过（`unreadable-nonroot.log`）。普通套件的TestRecoveryFixture按约定skip，另由恢复脚本强制运行三阶段；未把这些skip本身当作通过。
- Go SDK夹具必需集成入口在独立环境以`go test -count=1 -tags integration ./sdks/testsupport/fixture`通过（`linux-output/admin-permissions/sdk-fixture-linux-isolated.log`）。
- 新增11项必需顶层测试已同步`scripts/required-tests.txt`，服务相关项同步`accountsvc-required-tests.txt`。新增登记后，用上述原始JSON字节流重跑testgate，两份清单均通过，没有删除或放宽门禁。
- 当前562个Go源文件与容器本地重跑所用source.tar逐字节相同；后续仅整理门禁登记和验收文档。29个变动Go文件gofmt及LF检查通过。路由/告警对服务权限常量的最终引用检查通过（`route-constants.log`）。
- 14份变动Markdown的173个本地文件链接核对通过；OpenSpec严格校验46/46通过，diff空白检查通过；最终交付前再核对任务状态。

### 证据位置与未验证项

原始日志、独立审查报告、消费者快照和合成测试材料位于被忽略的 `.test-output/admin-permissions/`。工作树创建因沙箱Git引用写入被拒绝，按技能回退在既有任务分支操作；默认Go缓存和全局CLI路径受限，验证经正常工具提权执行。

SDK首次真实宿主启动因验证脚本过早设置ACCOUNTKIT_ADMIN_INTEGRATION_*触发旧前缀检查而失败；调整脚本的环境注入顺序后通过，未放宽服务检查。共享常量重命名中一次遗漏调用点导致临时编译失败，补齐后六包回归通过。

首次Linux验证直接挂载Windows源码/Go缓存，internal/accountsvc包在默认10分钟达到超时，没有断言失败；该次运行失败，日志`full-linux.log`保留。随后将相同最终源码、完整Git历史及依赖归档复制到容器本地文件系统重跑，原有超时、断言、race和门禁均未调整，完整verify及accountsvc进程入口通过。完整本地重跑原始材料在`linux-output/`，没有将首次失败覆盖成成功。

组合脚本首次SDK夹具阶段因继承恢复阶段的ACCOUNTKIT_RECOVERY_*变量被服务前缀检查拒绝；两个前置完整脚本已成功退出。将SDK入口在独立环境补跑后通过，组合脚本也已修正环境隔离，未改变产品配置检查。非root测试的首次PowerShell参数拆分以及testgate首次文本管道转义错误，仅为验证工具调用失败；随后分别用完整参数和原始字节流执行通过。

结束时按创建记录的完整容器/网络ID核对并清理本任务三个容器及专用网络，保留原有postgres/redis容器；测试材料和日志保留。独立最终审查规格/质量均通过，所有实质问题关闭。全部实现与验证任务完成，未执行提交、推送、归档或发布。

本机Flutter3.47.9/Dart3.13.7与仓库要求3.47.4/3.13.3不同；本轮未运行Flutter及Android/iOS构建或完整SDK总入口，也未重新验证远端Git发布消费。Flutter/TS包运行代码和平台代码未修改，SDK当前证据限明确列出的管理员TS与Go夹具验证。真实身份提供方、产品网关、产品部署与在线CI不属于本次本地验证。

## 2026-11-16：提交规范补充与本地提交

用户要求将分阶段本地提交写入AGENTS.md，并提交本次成果。规则明确：任务步骤可独立验证、相关检查与审查通过后及时本地提交，包含配套测试、文档和OpenSpec任务/验证记录；本地提交不自动授权推送、合并或发布。同步修正设计和计划中由代理自行添加的“不提交”限制，不把已经完成的工作追写成历史阶段提交。

提交前OpenSpec严格校验46/46、OpenAPI 24/11操作检查、SDK 36操作覆盖和diff空白检查通过；重新运行Go普通套件通过，证据保存在`.test-output/admin-permissions/precommit-go.log`，该结果不替代上文真实数据库/race/恢复证据。

实现提交为 `4721bf7`（`feat(accountsvc)!: 支持显式管理角色权限配置`），包含实现、测试、配套文档和OpenSpec工件；AGENTS.md分阶段提交规则与本段验收补记作为独立文档提交保存。没有补造历史过程提交，没有推送、合并、归档或发布。
