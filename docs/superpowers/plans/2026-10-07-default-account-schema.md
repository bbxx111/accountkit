# Default Account Schema Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 当前开发版的默认 PostgreSQL schema 改为 account。

**Architecture:** 只调整集中配置默认值及当前示例/文档；环境解析和服务沿用库默认值。保留显式 Schema 能力，不加入数据搬迁或探测逻辑。

**Tech Stack:** Go 1.26.5、pgx、PostgreSQL 17、现有 Chi/Redis 与 OpenSpec。

**Spec:** [proposal](../../../openspec/changes/default-account-schema/proposal.md)、[design](../../../openspec/changes/default-account-schema/design.md)、[delta](../../../openspec/changes/default-account-schema/specs/embedded-auth-package/spec.md)。

## Global Constraints

- 用户要求从 develop 派生任务分支直接实施，v0.1.0 未实际使用，不安排数据迁移或旧默认兼容逻辑。
- 空/未指定 Schema 默认 `account`，显式合法值保留；`AUTH_SCHEMA` 名称和 Redis 默认 `auth:` 不变。
- 不修改冻结 SQL、生成代码、依赖、HTTP/认证行为、历史归档/完成计划、v0.1.0 发布记录或 tag；不合并、归档、推送或发布。
- 原始日志在 ignored `.test-output/default-account-schema/`；验收记录在本 change verification.md。

## Review Focus

- 库 New、ConfigFromEnv 与服务装配对空 Schema 一致，默认断言分别覆盖。
- 显式 schema 包括 auth 仍为有效配置，保留既有覆盖并补相应纯配置测试。
- 默认实际落库 account，PoolConfig/search_path 一致，验证五表、迁移 version/dirty 与重复 Migrate。
- 既有 auth schema 不被自动创建或改动，真实用例比较其存在状态；已存在 account 时用例失败且不删除它。
- 当前默认文档与已发布源码的区别明确；当前默认修改不变成全仓 auth 替换。

### Task 1: 默认值、测试及当前文档一次调整

**Files:**
- Modify: `config.go`、`config_test.go`、`accountkit_test.go`、`internal/accountsvc/config_test.go`。
- Create: `default_schema_test.go`；Modify: `scripts/required-tests.txt`（追加对应顶层用例）。
- Modify: `README.md`、`AGENTS.md`、`docs/migrations.md`、`docs/compatibility.md`、`deploy/accountsvc/compose.yaml`；`docs/accountsvc.md` 仅补有助于当前默认值的说明。

**Interfaces:** `Config.Schema`、`ConfigFromEnv(prefix)`、`Auth.Config()`、`PoolConfig(dsn,schema)`、`Auth.Migrate(ctx)` 签名不变，仅空 Schema 的默认值不同。

- [ ] 修改 `TestValidateAppliesDefaults`、`TestNewIsPureAndAppliesDefaults` 和服务 `TestLoadConfig` 中默认 Schema 为 account；确保默认配置测试不受宿主同名前缀环境污染。`go test -count=1 . ./internal/accountsvc -run 'TestValidateAppliesDefaults|TestNewIsPureAndAppliesDefaults|TestLoadConfig'` 必须在生产修改前因 Schema=auth 失败，记录 RED。
- [ ] 最小修改 `config.go` 的 Schema 默认及注释；保留环境名、Redis 前缀及显式配置。补空/未指定与显式 account/auth/custom 的配置覆盖，保留既有环境覆盖测试。运行上述命令记录 GREEN。
- [ ] 新增 `TestDefaultSchemaMigrateAgainstRealDB`，复用 root `dbDSN/minimal/testRedis`：先确认 account 不存在，若存在则 fail 且不清理；记录 auth 存在状态；创建 PoolConfig(dsn,"account")、用空 Schema 构造 New 并 Migrate，断言 Config.Schema/account search_path，五个包表及 version=1/dirty=false，重复 Migrate 成功，auth 存在状态不变。只清理本例创建的 account；清理应先于连接池 Close。追加全名 `github.com/bbxx111/accountkit/TestDefaultSchemaMigrateAgainstRealDB` 到必需清单，保留其他项。
- [ ] 当前 README/AGENTS/迁移说明及查询示例默认 account；Compose 明确配置 account。兼容说明仅记录开发默认调整和显式配置能力，不增加搬迁步骤；README 区分开发版默认与 v0.1.0 的 auth。保留历史计划/归档/发布说明与 auth_staging/auth_x 等显式 schema 测试。
- [ ] gofmt；`GOWORK=off go build ./...`、`go vet ./...`、`go test -json -count=1 ./...`，数据库缺少时只报告普通测试与 skip。控制器提供一次性 PG17，Linux 执行 `go test -race -json -count=1 . ./migrations ./internal/accountsvc -run 'TestDefaultSchemaMigrateAgainstRealDB|TestMigrateStartCloseAgainstRealDB|TestMigrateRejectsPoolWithoutSchemaOnSearchPath|TestIndependentInstances|TestSourceDatabaseTakeover|TestTwoSchemasCoexist|TestUpCreatesTablesInsideSchemaOnly|TestSchemaPinsInvariants|TestValidateAppliesDefaults|TestNewIsPureAndAppliesDefaults|TestConfigFromEnv|TestLoadConfig'`，确认本次全部定向顶层用例实际 PASS。
- [ ] 工作者自审并只提交本任务代码/文档/选择器，报告 RED/GREEN、命令/输出、默认值与显式配置、文件范围和 skip。控制器进行任务及最终独立审查，检查迁移历史和 OpenSpec strict，补本 change 验收与完成标记。

## Controller Handoff

工作者拥有本 Task 1 列出的文件，不修改计划/tasks/verification；控制器负责一次性 PostgreSQL、Linux 定向 race、主规格/迁移检查与验收收尾。新用例为永久的真实落库检查，不为默认常量添加镜像实现测试。
