# qq-sign-in Specification

## Purpose

为 QQ 互联移动应用 SDK 登录提供服务端 access_token 校验、应用允许列表、固定身份识别模式和资料声明获取，确保多应用账号关联可预测，资料失败不影响已验证身份登录，并保持账号生命周期、令牌隔离与宿主职责边界。

## Requirements

### Requirement: App SDK 登录与宿主边界
系统 SHALL 接收 QQ 互联 Android/iOS SDK 登录取得的 access_token，不提供授权发起、网页回调、redirect_uri 或授权码兑换接口。客户端/宿主 SHALL 负责集成 SDK 并提交本次取得的令牌；SDK 回调中的 openid、unionid 或资料 SHALL 不被采信。文档 SHALL 说明 SDK 接入、UnionID 申请和令牌可重放风险；本变更不交付客户端实现。

#### Scenario: SDK 登录
- **WHEN** App 通过 QQ SDK 登录取得 access_token
- **THEN** 向既有第三方登录接口提交 QQ 的 app_id/access_token，无需调用 accountkit 的授权发起或回调端点

#### Scenario: 客户端声明的标识
- **WHEN** 客户端在 SDK 回调后自行得到 openid 并希望一并提交
- **THEN** 只能提交 app_id 与 access_token，身份标识仅取自服务端校验结果

### Requirement: 严格凭证与应用允许列表
QQ credential SHALL 为仅含非空 app_id、access_token 的对象，遵循既有 16 KiB 上限与严格解码规则。openid、unionid、模式、地址或未知字段 SHALL 被拒绝。Enabled 检查与应用允许列表检查 SHALL 在外部调用之前执行；未登记 AppID 返回 IDP_APP_NOT_ALLOWED，字段缺失或未知返回 CREDENTIAL_INCOMPLETE。

#### Scenario: 合法凭证
- **WHEN** 提交 `{"kind":"QQ","credential":{"app_id":"1100000001","access_token":"synthetic-token"}}` 且该应用已启用
- **THEN** 使用服务端配置校验令牌，不接受客户端指定接口地址或标识模式

#### Scenario: 未知字段或未知应用
- **WHEN** credential 含 openid 字段，或者 app_id 未登记
- **THEN** 分别返回 400 CREDENTIAL_INCOMPLETE 或 400 IDP_APP_NOT_ALLOWED，不访问 QQ，不创建身份或会话

### Requirement: 应用列表配置
系统 SHALL 支持多个独立 QQ AppID，列表为空表示未启用。AppID SHALL 为 1 至 20 位 ASCII 数字且不重复。本流程 SHALL 不要求 AppKey 或其他秘密。非法或重复配置 SHALL 在启动前拒绝，错误只指出配置项，不输出配置内容；构造 SHALL 不做网络或文件 I/O。

#### Scenario: 两个 App
- **WHEN** 配置两个不同的 QQ AppID
- **THEN** 构造成功，两者均可作为允许的令牌受众

#### Scenario: 配置失败
- **WHEN** AppID 列表含空项、重复项或非数字 AppID
- **THEN** 在启动前失败，不启动部分有效的应用配置

### Requirement: 环境变量与固定模式
环境变量 SHALL 使用宿主选择的前缀，后缀为 QQ_APP_IDS、QQ_SUBJECT_MODE 和可选 QQ_API_BASE_URL。APP_IDS SHALL 为逗号分隔的非重复列表。SUBJECT_MODE 缺省为 UNIONID，显式仅接受 UNIONID 或 OPENID；空白、小写或 AUTO SHALL 拒绝。缺省接口地址 SHALL 为 https://graph.qq.com；覆盖值必须是无用户信息、查询参数或 fragment 的 HTTPS 绝对地址，仅本地回环测试地址允许 HTTP。

#### Scenario: 前缀隔离
- **WHEN** 宿主使用 CUSTOM_ 前缀配置两个 QQ 应用
- **THEN** 仅读取 CUSTOM_QQ_*，不硬编码 ACCOUNTKIT_ 或 SERVER_；列表顺序不改变身份策略

#### Scenario: 不安全地址或模式
- **WHEN** 配置 HTTP 非回环接口地址，或模式为 AUTO
- **THEN** 启动前拒绝，不通过网络调用探测配置

### Requirement: 令牌校验与应用归属
系统 SHALL 以 HTTPS GET 调用 oauth2.0/me 并要求 JSON 格式响应，UNIONID 模式请求 unionid，OPENID 模式不请求。成功响应的 client_id SHALL 等于请求的 app_id，否则按应用不允许拒绝。请求 SHALL 禁止重定向并有界等待与读取；JSONP 包裹、尾随内容、重复或错误类型的字段 SHALL 不被视为成功。系统 SHALL 不自动重试，不将 QQ 令牌返回调用方。

#### Scenario: 正常校验
- **WHEN** 响应为合法 JSON，client_id 等于 app_id，且具有模式要求的标识
- **THEN** 生成已验证的 QQ 身份，进入既有账号流程

#### Scenario: 其他应用的令牌
- **WHEN** 响应的 client_id 是另一个 AppID，即使该 AppID 也已登记
- **THEN** 返回 400 IDP_APP_NOT_ALLOWED，不查询资料，不创建账号、身份、会话或 pending 流程

#### Scenario: 非 JSON 响应
- **WHEN** HTTP 200 响应为 JSONP 包裹或包含重复的 openid 字段
- **THEN** 拒绝身份验证，不按字符串切分猜测标识

### Requirement: 令牌可重复使用
QQ access_token SHALL 不做一次性限制，在提供方有效期内可重复用于登录或绑定，与 Facebook Classic 访问令牌一致。系统 SHALL 不为其登记 nonce、不缓存校验结果，每次请求均调用提供方校验。文档 SHALL 说明令牌泄露期间可被用于登录的风险及客户端保密责任。

#### Scenario: 同一令牌两次登录
- **WHEN** 同一有效 access_token 先后两次提交到第三方登录
- **THEN** 两次均按校验结果登录同一身份，不返回重放错误

#### Scenario: 令牌已失效
- **WHEN** 提供方对该令牌返回校验失败
- **THEN** 返回凭证无效，不使用此前成功校验的结果

### Requirement: 固定主标识与跨应用打通
UNIONID 模式 SHALL 使用 unionid:<unionid>；OPENID 模式 SHALL 使用 openid:<app_id>:<openid>。必需标识缺失或未开通 UnionID 权限 SHALL 作为提供方配置错误拒绝认证，沿用固定消息的 500。系统 SHALL 不在模式间回退，不按元数据查找登录或自动合并账号。文档 SHALL 要求跨 App 共用账号的应用在同一 QQ 互联开发者账号下完成 UnionID 打通。

#### Scenario: 跨 App 同一 UnionID
- **WHEN** UNIONID 模式下两个已登记 AppID 的校验结果具有相同 unionid
- **THEN** 两次登录命中同一 QQ 身份与账号；并发首次登录也不得创建两个最终账号

#### Scenario: OpenID 按应用隔离
- **WHEN** OPENID 模式下两个 AppID 返回相同 openid 文本
- **THEN** subject 不同，不自动关联

#### Scenario: 未开通 UnionID
- **WHEN** UNIONID 模式下提供方返回未开通 UnionID 权限的错误，或成功响应缺少 unionid
- **THEN** 返回固定消息 500，不创建 OPENID 身份；OPENID 模式不受该权限影响

### Requirement: 标识元数据与策略指纹
已验证的非空 unionid SHALL 保存为 provider_meta.unionid，openid SHALL 按 AppID 保存于 provider_meta.openids；缺失不清除旧值，矛盾标识拒绝并回滚。配置指纹 SHALL 覆盖排序后的应用集合、模式与接口地址。该配置变化后未完成的登录补全 SHALL 要求重新开始。

#### Scenario: 已有标识冲突
- **WHEN** 同一 subject 已存的 UnionID 或同 AppID OpenID 与新校验结果矛盾
- **THEN** 拒绝本次领域操作，既有元数据及资料保持不变，不创建会话

#### Scenario: 配置变化
- **WHEN** pending 创建后更换标识模式或移除一个 AppID
- **THEN** 旧流程被拒绝；仅调整应用列表顺序不使其失效

### Requirement: 资料查询与映射
每次成功校验 QQ 身份后，系统 SHALL 使用同一令牌、AppID 与已验证 openid 查询 user/get_user_info，查询在账号事务之前完成。仅 ret 为 0 且 is_lost 不为 1 的响应可被采信。nickname SHALL 映射为 name；picture 优先取 figureurl_qq_2，缺失时取 figureurl_qq_1，不使用 QQ 空间头像字段，沿用既有净化规则。系统 SHALL 不读取性别、地区、会员等其他字段。

#### Scenario: 取得资料
- **WHEN** 资料响应 ret 为 0，含 nickname 与 figureurl_qq_2
- **THEN** 将净化后的声明带入既有直接注册、绑定或登录补全流程，按资料规格补填空字段

#### Scenario: 只有小尺寸头像
- **WHEN** 资料响应缺少 figureurl_qq_2 但含 figureurl_qq_1
- **THEN** 使用 figureurl_qq_1 作为 picture 声明

#### Scenario: 数据可能丢失
- **WHEN** 资料响应 is_lost 为 1
- **THEN** 丢弃整份资料，身份登录继续

### Requirement: 头像地址改写
QQ 头像声明为 HTTPS 时 SHALL 原样进入净化。为 HTTP 时，仅当主机精确属于固定的 QQ 头像域名名单且不含端口与用户信息，SHALL 把协议改为 HTTPS 后再净化；其他 HTTP 地址 SHALL 丢弃头像声明，不影响昵称与登录。该改写 SHALL 仅作用于 QQ 提供方，通用头像地址规则保持只接受 HTTPS。

#### Scenario: 名单内 HTTP 头像
- **WHEN** figureurl_qq_2 为名单内主机的 http:// 地址
- **THEN** picture 声明为对应的 https:// 地址并通过既有净化

#### Scenario: 名单外 HTTP 头像
- **WHEN** 头像为 http://example.com/a.png 或名单内主机带端口
- **THEN** 丢弃头像声明，昵称照常处理，不计为资料降级

### Requirement: 资料失败允许登录
资料权限不足、ret 非 0、is_lost、上游错误、超时或解析失败 SHALL 不推翻已通过的身份校验；不可信资料不得写入。资料查询 SHALL 具有独立的 3 秒时间预算并受调用方更早截止时间约束。调用方上下文取消或超时 SHALL 终止整体操作。资料为空时不得清除已有声明或账号资料；补填遵循 user-basic-profile。

#### Scenario: 资料超时但请求仍有效
- **WHEN** 资料查询达到独立超时，而身份已校验且外层请求尚未取消
- **THEN** 记录资料降级并继续登录，是否需补全仍按既有 Required 策略决定

#### Scenario: 昵称或头像缺失
- **WHEN** 可信资料响应缺少其中一个字段
- **THEN** 处理存在且合法的字段，不因缺失阻断登录，不计为资料降级

#### Scenario: 用户已取消请求
- **WHEN** 资料查询期间调用方上下文取消
- **THEN** 终止操作，不将取消当作资料降级后继续建立会话

### Requirement: 错误分类与令牌保护
令牌无效、过期或已撤销 SHALL 返回凭证无效；网络错误、429/5xx、超时、未知上游错误码或无法解析的响应 SHALL 返回 503 IDP_UNAVAILABLE；未开通 UnionID 或成功响应缺少必需字段 SHALL 返回固定消息 500。access_token、完整上游响应与资料原文 SHALL 不进入日志、审计或错误响应；令牌只用于本次校验内存流程。

#### Scenario: 令牌校验失败
- **WHEN** 提供方返回令牌校验失败错误
- **THEN** 返回 400 IDP_CREDENTIAL_INVALID，不误映射为消费者 Bearer 401

#### Scenario: 上游未知错误
- **WHEN** 提供方返回未登记的错误码
- **THEN** 返回 503 IDP_UNAVAILABLE，响应与日志不含令牌或上游原文

### Requirement: 领域与持久化边界
QQ SHALL 复用既有身份策略、注册补全、绑定解绑、生命周期和会话来源规则。仅校验凭证不得提前保存账号资料；资料、身份、会话写入 SHALL 随领域事务提交或回滚。pending 只保存现有加密证明中已净化的标识和资料，不保存 QQ 令牌；完成、失败或重放不得再次调用上游。无需数据库结构变化或改写迁移。

#### Scenario: 登录进入补全
- **WHEN** 首次 QQ 身份不足以满足 Required
- **THEN** 返回既有 pending，不提前创建用户；资料随加密证明暂存，完成时才填充目标账号的空字段

#### Scenario: 失败与重放
- **WHEN** 身份占用、账号状态拒绝、元数据冲突、事务失败或已完成流程重放
- **THEN** 不留下该次资料补填或部分会话写入，保持既有错误及重放行为

### Requirement: 库侧自动验收与宿主联调边界
库侧验收 SHALL 使用正式根门面与 HTTP handler、实际 QQ 提供方、本地模拟接口及真实 PostgreSQL/Redis，覆盖从 HTTP 凭证到账号、资料与刷新会话的闭环及多应用固定模式。真实 SDK 登录、UnionID 打通返回和头像域名 HTTPS 可用性 SHALL 作为宿主接入检查清单。验收记录 SHALL 区分模拟契约与真实平台行为，不把未执行的联调标为通过。

#### Scenario: 无终端设备的库验收
- **WHEN** HTTP 契约测试使用真实存储通过，其他库门禁也通过，而无手机或 QQ 账号
- **THEN** 可以完成库侧验收，宿主真机清单仍记为未执行

#### Scenario: 头像域名未经真实验证
- **WHEN** 头像域名名单只经模拟接口测试
- **THEN** 验收记录标明真实 HTTPS 可用性未验证，不宣称 QQ 头像在生产可显示
