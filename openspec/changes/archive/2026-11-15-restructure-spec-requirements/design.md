# Design

## Context

见 proposal.md。起点为 bf51078，主规格与 harden-accountsvc-authentication 尚未归档的增量规格并存；其中认证增量的 MODIFIED 块会在归档时替换同名主需求。本次仅修复严格校验报告的11个正文长度问题，不顺带改写其他规则。

## Goals / Non-Goals

**Goals:** 一条需求表达一个可核验行为；保留所有规范性约束、原标题及原场景；同步关联delta后仍可正确归档合并；所有规格严格校验通过。

**Non-Goals:** 不修改认证语义、角色/scope/主体规则、迁移SQL、任何Go或测试代码；不修复未经确认的历史语义问题；不执行归档、推送或发布。

## Decisions

- 直接整理现有主规格，使用skip_specs记录这是纯文档维护，不制造新的产品行为delta。新增标题是原约束的结构分解，不是新增功能。
- 保留原Requirement标题及全部原Scenario正文。独立规范句移到紧邻的新Requirement并提供对应可核验场景，必要的例子移入场景；保留数字、协议字段、错误码、条件分支和SHALL强度。
- 不选压缩术语/删细节/放宽500字符限制；不把通用约束仅藏进某个特例场景，不借机去重或改行为。
- 三组文件独立编辑：邮件（runtime、verification-delivery）、服务认证（service-authentication、introspection、purged-user-feed及两个相关认证delta）、库与存储（embedded-auth-package、versioned-auth-storage）。每组记录旧需求到新需求的映射与约束核对。
- 对已有认证delta：缩短原MODIFIED块并保留它的全部场景；若新拆分主需求的内容与该变更目标不同，使用同名MODIFIED而非ADDED；相同则无需重复。保证认证变更的主体/scope/default profile语义不丢失，也不提前同步到主规格。
- 在隔离临时副本模拟应用认证delta，再执行严格校验，验证未来合并不会重新引入长段落或重复标题。真实仓库不执行归档。

## Risks / Trade-offs

- 拆分丢失否定条件或例外 → 逐句映射审查，保留原场景，核对全部阈值/错误码/字段。
- 未归档delta覆盖主规格 → 同步相关块并模拟应用后校验。
- 重组诱发额外修复 → 仅记录历史矛盾，不在文档维护中改变含义。

## Migration Plan

无产品迁移或部署步骤。只验证文档差异、引用、场景及OpenSpec；无需为纯文档整理重跑Go/数据库/race。

## Follow-up Corrections

用户随后明确要求直接修正两处已记录的历史表述。依据现有实现、测试与邮件原设计：默认嵌入清单同样参与多语言协商；0001仅创建八张数据表，schema_migrations由引擎另行维护。此后续步骤修正规格文字及相关场景，区别于前一结构整理阶段的原场景保留检查；不改运行行为或冻结SQL。
