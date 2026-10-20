## Context

上一固定版本为 v0.3.0（`ba2d0412764c659beabdbd0d3f0f0f9bb9007657`）。本次准备基线为本地 develop 的 `2d55d8e12f859b3598b62bdab73e79e87d244eb5`，比远端 develop `924c4f0` 多出已归档的 `harden-database-runtime` 合并，尚未推送。v0.3.0 之后的最终运行时变更为：

- `add-session-authentication-context`、`consolidate-session-context-initial-schema`、`simplify-session-context`：会话必有认证来源、解绑/换绑按来源撤销、当前会话查询、单份嵌套客户端描述，结构直接纳入未发布 0001。
- 数据库设计评审（`0157ffb`，经用户明确要求未建 change）：0001 索引调整、锚点 subject 跨摘要版本 advisory lock、导入排序加锁、审计查询改写。
- `harden-database-runtime`：内部领域事务 READ COMMITTED、连接池最低容量 2、审计窄候选分页。

发布说明按最终行为整理，不能把开发过程中的 known 标记、INITIAL/LATEST 双快照、平坦 HTTP client_info 或撤回的会话 0002 描述为对外发布内容；但已跟随开发版接入的调用者需要删除这些引用的提示仍须保留。

## Goals / Non-Goals

目标：准备 v0.4.0 说明、当前代码的验证证据及明确的发布门禁。非目标：新增功能、替集成方验收真实环境、自动迁移旧数据库、产品部署、正式发布或自动归档。

## Decisions

- 版本号建议 v0.4.0：存在旧库拒绝、解绑语义、直接 Go 解绑调用、连接池容量及公开结构新增字段等破坏性变化，v0 阶段以 minor 递增表达，不暗示向后兼容。最终版本号以用户确认为准。
- 在独立 worktree 的 `docs/prepare-v0-4-0-release` 分支准备，主工作区当前检出 main 且保持不动。
- 完整 Git 历史与全部 tag 通过 bundle 复制进 Linux 验证容器，checkout 到上述精确快照，使用专用 PostgreSQL 17、Redis 7 和三个独立空库；同时执行 `verify.sh` 与 `verify-accountsvc.sh`。该次验证覆盖数据库设计评审代码，补齐其此前没有绑定 SHA 的验收。
- 长期文档中“开发中”标记、README 发布入口与 OpenAPI `info.version` 在正式发布记录阶段统一更新，避免在 tag 前宣称已发布；发布说明状态写明“准备中，未发布”。
- `internal/migrationcheck` 无需改动：v0.4.0 tag 的 0001 与当前固定哈希一致，按普通已发布历史检查。发布后若再改动 0001，须先把 v0.4.0 像 v0.3.0 一样单独固定，本次不预先添加。
- 所有实际结果与未验证项集中在 verification.md；docs 只维护说明和流程入口。纯文档维护不启动功能 apply，不新增重复行为测试。

## Risks / Trade-offs

- 当前 v1 与 v0.3.0 v1 版本号相同但结构不同，且 v0.3.0 的 Migrate 不检查会话上下文，可能接受新结构；发布说明必须突出禁止混跑与未经核验回退。
- 0001 索引调整不在基线预检范围内，开发期已有同号库保留旧索引、缺少 `uk_session_user_id_device_id` 兜底；发布说明须要求新部署使用新空资源。
- 完整本地门禁只证明记录的快照；最终 main 提交、在线 CI、远端固定版本消费和真实产品环境需要各自证据。
- 本地 develop 未推送；正式发布前须推送并在在线 CI 上核验最终 main 候选。
