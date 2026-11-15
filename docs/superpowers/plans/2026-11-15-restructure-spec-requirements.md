# 规格结构整理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 修复七份主规格的11条正文长度问题，保留全部语义并避免未归档delta覆盖回旧结构。
**Architecture:** 纯文档结构维护，按三个不共享文件的组并行；原标题/场景不变，新需求承接移出的规范句。旧认证delta只做对应结构调整。
**Tech Stack:** Markdown、OpenSpec1.14.3。
**Spec:** [设计](../../../openspec/changes/restructure-spec-requirements/design.md)。

## Global Constraints
- 不改代码、SQL、测试或产品行为；不执行真实归档/推送/发布。
- 保留原标题和全部原场景正文；原阈值、错误码、API名字、条件和否定约束均有去处。
- 仅处理CLI明确报告的11条，不按包含标题的粗略字符计数扩大范围。

## Review Focus
- 邮件转义和不回退约束，输入上限和日志隐私不可丢失。
- 认证401/403/503、challenge及no-store与未配置优先级不可混淆。
- pending认证change与主规格的行为差异必须保留，不能提前同步或回退default/scope/sub。
- API改名清单、旧格式例外和冻结迁移哈希责任不可简化。
- 所有原场景保留；临时副本合并后的结果仍须strict通过。

### Task 1: 邮件
- [x] 编辑 openspec/specs/accountsvc-runtime/spec.md 的“服务邮件模板清单”及 verification-delivery/spec.md 的“真实 SMTP 邮件投递”；读取完整文件后拆分，输出旧→新规则映射。
- [x] 对两份主规格运行openspec validate --type spec --strict；独立审查语义保持。

### Task 2: 服务认证
- [x] 编辑主规格 accountsvc-service-authentication（认证挑战、服务令牌配置校验）、consumer-token-introspection（独立服务调用认证）、purged-user-feed（游标分页拉取、服务鉴权与暴露边界），同步 harden-accountsvc-authentication 前两份关联delta。
- [x] 核对原场景和所有错误/权限/传输规则，逐规格strict；独立审查增量合并语义。

### Task 3: 库与存储
- [x] 编辑 embedded-auth-package 的OAuth端点请求体编码、独立消费与v1.0.0公开能力、公开覆盖登记与正式名称，以及 versioned-auth-storage 的当前初始定义；输出完整规则映射。
- [x] 逐规格strict并独立核对公开标识符、冻结初始迁移和历史支持边界。

### Task 4: 综合验收
- [x] 核对11个原需求和全部原场景保留，全部文件差异为文档，检查引用和重复标题。
- [x] 使用隔离副本模拟应用认证delta，检验归档后的潜在结果，不改变真实仓库归档状态。
- [x] 运行全库strict和diff检查，写verification.md记录；纯文档无需Go/数据库/race。

### Task 5: 后续明确授权的表述修正
- [x] 对照emailRenderer、Multilingual装配、默认清单测试及原设计，明确内置默认清单也参与语言协商；只修正规格，不改Go。
- [x] 核对0001的8个CREATE/DROP和迁移引擎版本表配置，明确8张数据表加schema_migrations，保留冻结哈希。
- [x] 运行既有邮件定向测试及全库strict，独立复核并记录结果后提交；此步骤明确修正先前保留的错误表述，不沿用结构阶段的原场景逐字不变要求。
