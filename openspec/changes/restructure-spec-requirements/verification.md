# 规格结构整理验收

本次为纯文档维护，基线提交 bf51078。七份主规格的11条超长正文已拆分，新增33条需求承接移出的规范性规则；原需求标题、原场景及产品行为保持。对应认证delta同步调整，真实仓库未执行归档、推送或发布。

## 规则迁移对应

| 主规格／原需求 | 移出的规则由以下新需求承接 |
|---|---|
| accountsvc-runtime／服务邮件模板清单 | 服务邮件默认清单、清单语言结构、用途完整性与禁止回落、内置版式、占位符校验与转义、文字与清单上限、清单失败与日志隐私 |
| verification-delivery／真实 SMTP 邮件投递 | SMTP 构造与中文渲染兼容、独立服务多语言邮件装配 |
| accountsvc-service-authentication／认证挑战 | 无效 Bearer 的认证挑战、服务权限与依赖错误的响应头 |
| accountsvc-service-authentication／服务令牌配置校验 | 服务签发方地址与环境隔离、服务 audience 配置隔离 |
| consumer-token-introspection／独立服务调用认证 | 内省未配置的优先响应、内省生产调用链传输保护、内省调用认证错误协议 |
| purged-user-feed／游标分页拉取 | 清理游标校验与恢复、清理拉取的缓存与重复读取 |
| purged-user-feed／服务鉴权与暴露边界 | 清理拉取认证错误协议、清理拉取未配置的优先响应、清理拉取内部暴露与凭据轮换 |
| embedded-auth-package／OAuth 端点请求体编码 | OAuth 表单参数边界、OAuth 编码路径响应一致、刷新 scope 参数兼容、OAuth 表单支持范围 |
| embedded-auth-package／独立消费与 v1.0.0 公开能力 | 开发期公开符号迁移、可选当前会话查询与描述格式 |
| embedded-auth-package／公开覆盖登记与正式名称 | 正式公开入口清理、构造器渲染器与缓存正式名称、IdP 构造与校验阶段兼容、私有历史审计快照边界 |
| versioned-auth-storage／当前初始定义 | 初始结构校验与回退定义、初始迁移冻结与后续升级 |

每条原需求保留其主要行为及所有原场景，其他规则原文搬移。邮件默认清单的“未设置或为空”条件由旧分组标题明确写入新正文；签发方与audience拆分后分别保留监听前失败、变量名提示及不回显值，避免“任一规则”范围收窄。

## 检查结果

- OpenSpec 1.14.3：真实仓库 `openspec validate --all --strict` 退出0，46通过、0失败；原7份失败已消除。
- 自动结构审计：九份修改的主／增量规格中，334个原场景条目的标题和正文原样保留；所有原Requirement标题保留，未涉及的原需求块保持；新增33条，未发现重复标题或协议标识丢失。增量规则标识按当前主规格与delta共同构成的有效上下文核对。
- 独立语义审查覆盖全部diff，三组均通过。发现并修复一处“上述六个已移除符号”指代：将完整六符号清单前移，保留原场景正文；定向复核无剩余必修项。
- 隔离副本根目录经CLI确认位于 `.test-output/spec-restructure/simulation-final`。仅在该副本使用官方 `archive harden-accountsvc-authentication --yes --json` 应用delta，新增8、修改12、无删除／重命名；随后全库strict退出0，46通过、0失败。此为验证未来规格合并，不代表真实认证变更已归档。
- 认证delta只缩短三个已有MODIFIED需求；scope、机器主体及default/rfc9068差异保持。拆出的公共规则已存在于主规格，无需重复ADDED，因此未来归档不会覆盖回超长正文。
- 差异及引用检查通过；未修改Go、SQL、迁移、测试断言、校验器或500字符限制。纯文档整理未重跑Go、数据库、race或部署验收。

原始映射、差异、结构审计和CLI日志均保留在被忽略的 `.test-output/spec-restructure/`：`mail-report.md`、`service-report.md`、`library-report.md`、`review-report.md`、`structure-audit.json`、`validate-all.log`、`archive-simulation-final.log`、`validate-simulation-final.log`。本记录是提交范围内的汇总。

## 保留的历史表述问题

这些问题在拆分前已存在，本次不裁决行为或修改原场景，也未核对实际实现：

1. versioned-auth-storage 的初始表清单与“空数据库安装”场景中的“完整八表”计数不一致。
2. accountsvc-runtime 的“邮件语言选择”要求未配置清单时不读取Accept-Language，而“未配置清单”场景要求按Accept-Language: en选择英文；需另行核对默认嵌入清单的语言选择契约。

后续若处理上述问题，应分别核对实现、既有测试和原设计，不能仅凭结构校验通过判断语义已一致。实施范围见 [设计](design.md) 和 [计划](../../../docs/superpowers/plans/2026-11-15-restructure-spec-requirements.md)。
