# 恢复 users 资源命名 Implementation Plan

> **For agentic workers:** 使用 superpowers:subagent-driven-development；公开 HTTP 行为通过 TDD 验证，完成后独立审查。

**Goal:** 恢复 `/users` 路由和 `users/...` 资源名，使 HTTP 与现有 User/user_id 领域命名一致，保留身份策略及登录补全实现。

**Architecture:** 本轮仅调整 HTTP 路由、DTO 资源引用、调用者和文档。领域模型、认证事务、数据库、配置语义和令牌均沿用当前实现。

**Tech Stack:** Go 1.26.5、Chi、PostgreSQL 17、Redis、OpenSpec spec-driven。

**Spec:** [当前变更](../../../openspec/changes/add-identity-policy-sign-in-completion/proposal.md)及对应 design/specs/tasks；这是对 2026-10-14 计划中 accounts 命名决策的修订。

## Global Constraints

- 在现有 `feat/identity-policy-sign-in-completion` 分支继续，起点 afbdc23。用户已确认继续恢复 users 并同步实现、提案、文档和测试。
- 两面保持独立，宿主前缀 `/v1`、`/admin/v1` 不变；账号集合使用复数 users。两个新增端点为 `/users:sendSignInCompletionCode`、`/users:completeSignIn`。
- 此前未发布的 `/accounts` 路径不作为别名或重定向；列表 JSON users、Go User/UserID/Auth.Users、数据库 user_account/user_id、JWT/作用域不改。
- 禁止把 accountsvc、accountkit、默认 account schema、ImportAccounts 领域 API 或外部参考 URL 当作路由字符串批量替换。
- 不改领域认证逻辑、SQL、生成文件、迁移及冻结源基线。历史发布/归档记录保留，前次验收只作为历史结果。
- 真实微信/Apple 外部联调按用户已有选择仍未验证；不重新索取凭据。只勾选本轮实际验证的受影响任务。

## Review Focus

核对所有两面路由/name/引用、两个补全端点、网关允许清单、users 列表字段与旧 accounts 路径 404。检查是否误改服务/模块标识、外部文档 URL、历史记录或领域行为；重命名测试必须同步必需门禁。

## Task 1: 工件与文档同步

**Files:** 当前 change 的 proposal/design/specs/tasks（verification 由协调者写）；README、AGENTS、相关长期 docs 和原身份策略计划。

1. 更新原 accounts 迁移决策为沿用 users，并同步所有现有增量规格；保留其余已确认要求。
2. 改写兼容说明，去掉 users→accounts 的发布要求；更新新端点、资源名和网关示例。
3. 重开受影响的 7.1、7.2、7.6、8.1、8.2、8.4、8.6，8.5 继续未勾选，其余保持已完成。
4. 原实现计划明确本次修订，保持当前规划一致；不改更早计划、归档或发布记录。
5. 检查引用/用词和 OpenSpec 严格校验；向实现者交接最终契约。

## Task 2: HTTP 和调用者恢复

**Files:** httpapi/enduser、httpapi/admin 中路由/资源 DTO 与测试；根集成测试；internal/accountsvc 的服务/网关测试；必要的脚本测试名登记。

1. 先把既有 HTTP 正向断言改为 users，并把不支持路径断言改为 accounts，运行相关 HTTP 测试，确认现有实现 RED。
2. 恢复两面全部规范路由与资源 name，更新代码注释，不新增别名；保持所有认证中间件和 DTO 字段。
3. 同步根/服务/网关调用者，保留新增补全、pending、自动关联、错误分类与 no-store 断言。
4. 若测试重命名，同步 required-tests；不删除门禁、不放宽 assertions。
5. gofmt，执行完整 HTTP 测试和相关 vet；核对剩余 accounts 只来自明确的负向案例、历史或外部参考。

## Task 3: 审查与验收

1. 独立审查 Task 1/2 的契约与实际差异，处理实质问题。
2. 用本轮独立临时 PG17/Redis 和 Linux 验证镜像运行 GOWORK=off 完整 verify.sh，覆盖 build/vet/race、必需门禁及恢复；不以旧结果或 skip 代替。
3. 执行 openspec validate --all --strict、路径/引用和 diff 检查，将当前证据写入 change/verification.md，保留历史证据上下文。
4. 验证后恢复受影响任务勾选，8.5 仍未验证；提交本次改动并保留当前分支，清理本次创建的验证资源。

## Execution Dependencies

文档与测试准备可并行，生产路由修改以 Task 1 明确的最终 users 契约为准。两实施者写入范围互不重叠。协调者维护本计划、verification 和测试环境；最后统一审查与验证。
