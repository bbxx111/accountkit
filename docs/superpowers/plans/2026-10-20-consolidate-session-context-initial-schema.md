# 会话初始结构整合实施计划

**Goal:** 按用户明确要求将未发布 0002 合入当前 0001，并保持历史证据和拒绝旧库的完整性。

**Spec:** [本次设计](../../../openspec/changes/consolidate-session-context-initial-schema/design.md)及其 versioned-auth-storage delta。

**Execution:** 当前会话顺序实施，各步骤共享迁移和测试文件；完成后由独立代理审查。不自动提交、合并、发布或归档。

## Global Constraints

- 新安装 version=1；历史 tag、冻结源不变；原会话 v2 不自动降版本或接管。
- 不修改认证业务；不新增外键；不手改生成代码；只使用任务专属数据库。

## Review Focus

- v0.3.0 tag 被移动或旧 SQL 被未来 tag 复用必须拒绝。
- 撤回 v2 的完整结构不能因与新 v1 相同而被自动接受。
- dirty 优先，旧库拒绝必须保留 catalog/数据并释放锁。
- 合并前后列序、CHECK 和索引必须一致。
- 测试注入的下一版本不等同于撤回的生产 v2。

## Tasks

- [x] 更新 TestConsolidatedInitialSchema/默认版本断言；新增旧 v0.3.0 和原会话 v2 无写拒绝、完整 catalog 对比；先运行确认 RED。
- [x] 合并 SQL，保留标记列的原末尾位置；下迁移覆盖七表，删除生产 0002，测试探针回到 0002；预检对 v1 要求完整上下文。
- [x] internal/migrationcheck 为 v0.3.0 增加精确提交及旧哈希，不复用原 source-baseline；测试移动标签、错误历史和未来旧定义，当前哈希同步。
- [x] sqlc generate；更正文档当前升级路径和 AGENTS 有限授权，保留原发布/归档事实。
- [x] 运行 check-generated/check-migrations、完整 Linux verify.sh/verify-accountsvc.sh（含真实恢复）；独立审查与严格 OpenSpec 核验，记录实际证据后勾选任务。
