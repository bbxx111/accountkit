# 规格结构整理验收

本次为纯文档维护。结构整理阶段以bf51078为基线，七份主规格的11条超长正文已拆分，新增33条需求承接移出的规范性规则；该阶段原需求标题、原场景及产品行为保持。对应认证delta同步调整。用户随后要求修正两处已记录的历史表述，依据与结果见末节；此前实施阶段未执行归档或推送；本轮按用户明确授权完成正式归档并安排提交、推送，未发布。

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

## 结构整理阶段发现的历史表述问题

以下问题在拆分前已存在，结构整理阶段仅记录；用户后续明确要求直接修正，现已按末节依据澄清：

1. versioned-auth-storage 的初始表清单与“空数据库安装”场景中的“完整八表”计数不一致。
2. accountsvc-runtime 的“邮件语言选择”要求未配置清单时不读取Accept-Language，而“未配置清单”场景要求按Accept-Language: en选择英文；需另行核对默认嵌入清单的语言选择契约。

实施范围见 [设计](design.md) 和 [计划](../../../../docs/superpowers/plans/2026-11-15-restructure-spec-requirements.md)。

## 后续表述修正依据与验收

- 邮件：`internal/accountsvc/email_templates.go` 的emailRenderer在未配置文件时返回嵌入默认清单；`run.go`按EMAIL启用及实际渲染器Multilingual决定是否安装消费者语言中间件。`TestDefaultManifest`确认默认清单含zh-Hans/en/zh-Hant并按请求偏好渲染；单语言测试确认忽略偏好。原邮件模板变更的design.md D5/D6及verification.md第二轮也明确此行为。主规格现按实际生效清单判断，删除“未配置清单不读取”的错误条件，补默认和单语言场景；未改运行行为。
- 表清单：0001 up显式创建8张数据表，down显式删除同样8张；`migrations.go`把schema_migrations交给迁移引擎。既有`TestConsolidatedInitialSchema`明确排除该版本表后断言8。修正规格为8张数据表加引擎版本表，并澄清0001 down不删除版本表；没有把表数误改为9张数据表，也没有修改SQL。
- 冻结文件SHA256仍为：up `ef2d1f229fa15b660cc7ecb4f417615b76866c19494641c96faf25bab24188b6`；down `812541721471025056edd1f655d876b2f4d938fffd8f4f7ee2cfa9a97ff490d5`，与initialFrozen一致。
- 本轮实际运行：`GOWORK=off go test -count=1 ./internal/accountsvc/emailtemplates -run 'TestDefaultManifest|TestLanguageMatching|TestSingleLanguageIgnoresPreference'`及`go test -count=1 ./internal/accountsvc -run 'TestConfigEmailTemplates|TestAcceptLanguageMiddleware'`，均退出0；全库strict为46通过、0失败。
- 本轮不重跑真实数据库、SMTP进程或race；表数结论来自冻结SQL和既有迁移测试的源码核对，不宣称新执行了数据库迁移测试。原归档记录保留；原334场景逐字比对属于前一结构整理阶段，本轮对上述表述和关联场景的修正是用户明确授权的后续工作。
- 本轮独立复核通过：邮件条件与实际装配一致，数据表与版本表责任清晰，down定义未改变运行时禁止破坏性回退的契约。差异检查通过，只有规格和对应记录发生变化。

## 正式归档收尾（2026-11-15）

本变更为skip_specs的纯文档维护，无独立增量规格需同步。已与认证变更一并归档到本目录，保留所有实施与检查记录，并修复归档后的相对链接及计划引用。此前隔离副本模拟记录仍为历史证据；本节确认此次操作发生于真实仓库。未修改代码、SQL或测试。

归档收尾验证：活动变更列表为空，主规格全量strict为45通过、0失败；16个归档原文件及7个本地链接核对通过（两份Markdown仅有Git换行格式差异）。独立归档复核通过；本轮未修改或重跑Go/数据库/race实现验证。原始同步计划、链接审计和校验日志保留于 .test-output/archive-auth-specs/。
