# 验收记录：unify-accountsvc-env-prefix

日期：2026-10-28。分支 `refactor/accountsvc-env-prefix`。

## 实施方式

本会话内顺序实施，未使用逐任务子代理：改动集中在 `internal/accountsvc/config.go` 及测试，其余为机械改名。功能部分先写失败测试（TDD）。

## 检查结果

| 检查 | 结果 |
|---|---|
| 新测试改动前失败 | `TestConfigRejectsLegacyPrefix`、`TestConfigReadsLibrarySettingsWithServicePrefix`、`TestServiceAndLibraryEnvNamesDoNotOverlap` 在仅加入前缀常量时均失败 |
| 重名检测有效性 | 扫描到 38 个服务配置名逐一探测；临时在非测试文件加入 `env("AUTH_SCHEMA")` 后测试失败并指出 `ACCOUNTSVC_AUTH_SCHEMA`，还原后通过；库名 `AUTH_SCHEMA` 的正向对照通过 |
| Windows `GOWORK=off go build ./...`、`go vet ./...`、`go test -count=1 ./...` | 通过（无数据库 DSN，数据库用例 skip） |
| Linux 镜像 + 一次性 PG 17.4 / Redis 7 `scripts/verify-accountsvc.sh` | 通过：race，574 个测试事件 pass，0 fail；`internal/accountsvc` 包耗时 229 秒 |
| 必需测试门禁 | 三个新测试加入 `scripts/accountsvc-required-tests.txt`，门禁对本次 JSON 输出通过 |
| `openspec validate --all --strict` | 41 项通过 |
| `git diff --check` | 无问题 |

唯一 skip：`TestProfileAttributesUnreadableFile`（容器内以 root 运行，可读取无权限文件；与本变更无关的既有行为）。

一次性容器、网络、卷与镜像已删除；未触碰其他项目的 `postgres`、`redis` 容器。

## 剩余 `ACCOUNTKIT_` 引用

均为有意保留：库文档与示例（README、`docs/go-integration.md`、`examples/*`、AGENTS.md）、库测试（根 `config_*_test.go`、`recovery_test.go`、`user/code`）、恢复演练基础设施（`ACCOUNTKIT_RECOVERY_*`、CI、`scripts/verify*.sh`）、库规范（alipay/qq/user-basic-profile）、本变更的拒绝逻辑与测试、主规范 `accountsvc-runtime`（归档时由本变更的 delta 更新）。

## 未验证项

- 未用完整 `scripts/verify.sh`（库全量 race 与恢复演练）：本变更不改库代码，库测试未改。
- 接入方（ai-food）的部署配置须自行改名，启动拒绝会列出需改的变量。
