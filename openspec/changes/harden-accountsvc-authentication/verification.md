# 验证记录

本次实现及独立审查完成，代码验证通过；全库 OpenSpec strict 存在下述未改动主规格问题，不宣称全仓规格全绿。分支 `feat/harden-accountsvc-authentication`；用户已授权提交本次改动，推送、归档和发布不在本次范围内。

## 完整改进验证（模式改名前）

环境为 Go 1.26.5、Linux、CGO_ENABLED=1、GOWORK=off，一次性 PostgreSQL17 / Redis7。最终源码（受Git管理及本任务新增文件，排除忽略文件）复制到容器本地 `/src`；独立构建不依赖工作区或 `.git`。真实服务进程也启用 race。

| 检查 | 实际结果 |
|---|---|
| `go build ./...` | 退出0 |
| `go vet ./...` | 退出0 |
| `go test -race -json -count=1 -timeout=10m ./...` | 退出0，56个有测试的包通过，1273个顶层测试通过，0失败；accountsvc包约515秒 |
| `go run ./internal/testgate scripts/required-tests.txt`，输入最终JSON日志 | 退出0，必需库用例未缺失或跳过 |
| `go run ./internal/testgate scripts/accountsvc-required-tests.txt`，同一日志 | 退出0，必需服务用例未缺失或跳过 |
| 宿主 `go test -count=1 -v ./internal/accountsvc -run '^TestConfigServiceClientComposeExamples$'` | 退出0，16个真实Compose配置子场景通过 |
| `node scripts/check-openapi.cjs` | 通过，24消费者/11管理操作及会话契约检查 |
| `node scripts/check-sdk-coverage.mjs` | 通过，36操作；身份解析登记新增真实Go协议测试 |
| `node --test scripts/check-sdk-coverage.test.mjs` | 13/13通过，无skip |
| Swagger parser验证三份OpenAPI | 全部通过（文档子任务执行） |
| `git diff --check` / 变更Go文件的 `gofmt -l` | 退出0，无格式待修；Git有行尾规范提示 |
| OpenSpec1.14.3 `validate --all --strict` | 38通过、7失败；本次change通过，见下方既有问题 |

原始日志保留于项目 `.test-output/auth-all-tests.jsonl`；运行脚本为 `.test-output/verify-authentication.sh` 与 `verify-authentication-snapshot.sh`。本任务创建的两个容器及匿名卷、独立网络已按创建ID清理；未操作既有产品资源。

## 跳过与未验证项

- Linux测试镜像无Docker CLI，`TestConfigServiceClientComposeExamples` 跳过；已在宿主补验通过。
- `TestProfileAttributesUnreadableFile` 因容器root可读取拒绝权限文件而跳过；该既有资料属性用例不计为通过。另有无测试文件的包级skip。
- `TestRecoveryFixture` 普通套件按项目约定跳过；本次没有结构或迁移变更，未执行独立pg_dump/pg_restore恢复演练，因此不宣称完整发布门禁通过。
- 未执行真实外部签发方、产品网关/直连隔离、SDK平台构建或远端Git消费验收。机器主体独占性须由实际提供方配置及部署验收保证，不能仅凭本地sub匹配证明其授权流程。

## OpenSpec既有问题

`auth-openspec-validation.log` 保存全量strict输出。以下未改动主规格包含超过500字符的既有需求而失败：accountsvc-runtime、accountsvc-service-authentication、consumer-token-introspection、embedded-auth-package、purged-user-feed、verification-delivery、versioned-auth-storage。`git diff --name-only -- openspec/specs migrations internal/userdb internal/auditdb` 为空；未缩短既有需求或放宽strict掩盖结果。

## 行为与独立审查

- 公共Authorization解析：三类入口唯一认证头、ASCII空格、16KiB Bearer、可信上下文/严格认证边界均有先失败后通过的测试；Task1独立审查通过。
- 路由清单实际参与挂载与服务权限装配，三端点测试验证认证先于业务输入及原URL/方法；Task2独立审查通过。
- 管理员静态装配、有界预加载与共享刷新、3秒等待、故障恢复、取消及关闭的测试和race通过。Task3审查发现异常排空出口会跳过验证器关闭，已拆出独立closeTokens并补回归，复审通过；数据库排空规则保持。
- 服务Bearer强制P∩scope、JSON精确主体快照、通用azp/rfc9068、不回退、配置半配置/迁移隔离及三端点拒绝均通过；Basic保持注册权限。Task4独立核心审查通过。
- 最终整体审查发现真实fixture给Basic-only配置主体；新增 `TestUnifiedServiceClientFixtureConfiguration` 无DB真实fixture+生产Registry测试先复现，再修正条件赋值。生产校验不放宽，最终复审关闭唯一P2。

## 验证历史与设计裁定

首轮快速全仓测试通过但未提供数据库，不计真实集成通过。首轮Linux绑定Windows源码目录的build/vet通过，真实测试复现上述fixture的四个Unified失败，accountsvc包随后在600秒超时；日志 `auth-initial-tests.jsonl` 保留。最终改为容器本地源码快照，测试超时、断言和必需清单均未放宽，完整重跑及两份门禁通过。

保留消费者可信Principal和未配置503优先级；多aud按包含语义，仍须主体和权限校验；scope采用严格RFC6749语法；机器主体采用不裁剪JSON数组。内部便利装配在无Bearer时忽略孤立TokenProfile，生产LoadConfig已拒绝该半配置且此路径不能启用Bearer，审查判定不构成认证绕过，未重复增加环境配置校验。相关实施方案见 [计划](../../../docs/superpowers/plans/2026-11-15-harden-accountsvc-authentication.md) 与 [设计](design.md)。

## 后续模式改名验证

用户明确要求将两项TOKEN_PROFILE的azp模式改名default。本轮只改配置模式名、默认值及配套说明；JWT azp字段、默认服务客户端匹配、管理员不要求客户端声明、RFC9068及主体/scope策略保持。旧azp模式值拒绝，不保留别名。

- RED：新default配置被旧实现拒绝、旧azp仍被接受、缺省仍为azp；管理员显式default被拒绝。修改后上述回归通过。
- `GOWORK=off go test ./internal/accountsvc/... -count=1`、对应 `go vet` 退出0；缺真实数据库的集成用例不计为通过。
- 父代理再次运行四个相关包 `go test -count=1 ... -run 'TokenProfile|TokenContract'`，退出0。
- OpenAPI源码检查与36操作coverage检查通过；本轮独立审查通过。新增管理员默认profile回归登记到两份必需测试清单。
- `openspec validate --all --strict` 仍为38通过、7个既有主规格长度问题；本变更通过，日志 `.test-output/auth-profile-rename-openspec.log`。
- 本轮命名调整未重跑真实数据库/race完整门禁；前述完整门禁证据属于改名前版本，不能当作本轮完整重跑结果。
