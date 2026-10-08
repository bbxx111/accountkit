# 默认 account schema 验收记录

日期：2026-10-07。依据：[proposal](proposal.md)、[design](design.md)、[增量规格](specs/embedded-auth-package/spec.md)。实施计划：[详细计划](../../../../docs/superpowers/plans/2026-10-07-default-account-schema.md)。

## 范围与提交

从 develop 的 `71dcace` 派生 `refactor/default-account-schema`，规划提交 `faca0c6`，实施提交 `1e50c01`。用户确认 v0.1.0 未实际使用，直接将默认 schema 调整为 account，不安排数据搬迁、旧默认探测或兼容分支。

仅集中配置默认值和注释改变；库 New、环境配置及 accountsvc 复用此路径。显式 account/auth/custom_account 和既有 auth_staging 配置保留；AUTH_SCHEMA 名称与 Redis 默认 auth: 不变。当前指南及 Compose 使用 account，README 区分当前开发默认与已发布 v0.1.0 的 auth。

冻结 SQL、生成结果、固定源基线、go.mod/go.sum 与发布 tag 无差异；历史归档与 v0.1.0 发布记录相对任务分支起点无差异。

## RED / GREEN

使用 GOWORK=off 和工作区 Go 编译缓存：

1. 生产修改前运行 `go test -count=1 . ./internal/accountsvc -run 'TestValidateAppliesDefaults|TestNewIsPureAndAppliesDefaults|TestLoadConfig'`，原生 exit 1；库默认及新服务空/未指定配置用例仍得到 auth，预期 account。
2. 增加覆盖后、生产仍未修改时运行 `go test -count=1 . ./internal/accountsvc -run 'TestConfigFromEnvSchema|TestNewSchemaConfiguration|TestConfigProductionDefaultsAndMigrate|TestLoadConfigSchema'`，原生 exit 1；空/默认用例失败，显式 schema 子例通过。
3. 修改集中默认值后重跑完全相同的两条命令，均原生 exit 0。

原始日志与各次原生退出码在 ignored `.test-output/default-account-schema/{red,red-coverage,green,green-coverage}.{log,exit}`。

## 当前验证

| 检查 | 实际结果 |
|---|---|
| gofmt、`GOWORK=off go build ./...`、`go vet ./...` | exit 0；Windows build 有已披露的模块 stat-cache 写权限警告，vet 无输出 |
| `go test -json -count=1 ./...` | exit 0，282 个顶层 PASS、133 SKIP、0 FAIL；原始日志 tests.log，跳过项逐项记录于 skipped-tests.json |
| 一次性 PostgreSQL 17 / Linux CGO 定向 race | exit 0，17 个顶层 PASS、0 SKIP/FAIL；14 个必需定向选择器均 PASS，stderr 无输出 |
| 默认数据库用例 | TestDefaultSchemaMigrateAgainstRealDB 实际 PASS；验证 account search_path、五表、version=1/dirty=false、重复 Migrate、auth 存在状态不变及仅清理本例对象 |
| 迁移历史检查 | exit 0，固定源与正式版本历史校验通过 |
| 当前文档、链接及 OpenSpec strict | 56 个本地链接检查通过，diff-check 无错误，OpenSpec strict 13/13 PASS |

普通套件未提供全量 PostgreSQL/Redis 依赖，133 个 skip 不作为集成通过证据；其中普通 TestRecoveryFixture 为预期 skip。真实数据库验证只执行本次默认值及现有隔离/迁移/配置相关定向用例，不能表述为完整数据库套件或发布门禁已通过。

必需清单在保留全部原 338 条目的基础上追加四项（总计 342）：TestConfigFromEnvSchema、TestNewSchemaConfiguration、TestDefaultSchemaMigrateAgainstRealDB、internal/accountsvc/TestLoadConfigSchema。本次四项均由定向 race 实际执行通过。

本次 PostgreSQL 容器 default-schema-20261007-pg 及网络已清理；原有 postgres/redis 服务未操作。未重跑完整 verify.sh、严格服务进程门禁或恢复三阶段，实施阶段未进行产品环境验收、合并、推送、归档或新版本发布。

## 独立审查与执行决定

任务独立审查：Spec compliant / Approved，无 Critical/Important。Windows build 的 stat-cache 权限警告列为已披露环境项，原生退出码为 0，Linux 定向 race stderr 无输出。控制器核对冻结 SQL、tag 和 OpenSpec 证据如上。

最终分支审查（`71dcace..1a59ed8`）：Spec compliant / Approved，Ready to merge: Yes，无 Critical/Important。缓存权限警告接受为已披露非阻塞环境项；测试选择器修正与原始证据一致，没有待修实现问题。数据搬迁/旧默认探测、完整发布/恢复/产品环境验收和全局缓存权限修复均按已确认范围不在本次交付内。

执行中修正了计划的服务测试选择器：原计划 TestLoadConfig 不是仓库现有测试名，改用 TestConfigProductionDefaultsAndMigrate 并新增 TestLoadConfigSchema 覆盖服务配置。若选择器错误会漏验服务默认值；本次定向必需门禁明确确认两者实际 PASS，未扩大行为范围。

## 归档

2026-10-07 按用户要求归档至 `openspec/changes/archive/2026-10-07-default-account-schema/`。产品部署隔离主规格已同步默认 account、空 Schema 及显式 Schema 场景，原有要求和隔离场景保留。归档修复计划与验收记录的相对链接；随后按用户要求以独立 merge commit 合入 develop，实际合并状态以 Git 历史为准。本次不推送或发布版本，v0.1.0 tag 保持原提交。
