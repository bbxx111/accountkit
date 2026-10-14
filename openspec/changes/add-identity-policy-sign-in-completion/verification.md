## Scope

本轮按用户已确认的简化方案更新既有规划工件，未修改产品代码、主规格、SQL 或部署配置。变更为 add-identity-policy-sign-in-completion，任务分支为 docs/identity-policy-sign-in-proposal，未提交、推送或归档。

## Planning Review

- 采用 accounts 作为 C 端和管理端账号 HTTP 资源，name 与子资源引用同步迁移；user_id、u_…、JWT、scope、Go 门面及数据库命名不随 HTTP 改名改变。
- 仅新增 POST /accounts:sendSignInCompletionCode 和 POST /accounts:completeSignIn，保留独立发码，不复用普通登录发码切换用途。
- 补全请求和 pending 响应统一使用 sign_in_completion_token，Go 字段为 SignInCompletionToken；补全只接受手机或邮箱验证码。
- RequiredIdentityKinds 非空时仅 PHONE/EMAIL，any-of；显式 [] 保留不限定类型语义，普通绑定仍允许 Enabled 中的类型。
- 移除可选延后注册、第三方补全证明和新的第三方重新认证/恢复能力；现有手机/邮箱重新认证与消费者/管理员恢复保留，第三方近期认证通过正常重新登录。
- 自动分流、无账号 pending 与已有账号 user:bind 的区别保持；普通绑定不合并已有账号；默认值、用途隔离、锁序、幂等、加密和异步审计边界已对照检查。
- 更新只涉及原有工件，未新增 capability 或推进实施阶段。所有实施任务保持未完成。

## Current Checks

- proposal、design、specs、tasks 均存在且规划完成；8 个 capability 对应 8 份规格增量，35 项实施任务全部未勾选。
- `openspec validate --all --strict --json`：退出码 0，16/16 通过（本变更 1 项、主规格 15 项），无 issues。[本轮严格校验日志](../../../.test-output/openspec/identity-policy-simplified-validate.json)。
- 本轮 12 个既有文件同步更新，未新增规划工件；旧票据/参数/prepare 标识、活动 users 端点、缺失链接、占位符和行尾空白检查均无问题；MODIFIED requirements 的原有场景全部保留。[本轮工件检查日志](../../../.test-output/openspec/identity-policy-simplified-review.json)。
- `git diff --check` 退出码 0；未跟踪工件另做了内容检查。Git 状态仅显示当前任务分支下的提案目录，已跟踪产品文件无修改。
- 上述结果仅证明本轮规划工件通过检查，不代表产品实现、外部 IdP 或目标部署已经验证。

## Previous Planning Snapshot

首次提案曾完成 8 份规格增量和 39 项未执行任务，OpenSpec 16/16 通过。原始记录保留在 [初版严格校验日志](../../../.test-output/openspec/identity-policy-validate.json) 与 [初版工件检查日志](../../../.test-output/openspec/identity-policy-artifacts-check.json)。该快照包含后来撤回的规划，不作为本轮验收证据。

## Not Verified

- 尚未实施；未运行 Go build/vet/test、sqlc、数据库/Redis 并发、race 或备份恢复演练。本轮仅检查规划。
- accounts 路由迁移和新补全接口的实际行为、真实微信/Apple 登录与普通绑定、目标网络及 SMTP 联调均待实施验收。
- 新增第三方 prepare、重新认证和恢复流程已不在本次范围，不列作待实施项。
