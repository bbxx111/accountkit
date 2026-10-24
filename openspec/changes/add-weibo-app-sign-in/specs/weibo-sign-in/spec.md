# Spec Delta

## Purpose

为微博开放平台移动应用 SDK 登录提供服务端 access_token 校验、应用允许列表、全局 uid 主标识和资料声明获取，确保跨应用账号识别可预测，资料失败不影响已验证身份登录，并保持账号生命周期、令牌隔离与宿主职责边界。

## ADDED Requirements

### Requirement: App SDK 登录与宿主边界
系统 SHALL 接收微博 Android/iOS SDK 登录取得的 access_token，不提供授权发起、网页回调、redirect_uri 或授权码兑换接口，也不要求 AppSecret。客户端/宿主 SHALL 负责集成 SDK 并提交本次取得的令牌；SDK 回调中的 uid、refresh_token 或资料 SHALL 不被采信或接收。文档 SHALL 说明 SDK 接入、uid 公开属性和令牌可重放风险；本变更不交付客户端实现。

#### Scenario: SDK 登录
- **WHEN** App 通过微博 SDK 登录取得 access_token
- **THEN** 向既有第三方登录接口提交 WEIBO 的 app_id/access_token，无需调用 accountkit 的授权发起或回调端点

#### Scenario: 客户端声明的 uid
- **WHEN** 客户端在 SDK 回调后得到 uid 并希望一并提交
- **THEN** 只能提交 app_id 与 access_token，身份标识仅取自服务端校验结果

### Requirement: 严格凭证与应用允许列表
WEIBO credential SHALL 为仅含非空 app_id、access_token 的对象，app_id 为微博 AppKey，遵循既有 16 KiB 上限与严格解码规则。uid、refresh_token、地址或其他未知字段 SHALL 被拒绝。Enabled 检查与应用允许列表检查 SHALL 在外部调用之前执行；未登记 AppKey 返回 IDP_APP_NOT_ALLOWED，字段缺失或未知返回 CREDENTIAL_INCOMPLETE。

#### Scenario: 合法凭证
- **WHEN** 提交 `{"kind":"WEIBO","credential":{"app_id":"1234567890","access_token":"synthetic-token"}}` 且该应用已启用
- **THEN** 使用服务端配置校验令牌，不接受客户端指定接口地址

#### Scenario: 未知字段或未知应用
- **WHEN** credential 含 uid 字段，或者 app_id 未登记
- **THEN** 分别返回 400 CREDENTIAL_INCOMPLETE 或 400 IDP_APP_NOT_ALLOWED，不访问微博，不创建身份或会话

### Requirement: 应用列表与环境变量
系统 SHALL 支持多个独立微博 AppKey，列表为空表示未启用。AppKey SHALL 为 1 至 20 位 ASCII 数字且不重复。本流程 SHALL 不要求 AppSecret 或其他秘密。环境变量 SHALL 使用宿主选择的前缀，后缀为 WEIBO_APP_IDS 和可选 WEIBO_API_BASE_URL；APP_IDS 为逗号分隔的非重复列表。缺省接口地址 SHALL 为 https://api.weibo.com；覆盖值必须是无用户信息、查询参数或 fragment 的 HTTPS 绝对地址，仅本地回环测试地址允许 HTTP。非法或重复配置 SHALL 在启动前拒绝，错误只指出配置项，不输出配置内容；构造 SHALL 不做网络或文件 I/O。

#### Scenario: 两个 App
- **WHEN** 宿主使用 CUSTOM_ 前缀配置两个不同的微博 AppKey
- **THEN** 仅读取 CUSTOM_WEIBO_*，构造成功，两者均可作为允许的令牌归属应用；列表顺序不改变身份策略

#### Scenario: 配置失败
- **WHEN** AppKey 列表含空项、重复项或非数字项，或接口地址为 HTTP 非回环地址
- **THEN** 在启动前失败，不启动部分有效的应用配置，不通过网络调用探测配置

### Requirement: 令牌校验与应用归属
系统 SHALL 以 HTTPS POST 调用 oauth2/get_token_info 校验令牌。成功响应的 appkey SHALL 等于请求的 app_id，否则按应用不允许拒绝；expire_in 为负 SHALL 视为令牌已失效。uid、appkey、expire_in SHALL 接受 JSON 字符串或整数并按十进制文本校验，不得经浮点转换；uid SHALL 为 1 至 20 位 ASCII 数字。请求 SHALL 禁止重定向并有界等待与读取；非 JSON、尾随内容、重复键或其他类型的字段 SHALL 不被视为成功。系统 SHALL 不自动重试，不将微博令牌返回调用方。

#### Scenario: 正常校验
- **WHEN** 响应为合法 JSON，appkey 等于 app_id，expire_in 非负，uid 合法
- **THEN** 生成已验证的微博身份，进入既有账号流程

#### Scenario: 字符串与整数字段
- **WHEN** 一次响应中 uid 与 expire_in 为整数，另一次为数字字符串，取值相同
- **THEN** 两次得到相同的身份 subject，不因类型差异拒绝或产生精度损失

#### Scenario: 其他应用的令牌
- **WHEN** 响应的 appkey 是另一个 AppKey，即使该 AppKey 也已登记
- **THEN** 返回 400 IDP_APP_NOT_ALLOWED，不查询资料，不创建账号、身份、会话或 pending 流程

#### Scenario: 剩余有效期为负
- **WHEN** 响应 expire_in 为负数
- **THEN** 返回 400 IDP_CREDENTIAL_INVALID，不查询资料

#### Scenario: 异常响应
- **WHEN** HTTP 200 响应包含重复的 uid 键、uid 为浮点数或布尔值
- **THEN** 拒绝身份验证，不猜测标识

### Requirement: 令牌可重复使用
微博 access_token SHALL 不做一次性限制，在提供方有效期内可重复用于登录或绑定，与 QQ 及 Facebook Classic 访问令牌一致。系统 SHALL 不为其登记 nonce、不缓存校验结果，每次请求均调用提供方校验。文档 SHALL 说明令牌泄露期间可被用于登录的风险及客户端保密责任。

#### Scenario: 同一令牌两次登录
- **WHEN** 同一有效 access_token 先后两次提交到第三方登录
- **THEN** 两次均按校验结果登录同一身份，不返回重放错误

#### Scenario: 令牌已失效
- **WHEN** 提供方对该令牌返回令牌过期或已撤销错误
- **THEN** 返回凭证无效，不使用此前成功校验的结果

### Requirement: 全局 uid 主标识
微博身份 subject SHALL 为 uid:<uid>，不包含 AppKey，因此同一微博用户经任一已登记应用登录均命中同一身份。系统 SHALL 不提供标识模式配置，不写入 unionid 或按应用的 openid 元数据，不按资料查找或自动合并账号。文档 SHALL 说明 uid 是公开标识，只用于识别，不代表持有者证明。

#### Scenario: 跨 App 同一 uid
- **WHEN** 两个已登记 AppKey 的令牌校验结果具有相同 uid
- **THEN** 两次登录命中同一微博身份与账号；并发首次登录也不得创建两个最终账号

#### Scenario: 不同 uid
- **WHEN** 两次校验得到不同 uid，资料昵称相同
- **THEN** 视为两个不同身份，不自动关联

### Requirement: 策略指纹
配置指纹 SHALL 覆盖排序去重后的 AppKey 集合与规范化接口地址，不含秘密。该配置变化后未完成的登录补全 SHALL 要求重新开始；仅调整列表顺序 SHALL 不使其失效。

#### Scenario: 配置变化
- **WHEN** pending 创建后移除一个 AppKey 或更换接口地址
- **THEN** 旧流程被拒绝

#### Scenario: 列表重排
- **WHEN** pending 创建后仅调整 AppKey 列表顺序
- **THEN** 旧流程仍可完成

### Requirement: 资料查询与映射
每次成功校验微博身份后，系统 SHALL 使用同一令牌与已验证 uid 查询 2/users/show.json，查询在账号事务之前完成。仅无错误字段且返回的 id 与已验证 uid 一致的响应可被采信。screen_name SHALL 映射为 name；picture 依次取 avatar_hd、avatar_large、profile_image_url 中第一个非空值，沿用既有净化规则，仅接受 HTTPS，不做协议改写。系统 SHALL 不读取邮箱、性别、地区、简介等其他字段。

#### Scenario: 取得资料
- **WHEN** 资料响应含 screen_name 与 HTTPS 的 avatar_hd
- **THEN** 将净化后的声明带入既有直接注册、绑定或登录补全流程，按资料规格补填空字段

#### Scenario: 只有小尺寸头像
- **WHEN** 资料响应缺少 avatar_hd 与 avatar_large，但含 HTTPS 的 profile_image_url
- **THEN** 使用 profile_image_url 作为 picture 声明

#### Scenario: HTTP 头像
- **WHEN** 选中的头像字段为 http:// 地址
- **THEN** 丢弃头像声明，不回退到其他头像字段，昵称照常处理，不计为资料降级

#### Scenario: 资料用户不符
- **WHEN** 资料响应的 id 与已验证 uid 不同
- **THEN** 丢弃整份资料并记资料降级，身份登录继续

### Requirement: 资料失败允许登录
资料权限不足、限流、上游错误码、超时或解析失败 SHALL 不推翻已通过的身份校验；不可信资料不得写入。资料查询 SHALL 具有独立的 3 秒时间预算并受调用方更早截止时间约束。调用方上下文取消或超时 SHALL 终止整体操作。资料为空时不得清除已有声明或账号资料；补填遵循 user-basic-profile。

#### Scenario: 资料被限流
- **WHEN** 资料查询返回频率限制错误或 HTTP 429，而身份已校验且外层请求尚未取消
- **THEN** 记录资料降级并继续登录，是否需补全仍按既有 Required 策略决定

#### Scenario: 昵称或头像缺失
- **WHEN** 可信资料响应缺少其中一个字段
- **THEN** 处理存在且合法的字段，不因缺失阻断登录，不计为资料降级

#### Scenario: 用户已取消请求
- **WHEN** 资料查询期间调用方上下文取消
- **THEN** 终止操作，不将取消当作资料降级后继续建立会话

### Requirement: 错误分类与令牌保护
令牌校验中令牌已使用、过期、撤销、拒绝、授权被收回或无效（21314、21315、21316、21317、21319、21327、21332）SHALL 返回 400 IDP_CREDENTIAL_INVALID；应用权限受限、需要更高授权或未审核应用用户数超限（10005、10014、21321）以及成功响应缺少必需字段 SHALL 作为提供方配置错误返回固定消息 500；网络错误、3xx、429/5xx、超时、其他上游错误码或无法解析的响应 SHALL 返回 503 IDP_UNAVAILABLE。access_token、完整上游响应与资料原文 SHALL 不进入日志、审计或错误响应；令牌只用于本次校验内存流程。

#### Scenario: 令牌过期
- **WHEN** 令牌校验返回 21327
- **THEN** 返回 400 IDP_CREDENTIAL_INVALID，不误映射为消费者 Bearer 401

#### Scenario: 未审核应用超限
- **WHEN** 令牌校验返回 21321
- **THEN** 返回固定消息 500，响应与日志不含令牌或上游原文

#### Scenario: 上游未知错误
- **WHEN** 提供方返回未登记的错误码
- **THEN** 返回 503 IDP_UNAVAILABLE

### Requirement: 领域与持久化边界
WEIBO SHALL 复用既有身份策略、注册补全、绑定解绑、生命周期和会话来源规则，不能用于验证码登录、重新认证、换绑或登录补全的补充证明等锚点端点。仅校验凭证不得提前保存账号资料；资料、身份、会话写入 SHALL 随领域事务提交或回滚。pending 只保存现有加密证明中已净化的标识和资料，不保存微博令牌；完成、失败或重放不得再次调用上游。无需数据库结构变化或改写迁移。

#### Scenario: 登录进入补全
- **WHEN** 首次微博身份不足以满足 Required
- **THEN** 返回既有 pending，不提前创建用户；资料随加密证明暂存，完成时才填充目标账号的空字段

#### Scenario: 失败与重放
- **WHEN** 身份占用、账号状态拒绝、事务失败或已完成流程重放
- **THEN** 不留下该次资料补填或部分会话写入，保持既有错误及重放行为

### Requirement: 库侧自动验收与宿主联调边界
库侧验收 SHALL 使用正式根门面与 HTTP handler、实际微博提供方、本地模拟接口及真实 PostgreSQL/Redis，覆盖从 HTTP 凭证到账号、资料与刷新会话的闭环及多应用同一 uid。真实 SDK 令牌下 get_token_info 的字段类型与 appkey 取值、users/show 对新应用的权限与限流、头像地址协议 SHALL 作为宿主接入检查清单。验收记录 SHALL 区分模拟契约与真实平台行为，不把未执行的联调标为通过。

#### Scenario: 无终端设备的库验收
- **WHEN** HTTP 契约测试使用真实存储通过，其他库门禁也通过，而无手机或微博账号
- **THEN** 可以完成库侧验收，宿主真机清单仍记为未执行

#### Scenario: 头像协议未经真实验证
- **WHEN** 头像只经模拟接口测试
- **THEN** 验收记录标明真实返回的头像协议与可显示性未验证
