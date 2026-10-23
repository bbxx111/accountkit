# 微信小程序与固定身份模式验收

日期：2026-10-23。任务分支 `feat/wechat-mini-program-subject-mode`，起点 `6e8c88850ff68861b1821dee0d925bf52c689a4e`。范围见 [proposal](proposal.md)、[design](design.md)、[规格](specs/wechat-sign-in/spec.md) 和 [实施计划](../../../../docs/superpowers/plans/2026-10-23-wechat-mini-program-sign-in.md)。

## 实现范围

- `WECHAT_APPS` / `WECHAT_MINI_PROGRAM_APPS` 沿用 `appid:secret,...`；全局 `WECHAT_SUBJECT_MODE` 缺省 UNIONID，显式仅接受 UNIONID/OPENID。
- OAuth 与 jscode2session 按服务端 AppID 配置选择，保留统一 kind/credential 请求。两组 AppID 唯一、凭据非空、AppID 不含冒号。
- 固定 subject 前缀；OpenID 按 AppID 隔离，不自动升级、降级、合并或通过元数据登录。
- 验证后的 UnionID/OpenID 记录到元数据，缺失保留、矛盾回滚，创建/绑定/pending/刷新共用合并规则；不保存 token、session_key、code、Secret 或原始响应。
- 配置指纹包含应用归属与模式；accountsvc 仅小程序装配、管理员 audience 隔离及错误脱敏同步更新。
- 没有修改 SQL、生成代码、数据库结构、历史标签或冻结源；实现已提交为 `eaf8457`，用户在验收后另行授权归档及提交，未推送。

## 实际验证

全部 Go 命令使用 `GOWORK=off`。数据库验证使用本任务新建、仅绑定回环随机端口的 PostgreSQL 17 与 Redis 7 容器；不使用既有产品资源。Linux 验证镜像为本机已有的 Go 1.26.5 工具链，启用 CGO/race，模块缓存只读挂载且 GOPROXY=off。

| 检查 | 结果 |
|---|---|
| 改动前 TestWeChatVerify 基线 | 通过 |
| 各实现任务 TDD | 新接口/helper 缺失时测试先失败，实现后通过；记录在原始任务报告 |
| `go test ./user/idp -count=1`、`go vet ./user/idp` | 通过 |
| 根包/accountsvc 微信配置与装配定向测试及 vet | 通过 |
| 真实 PostgreSQL 元数据、绑定、pending、并发与指纹定向测试 | 通过 |
| `bash scripts/check-migrations.sh` | 通过，历史保持 |
| 所有改动 Go 文件 `gofmt -l` | 无输出 |
| `git diff --check` | 通过，仅既有行尾策略提示 |
| `openspec validate --all --strict` | 31 项通过，0 失败 |
| Linux 独立 `go build ./...`、`go vet ./...` | 通过 |
| Linux `go test -race -json -count=1 ./...` | 通过：44 个有测试的包、938 个顶层测试通过，0 失败 |
| `go run ./internal/testgate scripts/required-tests.txt` | 通过，所有必需测试实际执行 |

原始输出位于被忽略的 `.test-output/`：`wechat-verifier-report.md`、`wechat-config-report.md`、`wechat-metadata-report.md`、首轮 `wechat-tests-linux.jsonl`、最终 `wechat-tests-linux-final.jsonl`。

## 失败与修正记录

1. 初始 sandbox 无法访问本机 Go 缓存和 npm CLI，通过正常工具提权后运行；没有修改权限策略或绕过门禁。
2. 真实数据库既有 `TestIdentityBindingEndToEndAgainstRealDB` 仍预种裸 UNION-B/C，新的 subject 前缀使登录返回 pending。仅把合成 fixture 改为 `unionid:UNION-B/C`，未放宽断言；Windows 真实数据库定向复验通过，最终 Linux race 对应用例已通过。
3. 首轮 Linux 同时记录 `TestSessionContextConcurrentRevocation/unbind-before-login` 等待超时、`TestServiceIntegrationMetrics` 服务重启迁移失败；发生在临时验证镜像快照期间。没有修改这些用例、超时或迁移逻辑。最终整套在不进行快照的条件下重新执行，两个用例及全部套件通过；快照与超时的因果未单独复现实验，首轮失败原始日志保留，不作为通过证据。

## 独立审查

验证器、元数据各一次任务审查，整组配置/装配/事务/文档再次独立最终审查，均未发现实质缺陷。最终审查包含既有数据库 fixture 修正，报告保留在 `.test-output/wechat-verifier-review.md`、`wechat-metadata-review.md`、`wechat-final-review.md`。审查不替代测试。

## 限制与交付边界

- 微信响应由本地 httptest 模拟，未使用真实微信 AppID/Secret，未做微信 APP 或小程序实机联调。
- 本次没有运行 pg_dump/pg_restore 恢复演练或完整发布入口 `scripts/verify.sh`，不宣称发布验收或在线 CI 已通过。普通套件中 TestRecoveryFixture 的预期 skip 不属于恢复演练通过。
- 历史裸 UnionID 不自动转换；模式切换可能产生不同身份。已在 README、服务说明和兼容指南明确，不能在已有身份数据上无评估切换。
- 已按标签与镜像 ID 核验并删除本任务创建的 PostgreSQL/Redis 测试容器和临时缓存镜像；原始日志保留在 .test-output。

## 归档核验

2026-10-23 按用户指令归档；全部 4 项任务和工件完成。已将 5 条新增要求及全部场景同步到 [wechat-sign-in 主规格](../../../specs/wechat-sign-in/spec.md)，原 Purpose 保留，主规格不含 delta 操作标题。归档同步修复实施计划与验收记录的双向链接；本次仅移动及维护文档，不重新运行 Go 测试，沿用上面的实现验收记录。归档后 `openspec validate --all --strict` 共 31 项通过；规格逐字一致性、相对链接检查及 `git diff --check` 均通过；活动变更列表为空。
