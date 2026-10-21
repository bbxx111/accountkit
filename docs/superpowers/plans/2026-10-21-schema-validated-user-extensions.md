# 用户扩展资料 Implementation Plan

> **历史计划，受影响步骤已被替代。** 本文勾选及命令结果仅描述 54afa4b 旧实现。本轮命名改为 profile_attributes，移除 revision/expected_revision/Conflict，改为数据库账号行锁内最新读取→Merge Patch→完整 Schema 校验→提交。任务 3–6 的存储、公开 API、并发、生命周期、审计和恢复步骤，以及任务 1/2 中相关命名与保留键，以[本轮详细计划](2026-10-21-profile-attributes-row-lock.md)为准；Schema 版本、指纹和独立更新时间保留。旧完成证据不能证明本轮完成。本文余下历史原文保持。

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为宿主增加基于 JSON Schema 的账号扩展资料及带版本条件的 JSON Merge Patch，覆盖数据库升级、状态控制、审计和匿名化。

**Architecture:** 在内部包封装精确 JSON 解析、Schema 编译和补丁处理，由 `user.Service` 执行账号锁内的领域写入。资料放在 `user_account` 的新增列，Schema 版本/指纹与资料 revision 独立，现有 HTTP 只沿用明确 DTO。

**Tech Stack:** Go 1.26.5、PostgreSQL 17、pgx/v5、sqlc 1.31.1、`github.com/santhosh-tekuri/jsonschema/v6` 候选、现有异步审计与 Linux race 验证。

**Spec:** [设计](../../../openspec/changes/add-schema-validated-user-extensions/design.md)、[提案](../../../openspec/changes/add-schema-validated-user-extensions/proposal.md)、[扩展资料](../../../openspec/changes/add-schema-validated-user-extensions/specs/user-profile-extensions/spec.md)、[生命周期](../../../openspec/changes/add-schema-validated-user-extensions/specs/embedded-auth-package/spec.md)、[存储升级](../../../openspec/changes/add-schema-validated-user-extensions/specs/versioned-auth-storage/spec.md)、[迁移安全](../../../openspec/changes/add-schema-validated-user-extensions/specs/migration-safety/spec.md)。

## Global Constraints

- 固定 Draft 2020-12，主声明树每层对象固定 properties 和 additionalProperties=false，初始化纯计算且每个 Service 只编译一次；配置 nil 关闭新增接口。
- Schema 原始内容 64 KiB、JSON 深度 32、展开属性 128、展开校验节点 1024；补丁原始内容和规范化资料各 16 KiB，深度 8、值节点 512，根深度为 1。
- 支持关键字、format 白名单、保留属性名及版本格式以设计 Decisions 1/2 为准，不扩大、缩小或静默忽略其范围。
- 数值不经有损 float64；`1`、`1.0`、`1e0` 按数值相等；拒绝重复键、非法 Unicode、NUL、尾随内容及 JSONB 不可存储值。
- 顶层补丁必须是对象；属性 null 删除、数组整体替换，未知属性 null 也拒绝；资料中不保存 JSON null。
- 普通写入仅 ACTIVE，读取 ACTIVE/FROZEN；显式 expected_revision，0 有效；无变化不更新元数据；Schema 标识不匹配禁止普通更新。
- 保持 v0.4.0 `0001`、固定哈希、冻结源及所有正式标签；新增 `0002_add_user_extensions`，真实验证各代历史同号结构拒绝。
- 新增 SQL 进入 user/query.sql；user/db 与 audit/db 只由 sqlc generate 更新；无外键、通用 JSONB 索引、触发器或宿主业务字段。
- 冷静期保留，purge 同事务清空；revision 达 BIGINT 上限也必须清理；审计尽力且不包含资料值、补丁、属性名或 Schema 全文。
- 宿主负责业务授权；不新增内置 HTTP 路由、Schema 文件环境变量、accountsvc 装配或隐式扩展响应。
- 日志与报告只用合成数据；测试输出存 `.test-output/add-schema-validated-user-extensions/`，不得把跳过的数据库用例称为已通过。
- 按仓库默认子代理实施，每项任务完成后独立审查；当前尚未获提交、推送、合并或发布授权，步骤仅留工作树差异供审阅。

## Review Focus

1. 对已声明但尚不存在的对象提交 `{}` 会创建对象，可能因子字段 required 失败；任务 2 单独覆盖，不能总当作无变化。
2. 数组整体替换中的对象仍有未知属性和 null 边界；任务 2 检查目录及最终 Schema，不能因数组是叶子补丁漏验。
3. Schema 关闭后匿名化和历史资料持久化仍须正确；任务 5 测试无配置、不同 Schema 及回调回滚。
4. 新生产 version 2 不能接管任何历史撤回 version 2；任务 3 将真实源存在与结构预检分开验收。
5. SQL `SELECT *` 的生成模型会增加列，但 DTO 和令牌不能泄露；任务 4 对真实带敏感扩展的账号做公开响应回归。

## Execution Order

任务依赖为 `1 → 2 → 4 → 5 → 6`，存储任务 `3 → 4`。为了共享文档和 sqlc 生成结果不互相覆盖，默认顺序执行 1、2、3、4、5、6，不同时派发实现代理。每个任务使用新实现代理，随后独立规格与质量审查；修复回到原实现代理。

实施前按 using-git-worktrees 检查隔离方式，保留已有未提交规划工件；如果用户选择当前任务分支，则在 `feat/schema-validated-user-extensions` 原地实施。验证当前工具实际可用性和一次性资源归属后运行基线，缺少 PostgreSQL/Redis/恢复客户端则准备本任务专用资源，不能复用未知产品资源。

## File Structure

| 路径 | 职责与任务 |
|---|---|
| `internal/extensionjson/` | 精确、有界、脱敏的 JSON 解码及规范化；任务 1 |
| `internal/userextensions/` | 校验器隔离、Schema 使用范围、目录及 Merge Patch；任务 1/2 |
| `user/extensions.go`、`user/service_extensions.go` | 公开类型、错误与服务读写；任务 4 |
| `user/service_extensions_audit.go` | 成功修改事件；任务 4/5 |
| `config.go`、`accountkit.go`、`user/service.go` | 可选配置及只编译一次的构造；任务 4 |
| `migrations/0002_add_user_extensions.*.sql`、`migrations/baseline.go` | 新列、基础约束及结构预检；任务 3 |
| `user/query.sql`、生成的 `user/db/`、`audit/db/` | 读取/条件更新、purge 及生成行形状；任务 3/5 |
| `user/service_extensions*_test.go` | 真实状态、并发、审计、隐私与生命周期验证；任务 4/5 |
| `recovery_test.go`、`scripts/required-tests.txt` | 真实恢复材料与必需测试门禁；任务 6 |
| `README.md`、`docs/compatibility.md`、`docs/migrations.md`、`examples/embedded/` | 每项能力对应的接入与迁移说明；所属任务随实现更新 |

## Task 1: 精确 JSON 基础与校验器适配验证

**OpenSpec tasks:** 1.2、2.1。

**Files:** 新建 `internal/extensionjson/json.go`、`number.go`、`json_test.go`、`fuzz_test.go`；新建 `internal/userextensions/validator_test.go`；修改 `go.mod`、`go.sum`。不修改现有 ClientInfo 的校验行为。

**Interfaces:**

```go
// internal/extensionjson
type Limits struct {
    MaxBytes int
    MaxDepth int
    MaxNodes int // 0 表示由输入字节上限约束，不单独计节点上限。
}
func Decode(raw []byte, limits Limits) (any, error)
func Canonical(value any) ([]byte, error)
func Clone(value any) any
```

Decode 返回 `map[string]any`、`[]any`、string、bool、nil、精确 `json.Number`；保留 null 供 Schema 注解和补丁，资料禁用 null 在任务 2 执行。规范化对等值数值产生一致表示；数字展开在分配前核验范围和结果预算。所有错误只含固定分类及受控位置，不附带输入值。

- [x] **Step 1: 写 `TestDecodeRejectsAmbiguousJSON` 与 `TestCanonicalNumbers`，固定以下断言。**

```go
invalid := []string{
    `{"x":1,"x":2}`, `{"x":"\ud800"}`, `{"x":"\u0000"}`,
    `{"x":1}{"y":2}`, `{"x":1e999999999}`,
}
equivalent := []string{`{"n":1}`, `{"n":1.0}`, `{"n":1e0}`}
// invalid 全部返回错误；equivalent 的 Canonical 输出相同。
// {"n":9007199254740993} 往返保持整数，不变为 9007199254740992。
```

增加非法 UTF-8 字节、重复键的转义等价形式、合法代理对、空白占满原始容量、深度 8/9 和节点 512/513 边界；Schema 输入单独使用 64 KiB/32 层限制。fuzz 种子不得 panic 或产生不同于精确值的往返结果。

- [x] **Step 2: 运行 `go test -count=1 ./internal/extensionjson` 保存 RED，再按接口实现解析、数字边界和规范化。** 不把仅因依赖未安装或 fixture 错误导致的失败当作行为 RED。
- [x] **Step 3: 编写 `TestValidatorCandidate`。** 验证 `integer` 接受 1.0、拒绝 1.5 和 `"1"`；精确判断大整数 const 和小数 multipleOf；不修改 default；format 断言拒绝错误日期；外部 URL/file 引用不触发 I/O；同一编译结果并发验证安全。
- [x] **Step 4: 选择满足行为的 v6 版本并固定依赖。** 先读候选实际源码/文档再写适配调用，失败不得降精度或关闭断言；用 Go 标准 regexp 引擎验证设计允许的表达式范围。
- [x] **Step 5: 运行 `go test -count=1 ./internal/extensionjson ./internal/userextensions`、相关包 vet 和 Linux race，保存输出并独立审查。** 在验收记录记录候选版本及可用性结论；不把这些测试当作服务已完成。

## Task 2: Schema 策略、属性目录与 Merge Patch

**OpenSpec tasks:** 1.3、2.2、2.3；依赖任务 1。

**Files:** 新建 `internal/userextensions/policy.go`、`schema.go`、`patch.go`、`errors.go` 及对应 `_test.go`；修改 `docs/compatibility.md` 的补丁语义说明。

**Interfaces:**

```go
// internal/userextensions
type Policy struct { /* 私有只读编译结果、目录、版本及指纹 */ }
type Patch struct { /* 私有已解析补丁；执行时不修改 */ }
type Violation struct { Path, Rule string }
type ValidationError struct { Violations []Violation }
func Compile(version string, schema json.RawMessage) (*Policy, error)
func (p *Policy) Version() string
func (p *Policy) Fingerprint() string
func DecodePatch(raw json.RawMessage) (*Patch, error)
func NormalizeStored(raw json.RawMessage) (json.RawMessage, error)
func (p *Policy) Apply(current json.RawMessage, patch *Patch) (next json.RawMessage, changed bool, err error)
```

DecodePatch 仅完成语法和容量检查，路径白名单由 Apply 检查，使领域层可保持先状态/revision、再 Schema/路径的错误顺序。NormalizeStored 只验证通用对象、null、精度及存储边界，不依赖当前 Schema。Apply 不修改原始资料或 Patch，且返回已通过完整 Schema 的规范化结果。

- [x] **Step 1: 写 `TestCompilePolicy`。** 覆盖 nil/空版本、非法版本、错方言、根不能接受 `{}`、未知关键字、保留字段、动态字段、类型联合、嵌套 `$id`、本地引用复用/循环/超限、format 白名单、正则范围和初始化无 I/O。
- [x] **Step 2: 写 `TestPolicyMergePatch` 并先运行 RED。** 以下输入在声明相应字段的合法 Schema 下产生明确结果：

```go
current := `{"bio":"old","preferences":{"theme":"LIGHT","notifications":true}}`
patch := `{"bio":null,"preferences":{"theme":"DARK"}}`
want := `{"preferences":{"notifications":true,"theme":"DARK"}}`
// Apply(current, DecodePatch(patch)) 返回 want、changed=true。
// {"unknown_field":null} 必须拒绝；不能因结果不存在该字段而放过。
```

独立覆盖数组对象未知属性、数组中的 null、已有对象 `{}`、缺失对象 `{}` 触发子字段 required、删除父对象、删除不存在已声明字段、必填子字段删除、条件约束、等值数字无变化及双方数据深拷贝。

- [x] **Step 3: 实现 Schema 使用范围检查和目录，再离线调用标准编译器。** 展开过程中计数、检测循环，不对全部分支组合做可满足性枚举。条件片段只能约束主树既有属性，所支持关键字的真假由校验器判断；Schema 通过后计算包含注解的规范化 SHA-256。
- [x] **Step 4: 实现 RFC 7396 合并和完整结果验证。** 最多返回 16 个安全路径/规则；未知键使用最近合法父路径，不能保留原始校验器错误供日志或 errors.Unwrap 泄露。
- [x] **Step 5: 运行 `go test -count=1 ./internal/userextensions ./internal/extensionjson`、vet 和 race；更新兼容说明，独立审查。** 测试证明注解、未更改数据及未声明 null 操作的行为，不通过修改 Schema 放宽失败用例。

## Task 3: 递增迁移、预检和 sqlc 查询

**OpenSpec tasks:** 3.1–3.4。

**Files:** 新增 `migrations/0002_add_user_extensions.up.sql` / `.down.sql`、`migrations/user_extensions_test.go`；修改 `migrations/baseline.go`、相关迁移测试、`user/query.sql`、`user/repo_test.go`、`docs/migrations.md`；运行 sqlc 生成 `user/db/`、`audit/db/`。

**Interfaces:**

- 表结构采用设计 Decisions 3 的五列与四个基础约束。
- 新增 sqlc `GetUserExtensions :one`，显式返回 id、state 和五个扩展列；锁继续使用既有 `LockUserByID`。
- 新增 `UpdateUserExtensions :one`，参数为 id、expected_revision、extensions、extensions_schema_version、extensions_schema_fingerprint、now；WHERE 同时限定 id、revision 和传入的 ACTIVE state，revision 在 SQL 中加一并返回五列及 id/state。
- 保持现有 CreateUser/ImportAccount 默认空扩展，现有 SELECT/RETURNING `*` 生成模型变化通过编译检查全部调用；所有公开响应仍使用明确 DTO。

- [x] **Step 1: 先写 `TestUserExtensionsMigration`。** 使用冻结的 v0.4.0 SQL 在一次性数据库装载合成账号及关联数据，断言升级前 v1、升级后 v2/clean，旧值和原时间相同，扩展为 `{}`/revision=0/NULL 元数据。
- [x] **Step 2: 写 `TestUserExtensionsBaseline` 并保存 RED。** 覆盖合法 v2 重复 Up、不完整列/默认值/约束、v1 手工残留扩展列、历史撤回 v2、dirty 优先、旧数据/catalog 不变、并发与取消后锁释放。
- [x] **Step 3: 新增生产 SQL 并扩展版本感知预检。** 先确认完整基础结构，再区分 v1/v2；测试源继续在 latest+1 注入 probe，不能用新生产 `0002` 覆盖失败夹具。
- [x] **Step 4: 添加查询并运行 `sqlc generate`。** 仓储测试验证有条件更新、版本冲突无行更新、大整数与空元数据往返；不手改生成文件。
- [x] **Step 5: 运行真实 `go test -count=1 ./migrations ./user -run 'Test(UserExtensionsMigration|UserExtensionsBaseline|Repo.*Extensions|Upgrade.*)'`，再运行全部迁移测试及历史/sqlc 检查。** 更新迁移文档，并独立审查新合法 v2 与历史拒绝边界。

## Task 4: 配置装配、领域接口和成功审计

**OpenSpec tasks:** 1.4、4.1–4.4、5.3；依赖任务 2/3。

**Files:** 新增 `user/extensions.go`、`user/service_extensions.go`、`user/service_extensions_audit.go`、`user/service_extensions_test.go`、`user/service_extensions_concurrency_test.go`、`user/service_extensions_audit_test.go`、`accountkit_extensions_test.go`；修改 `config.go`、`accountkit.go`、`user/service.go`、README、开发宿主示例。

**Interfaces:**

- 公开 `ExtensionsConfig`、`ExtensionsView`、`ExtensionsPatch` 及 GetExtensions/PatchExtensions 完全采用设计 Decisions 2。
- 新增 `user.ValidateExtensionsConfig(config *ExtensionsConfig) error`，nil 成功，其余调用任务 2 编译器只做验证；直接 NewService 编译并保存私有 `*userextensions.Policy`。
- Config 新增 `UserExtensions *user.ExtensionsConfig`，Deps 新增对应指针。把当前基础配置校验提为私有 `validateBase()`；公开 Validate 依次调用基础与扩展验证，实际 New 调用基础校验再由 NewService 编译扩展，避免同一构造编译两次。
- 公共错误新增 `ErrExtensionsNotConfigured`、`ErrExtensionsConflict`、`ErrExtensionsSchemaMismatch`；非法格式/Schema 使用 ErrInvalidArgument，状态沿用 ErrInvalidState。结构化 `ExtensionsValidationError` 只持有安全路径/规则且支持 `errors.Is(err, ErrInvalidArgument)`。

- [x] **Step 1: 写构造和接口测试并保存 RED。** BeforeConnect 禁止 I/O；验证 nil 功能、独立 Config.Validate、直接 NewService、配置深拷贝、初始视图、缺失/负/过期 revision、合法未知 ID、ACTIVE/FROZEN 读、非 ACTIVE 写和冷静期读取拒绝。
- [x] **Step 2: 实现配置传递和锁内领域更新。** 复用 withDomainTx；DecodePatch 在锁外，获取锁后检查存在/状态/revision/Schema 标识，再 Apply。无变化返回原元数据；有变化检查上限并调用条件 UPDATE，只有成功提交后返回结果。
- [x] **Step 3: 写真实并发测试 `TestUserExtensionsConcurrency`。** 用可观察的锁持有/排队安排代替仅 sleep：同 revision 只有一次实际写；更新与冻结/注销两种先后顺序；较高默认隔离级别仍锁后最新；取消回滚；提交断连不返回成功或保证未提交。
- [x] **Step 4: 接入成功审计并验证隐私。** 使用通用 audit.Event、UUIDv7、配置 source/namespace、SERVICE/accountkit-host actor、USER 目标和动作 `USER_EXTENSIONS_UPDATED`；安全 data 仅含 Schema 标识及新 revision，context 只取 request_id；Freeze 后按既有 Recorder 投递，失败不改变已提交结果。测试注入写侧、无变化不投递、原始内容与属性名不出现在事件或日志。
- [x] **Step 5: 增加公开响应兼容回归。** 为真实有扩展资料账号检查 GetMe、两面详情、BatchPublicProfiles、JWT 和内省；确认没有附加扩展字段，不新增认证查询，sqlc 行不能被直接序列化。
- [x] **Step 6: 更新 README 的完整 Schema/Go 示例及 expected_revision 冲突处理，编译示例。** 运行相关根包、user、httpapi 测试与 vet，真实并发用例通过后独立审查。

## Task 5: 匿名化、导入默认值与隐私生命周期

**OpenSpec tasks:** 5.1、5.2、5.4；依赖任务 4。

**Files:** 修改 `user/query.sql` 的 PurgeUser 并重新生成，新增 `user/service_extensions_lifecycle_test.go`、`user/service_extensions_privacy_test.go`；扩展 `user/service_purge_test.go`、`user/service_import_test.go`，同步 README 与兼容指南。

**Interfaces:** 使用现有 PurgeUser 参数和公开生命周期方法，不新增绕过状态的通用更新接口；清理直接执行 SQL，不调用当前 Schema。

- [x] **Step 1: 写 `TestUserExtensionsLifecycle` 并先运行 RED。** ACTIVE 写入后冻结/解冻/冷静期/恢复均保持扩展和元数据；最终 purge 清空及去 Schema 标识；宿主回调失败全部回滚；无配置和配置不匹配仍能 purge。
- [x] **Step 2: 扩展 PurgeUser。** 已使用资料 revision 非上限时加一，上限时保持且清理时间照常记录；从未写入的空资料保持元数据未初始化；已 DELETED 重复维护不推进。
- [x] **Step 3: 验证默认创建与 ImportAccounts。** ACTIVE 和 DELETED 导入均无扩展；ACTIVE 完成导入后可另调 PatchExtensions，DELETED 拒绝；文档不得承诺这两个调用在同一批事务内。
- [x] **Step 4: 验证审计及日志中的合成敏感文本不泄露。** 包括校验器 enum/const 错误、未知属性名、SQL/事务错误和匿名化失败；公开错误不能通过 Unwrap 找回原输入。
- [x] **Step 5: 运行生命周期、导入、审计隐私及 `TestPurgeCoversEveryTableWithUserID`，sqlc 检查通过，更新清理和导入说明，独立审查。**

## Task 6: 完整集成、性能与恢复验收

**OpenSpec tasks:** 6.1–6.4；依赖所有前置任务。

**Files:** 修改 `recovery_test.go`、`scripts/required-tests.txt`；新增 `internal/userextensions/benchmark_test.go`、必要的 user 上界并发测试；更新变更 verification.md，不修改冻结源。

**Interfaces:** 沿用 scripts/verify.sh 和 verify-recovery.sh 的现有环境变量与三阶段 fixture，新增资料使用前述公共接口及明确合成值。

- [x] **Step 1: 为恢复夹具增加带 Schema 身份、精确大整数、revision 和时间的资料。** seed 在新结构写入，verify 在独立恢复库读取并核对，source-unchanged 验证源数据未变；在正常套件继续保持 TestRecoveryFixture 的预期 skip。
- [x] **Step 2: 同步必需测试清单。** 新增顶层集成用例必须在真实环境执行；不删除旧条目或把 skip 当成成功。
- [x] **Step 3: 写代表性和上限 benchmark。** 测量 Compile、Apply 的 ns/op 与 allocs/op，覆盖 128 属性、1024 展开节点、16 KiB 数据及重复本地引用；以验证过的最大合法深度/节点夹具避免 benchmark 只测快速拒绝。记录同账号并发等待与不同账号并发结果。
- [x] **Step 4: 执行完整检查并保留原始输出。** `GOWORK=off go build ./...`、`go vet ./...`、`go test -count=1 ./...`、`bash scripts/check-generated.sh`、`bash scripts/check-migrations.sh`、提供专用 PostgreSQL/Redis/恢复库后执行 `bash scripts/verify.sh`，最后 `openspec validate --all --strict`。Windows race 条件不足时使用 tests/Dockerfile 的 Linux 验证环境。
- [x] **Step 5: 执行最终独立审查并处理实质问题。** 汇总每项任务审查、实际测试、未验证项到变更 verification.md；只有证据完整才勾选 OpenSpec 对应任务。保持用户未授权的提交、推送、合并和归档动作未执行。

## Coverage and Self-Review

| 规格主题 | 实施任务 | 主要证据 |
|---|---|---|
| 离线配置、固定字段树、容量与精度 | 1/2/4 | parser/编译器/无 I/O 构造测试 |
| Merge Patch、完整结果、未知 null | 2 | 合并矩阵及条件约束测试 |
| Go 接口、错误、Schema 身份、并发 | 4 | 真实数据库状态与锁竞争测试 |
| 生命周期、导入默认值、隐私 | 4/5 | purge 回滚、无配置清理、DTO/日志回归 |
| 新 v2 与历史版本拒绝 | 3 | 迁移前后对照、catalog 无写拒绝 |
| 发布恢复、性能和必需门禁 | 6 | 完整脚本、三阶段恢复、benchmark/race |

接口核对：任务 4 只消费任务 2 的 Policy/DecodePatch/NormalizeStored，不自行实现第二套 JSON 校验；任务 5 复用既有事务与 PurgeUser 签名；任务 3/5 对同一查询文件和生成结果顺序修改。各任务测试与文档随所属实现交付，最终任务只负责跨能力验收。

计划编写阶段未运行代码测试，未准备数据库或容器，未安装候选依赖。实施前的计划评审与工作区选择按执行技能要求完成。
