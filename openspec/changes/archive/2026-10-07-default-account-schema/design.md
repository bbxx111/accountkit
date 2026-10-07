## Context

范围与用户决定见 [proposal](proposal.md)。默认值在 Config.applyDefaults 集中应用，环境装配和 accountsvc 沿用库配置；迁移 SQL 使用非限定表名，无需改变 SQL。

## Goals / Non-Goals

**Goals:** 默认 account 与当前操作文档一致；显式 schema 仍可配置。

**Non-Goals:** 不搬迁任何数据库对象，不加入旧 schema 探测/兼容分支，不改变 Redis 前缀、环境变量名或发布 tag。

## Decisions

只修改集中默认值，更新当前默认值断言、Compose 的明确 schema 和操作示例。显式 auth/auth_staging 等自定义测试保留，证明支持任意合法 schema。历史归档、完成的计划、固定基线和 v0.1.0 发布记录保持历史事实；当前 README 说明开发代码的默认值与已发布版本不同。

真实数据库回归新增默认配置迁移用例，验证 search_path、五个包表、版本记录及重复 Migrate，确保没有创建 auth。只在一次性测试库运行，若 account 已存在则失败且不得清理它；只清理本用例创建的对象。当前默认值变更不修改迁移器或引入新的 SQL 版本。

## Risks / Trade-offs

- [不加限定的旧名替换误改协议或历史] → 按默认值、当前示例和显式配置分类，只修改前两类。
- [验证误删既有 schema] → 用例先确认目标不存在，创建后才登记清理；控制器只提供本次一次性数据库。
- [只看单元默认值而遗漏实际落库] → 运行真实 PostgreSQL 默认迁移与既有隔离回归。

## Migration Plan

用户确认没有实际使用 v0.1.0，不安排数据迁移。保留既有显式 Schema 配置能力；本次仅提交到任务分支，归档、合并和下一版本发布分别执行。
