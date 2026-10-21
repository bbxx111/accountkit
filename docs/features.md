# 功能清单与候选功能

更新日期：2026-10-21。Go 集成与 HTTP 文档面向已发布的 v0.5.0；标准化指标实现与验证状态见[库指标验收](../openspec/changes/archive/2026-10-19-add-standardized-metrics/verification.md)，accountsvc 采集端点见[服务指标验收](../openspec/changes/archive/2026-10-19-add-accountsvc-prometheus/verification.md)。版本交付状态以[v0.5.0 发布说明](releases/v0.5.0.md)为准。

本文统一记录 v0.5.0 已支持能力、已实现但待真实环境验证的项目，以及尚未立项的候选功能。版本发布不表示产品环境已验收；历史版本不包含本表全部能力。目前尚无实际产品接入，正式部署暂不安排。

协议和配置细节以 [README](../README.md)、[主规格](../openspec/specs)及公开 Go 类型为准；调用升级见[兼容指南](compatibility.md)，独立服务边界见[accountsvc 手册](accountsvc.md)。

## 已支持功能

| 领域 | 当前能力 | 边界与说明 |
|---|---|---|
| 登录与注册 | 手机号、邮箱验证码登录；微信、Apple 登录；满足身份策略时自动注册 | 短信和邮件由发送器投递；第三方验证需要对应应用配置 |
| 身份策略 | EnabledIdentityKinds 控制可用类型；RequiredIdentityKinds 控制账号完整性 | Required 仅 PHONE/EMAIL、any-of；显式空列表允许纯第三方账号；配置在装配时固定，不热加载 |
| 登录补全 | 身份不足时返回 pending；独立发码与完成端点；验证后自动关联已有活动账号或注册；受约束幂等重试 | pending 尚无 user_id、identity、session 或 token pair；补全只接受已启用的手机/邮箱证明 |
| 身份管理 | 身份列表、手机/邮箱/第三方绑定、解绑、同类手机或邮箱换绑 | 解绑保护至少一个可用身份和 Required 条件；普通绑定拒绝占用身份；换绑保留当前会话并撤销其他会话 |
| 令牌与会话 | JWT access、refresh 轮换、宽限重试、重放处理、同设备会话替换、会话列表和撤销 | 宽限内返回同一 token pair；可撤销单个、其他或管理端全部会话；业务仍需检查 scope 和资源归属 |
| 敏感操作认证 | 手机/邮箱重新认证、近期认证检查；第三方通过正常重新登录取得新的认证状态 | 手机/邮箱重新认证不更新 refresh；第三方重新登录替换同设备会话，客户端须保存新 pair 并核对账号 |
| 资料与生命周期 | 显示名与头像更新、可配置资料属性及本人 `GET/PATCH /users/me/profile`、批量公开资料、冻结/解冻、注销冷静期、恢复、到期匿名化 | 手机/邮箱与管理员恢复保留；普通第三方登录不能恢复冷静期账号；资料属性按宿主 Schema，头像只保存 HTTPS 地址；最终匿名化后不可恢复 |
| 管理接口 | 用户筛选与游标分页、账号状态操作、身份掩码/授权查看、会话撤销、审计查询 | 消费者与管理员身份体系独立；管理员验证由宿主提供，未配置时管理面返回 503 |
| 安全与审计 | 验证码轮次和用途隔离、发送额度、跨轮次失败预算、身份加密/摘要、密钥轮换、异步脱敏审计 | Required 不是 MFA；不同依赖操作保留各自故障策略，详见 [Redis 契约](../README.md#redis-键与失败模式) |
| 标准化指标 | 可选类型化 Observer、OTel/原生 Prometheus、认证/验证码/依赖/降级/审计/维护的 13 项指标；accountsvc 可选 Prometheus 采集端点 | 默认 no-op；嵌入宿主负责 Provider/Registry、导出与端点，见[指标指南](metrics.md)；accountsvc 默认关闭指标，启用后提供同址 GET /metrics，部署方落实采集访问控制，见[服务配置](accountsvc.md#prometheus-指标)；合成示例不替代真实联调 |
| 宿主事务集成 | 注销前检查、同库业务匿名化回调、活动账号事务保护、受控离线导入 | 导入仅 Go 接口，不发令牌或自动合并；业务表和业务授权仍由宿主负责 |
| 存储与维护 | PostgreSQL + Redis、schema/键前缀/令牌域隔离、版本迁移、迁移锁、dirty 阻断、定期清理、备份恢复验证 | 新库由 0001 直接定义 sign_in_completion_flow，初始化版本为 1；旧表名 v1、双表和已撤回旧 v2 拒绝接管，见[迁移手册](migrations.md#当前初始基线)；公开 Down 拒绝执行 |
| 集成与运行形式 | 嵌入式 Go 包；可选 accountsvc，提供 SMTP、TLS、远程令牌内省、外部管理员 OIDC 验证、探针和优雅关闭 | accountsvc 尚未装配短信服务商；网关、入口流量控制和实际网络隔离由部署方负责 |
| Go 接入文档与示例 | 集成指南、生命周期、token/pending 分流、刷新与重新认证、调用层授权、事务回调和错误处理 | [指南](go-integration.md)及[可编译片段](../examples/gointegration/example_test.go)；无 Output 片段不执行依赖 I/O，合成输出不替代产品联调 |
| HTTP 契约文档 | 消费者与管理面分别提供 OpenAPI 3.1，覆盖现有 24/11 个操作 | [契约与维护说明](openapi.md)；不包含 accountsvc 专属内省/探针，不代表已有完整客户端 SDK |

登录补全使用 `POST /users:sendSignInCompletionCode` 和 `POST /users:completeSignIn`。它可以将首次出现、尚无账号归属的第三方身份关联到已有账号；这不等于将两个已经存在的账号合并。

主要能力说明：[身份策略与登录补全](../README.md#身份策略与登录补全)、[身份绑定](../README.md#身份绑定阶段-4b)、[手机/邮箱换绑](../README.md#手机号与邮箱换绑)、[宿主事务契约](../README.md#宿主账号事务)、[服务运行](accountsvc.md)。

## 已实现但待真实环境验证

这些是已有实现的验证缺口，不是尚未实现的新功能。

| 项目 | 已有证据 | 待补验证 |
|---|---|---|
| 微信 / Apple | 本地提供方夹具、协议和数据库集成测试 | 真实应用上的既有登录、普通绑定、首次补全关联与注册；目前尚无测试应用配置 |
| SMTP 邮件 | 本地 SMTP/TLS 夹具和邮件验证码闭环 | 实际服务商认证、证书、投递和最终收件 |
| 外部管理员 OIDC | 本地 HTTPS discovery/JWKS、签名、角色和换钥测试 | 实际管理员提供方的 audience、角色映射和换钥行为 |

实际验证状态以各次变更的 verification.md 为准：[身份策略与登录补全验收](../openspec/changes/archive/2026-10-15-add-identity-policy-sign-in-completion/verification.md)、[初始结构整合验收](../openspec/changes/archive/2026-10-15-consolidate-initial-schema/verification.md)。其他真实服务、网关和产品环境验收边界见 [accountsvc 验证边界](accountsvc.md#验证边界)及[发布与上线检查表](release-checklist.md)。本文不重复维护测试统计或验收报告。

## 候选功能

以下均为待评估候选，不代表已确认需求、排期或实施任务。建议顺序用于后续讨论；立项后通过 OpenSpec 明确契约、范围和验收标准。

| 候选功能 | 要解决的问题与现有差距 | 建议顺序 |
|---|---|---|
| 完整 HTTP 客户端调用示例 | 已提供 Go 集成片段和两套 OpenAPI；仍可按目标客户端补齐真实交互流程、并发刷新协调与安全凭证存储 | 有目标客户端后 |
| HTTP 客户端 SDK | 为 HTTP 调用方统一 token/pending 解析、令牌更新和错误处理；不替代已有 Go 库 | 调用示例稳定且有目标客户端后 |
| accountsvc 短信发送器 | 库支持注入短信发送器，但独立服务尚不能启用 PHONE；补齐短信渠道的配置与真实服务商适配 | 采用手机号登录时优先 |
| 纯第三方账号自助恢复 | Required=[] 允许纯第三方注册，但只有第三方身份的账号在注销冷静期内尚无自助恢复路径，需管理员代办；需要设计对应的控制权证明与恢复授权 | 中，先确认安全与产品规则 |
| 更多登录与认证方式 | 按目标产品需要评估更多第三方提供方、Passkey、MFA；现有身份完整性策略不提供多因素认证 | 按需分别立项 |
| 两个已有账号合并 | 首次身份关联已经支持；合并两个已有 user_id 还需要处理业务数据归属、冲突、授权及审计 | 有明确产品需求后 |

Go 集成指南与内置 HTTP 契约已补充，实际验证与限制见[本轮记录](../openspec/changes/archive/2026-10-18-document-go-integration-and-http-contracts/verification.md)。后续根据目标客户端完善完整调用示例，并根据手机号需求决定短信适配。其余候选不作为当前版本的未完成任务。

## 维护方式

- 已有能力或边界变化时，更新对应行、实现基准和相关规格链接；区分开发代码与已发布版本。
- 真实联调完成后，先在对应变更的 verification.md 记录证据，再更新本文验证状态，不以模拟夹具替代实际结果。
- 候选确认立项时，补充对应 OpenSpec 变更链接；只有实现并验证后才移入“已支持功能”。
- 发布、部署和候选功能立项分别处理；正式部署恢复安排时，再使用发布与上线检查表。
