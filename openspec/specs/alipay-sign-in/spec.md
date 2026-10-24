# alipay-sign-in Specification

## Purpose

为 App 极简版支付宝授权提供统一的服务端凭证验证、固定身份识别模式和资料声明获取，确保多应用账号关联可预测，资料失败不影响已验证身份登录，并保持账号生命周期、秘密隔离与宿主职责边界。

## Requirements

### Requirement: 库侧自动验收与宿主联调边界
库侧验收 SHALL 使用正式根门面与 HTTP handler、实际支付宝提供方、独立签名网关及真实 PostgreSQL/Redis，覆盖从 HTTP 凭证到账号、资料与刷新会话的闭环，以及多应用固定模式。真实 SDK、安装/取消/回跳 state、实际授权权限与控制台分组 SHALL 作为宿主接入检查清单，不作为库 CI 或实施完成的设备前提。验收记录 SHALL 区分模拟协议契约与真实平台行为，不把未执行的宿主联调标为通过。

#### Scenario: 无终端设备的库验收
- **WHEN** 两项 HTTP 契约测试使用真实存储实际通过，其他库门禁也通过，而无手机或支付宝账号
- **THEN** 可以完成库侧验收，宿主真机清单仍记为未执行，不宣称平台实际兼容性已验证

#### Scenario: 多应用协议声明
- **WHEN** 独立签名网关给两个 AppID 返回相同 UnionID
- **THEN** 验证库命中单账号、单身份与合并 OpenID 的规则；真实支付宝分组是否返回相同值仍由宿主核对

### Requirement: App 极简授权与宿主边界
系统 SHALL 接收 App 极简版授权取得的凭证，不提供授权发起、签名 authInfo 或浏览器回调接口。客户端/宿主 SHALL 配置极简版 SDK 回跳，以 auth_user 请求资料授权，将不可预测的一次性 state 关联本次授权并核对回调。文档 SHALL 说明需要安装支付宝客户端以及 Flutter 桥接要求；本变更不交付客户端实现或 PC Web 验收。

#### Scenario: 极简版登录
- **WHEN** App 完成授权并核对回调上下文
- **THEN** 向既有第三方登录接口提交 ALIPAY 的 app_id/code，无需先调用 accountkit 生成 authInfo

#### Scenario: 客户端取消或回调不匹配
- **WHEN** 用户取消授权或回调 state 不匹配本次授权
- **THEN** 客户端/宿主结束该次流程，不把结果提交为成功登录；文档不得宣称仅靠后端兑换 code 就完成了回调关联校验

### Requirement: 严格凭证与应用允许列表
ALIPAY credential SHALL 为仅含非空 app_id、code 的对象，遵循既有 16 KiB 上限与严格解码规则。模式、用户标识、第三方 token 或未知字段 SHALL 被拒绝。Enabled 检查与应用允许列表检查 SHALL 在外部调用之前执行，未知 AppID 返回既有 IDP_APP_NOT_ALLOWED，格式缺失返回 CREDENTIAL_INCOMPLETE。

#### Scenario: 合法凭证
- **WHEN** 提交 `{"kind":"ALIPAY","credential":{"app_id":"2026000000000001","code":"synthetic-code"}}` 且该应用已启用
- **THEN** 使用该应用的服务端配置兑换授权码，不接受客户端指定网关、密钥或标识模式

#### Scenario: 未知字段或未知应用
- **WHEN** credential 含 union_id 字段，或者 app_id 未登记
- **THEN** 分别返回 400 CREDENTIAL_INCOMPLETE 或 400 IDP_APP_NOT_ALLOWED，不访问支付宝、不创建身份或会话

### Requirement: 多应用普通公钥配置
系统 SHALL 支持多个独立 AppID，每项具有应用 RSA 私钥和支付宝 RSA 公钥，首版只支持普通公钥模式。配置列表为空表示未启用。AppID SHALL 为 16 位 ASCII 数字且不重复；密钥 SHALL 为可解析的 PEM RSA 密钥且至少 2048 位，私钥支持 PKCS#1/PKCS#8，公钥支持 PKCS#1/SPKI。缺失、非法配置或证书输入 SHALL 在启动前拒绝，错误只指出配置项，不输出配置内容。构造 SHALL 不做网络或文件 I/O。

#### Scenario: 两个 App
- **WHEN** 配置两个不同 AppID 及其各自的私钥、公钥
- **THEN** 构造成功，请求分别使用所属应用的签名与验签材料

#### Scenario: 配置失败
- **WHEN** 存在重复 AppID、缺失公钥、非 RSA 密钥、弱密钥或证书 PEM
- **THEN** 在启动前失败，不启动部分有效的应用配置，不回显密钥内容

### Requirement: 环境变量与固定模式
环境变量 SHALL 使用宿主选择的前缀，后缀为 ALIPAY_APP_IDS、ALIPAY_<APPID>_PRIVATE_KEY、ALIPAY_<APPID>_PUBLIC_KEY、ALIPAY_SUBJECT_MODE 和可选 ALIPAY_GATEWAY_URL。APP_IDS SHALL 为逗号分隔的非重复列表。SUBJECT_MODE 缺省为 UNIONID，显式仅接受 UNIONID 或 OPENID；空白、小写或 AUTO SHALL 拒绝。缺省网关 SHALL 为 https://openapi.alipay.com/gateway.do；覆盖值必须是无用户信息、查询参数或 fragment 的 HTTPS 绝对地址，仅本地回环测试地址允许 HTTP。

#### Scenario: 前缀隔离
- **WHEN** 宿主使用 CUSTOM_ 前缀配置两个支付宝应用
- **THEN** 仅读取 CUSTOM_ALIPAY_*，不硬编码 ACCOUNTKIT_ 或 SERVER_；列表顺序不改变身份策略

#### Scenario: 不安全网关或模式
- **WHEN** 配置 HTTP 非回环网关，或模式为 AUTO
- **THEN** 启动前拒绝，不通过网络调用再探测配置是否可用

### Requirement: 授权码兑换与响应可信性
系统 SHALL 以 RSA2 签名 POST 表单调用 alipay.system.oauth.token，使用 grant_type=authorization_code 兑换 code。请求 SHALL 禁止重定向并有界等待与读取。成功身份响应 SHALL 按支付宝原始响应节点字节验签后才可采信；缺签名、坏签名、重复或歧义 JSON 字段、错误节点混用、畸形响应不得作为成功。系统 SHALL 不自动重试可能已消费的授权码，不将支付宝令牌返回调用方。

#### Scenario: 正常兑换
- **WHEN** 兑换响应签名有效、结构合法且具有模式要求的标识
- **THEN** 生成已验证的 ALIPAY 身份，进入既有账号流程

#### Scenario: 伪造或歧义响应
- **WHEN** HTTP 200 响应未签名、签名错误或包含重复的身份字段
- **THEN** 拒绝身份验证，不查询资料、不创建账号、身份、会话或 pending 流程

#### Scenario: 换码网络中断
- **WHEN** 请求已发出但读取响应超时
- **THEN** 返回依赖不可用，不自动重复兑换；客户端通过新的授权动作获取新 code

### Requirement: 固定主标识与应用分组
UNIONID 模式 SHALL 使用 unionid:<union_id>；OPENID 模式 SHALL 使用 openid:<app_id>:<open_id>。必需标识缺失 SHALL 作为提供方配置/响应错误拒绝认证，沿用固定消息的 500。系统 SHALL 不在模式间回退，不使用 user_id 兜底，不按元数据查找登录或自动合并账号。文档 SHALL 要求跨 App 共用账号的应用属于同一支付宝应用分组，且实际返回相同 union_id。

#### Scenario: 跨 App 同一 UnionID
- **WHEN** UNIONID 模式下两个已登记 AppID 的可信响应具有相同 union_id
- **THEN** 两次登录命中同一 ALIPAY 身份与账号；并发首次登录也不得创建两个最终账号

#### Scenario: OpenID 按应用隔离
- **WHEN** OPENID 模式下两个 AppID 返回相同 open_id 文本
- **THEN** subject 不同，不自动关联；后续出现 union_id 也不改变 subject

#### Scenario: UnionID 缺失
- **WHEN** UNIONID 模式收到有 open_id 但无 union_id 的可信响应
- **THEN** 返回配置/响应错误，不创建 OPENID 身份

### Requirement: 标识元数据与策略指纹
已验证的非空 union_id SHALL 保存为 provider_meta.unionid，open_id SHALL 按 AppID 保存于 provider_meta.openids；缺失不清除旧值，矛盾标识拒绝并回滚。配置指纹 SHALL 覆盖排序后的应用集合、模式、网关以及公钥材料指纹，不含私钥；相同公钥的 PEM 表达变化不改变指纹。该配置变化后未完成登录补全 SHALL 要求重新开始。

#### Scenario: 已有标识冲突
- **WHEN** 同一 subject 已存的 UnionID 或同 AppID OpenID 与新可信结果矛盾
- **THEN** 拒绝本次领域操作，既有元数据及资料保持不变，不创建会话

#### Scenario: 验签配置改变
- **WHEN** pending 创建后更换支付宝公钥或标识模式
- **THEN** 旧流程被拒绝；仅调整应用列表顺序或同一公钥 PEM 换行不使其失效

### Requirement: 资料查询与映射
每次成功验证支付宝身份后，系统 SHALL 尝试使用本次访问令牌查询 alipay.user.info.share，查询在账号事务之前完成且不重新消费授权码。仅采信签名有效且 open_id 与换码响应同 AppID OpenID 一致的资料；缺少可比对标识时不采信资料。只将 nick_name、avatar 映射为 name、picture，沿用既有净化规则；不得用真实姓名、display_name、手机号或邮箱兜底。

#### Scenario: 首次取得资料
- **WHEN** 换码和资料验签成功，OpenID 一致且 nick_name/avatar 有效
- **THEN** 将净化后的声明带入既有直接注册、绑定或登录补全流程，按资料规格填充空字段

#### Scenario: 资料身份不一致
- **WHEN** 资料响应的 open_id 与换码响应不一致或缺失
- **THEN** 丢弃整份资料并记录安全分类告警，仍可凭已经验证的主标识继续登录，不用资料改变 subject

### Requirement: 资料失败允许登录
资料缺失、权限不足、上游错误、超时、解析或验签失败 SHALL 不推翻已通过的身份验证；不可信资料不得写入。资料查询 SHALL 具有独立的 3 秒时间预算并受调用方更早截止时间约束。调用方上下文已取消或超时 SHALL 终止整体操作，不能吞掉取消继续写库。资料为空时不得清除已有声明或账号资料；后续补填遵循 user-basic-profile。

#### Scenario: 资料超时但请求仍有效
- **WHEN** 资料查询达到独立超时，而身份已验证且外层请求尚未取消
- **THEN** 记录资料降级并继续登录，账号是否需补全仍按既有 Required 策略决定

#### Scenario: 昵称或头像未设置
- **WHEN** 可信资料响应缺少其中一个字段
- **THEN** 处理存在且合法的字段，不因缺失阻断登录，不记录为上游故障

#### Scenario: 用户已取消请求
- **WHEN** 资料查询期间调用方上下文取消
- **THEN** 终止操作，不将取消当作资料降级后继续建立会话

### Requirement: 错误分类与秘密保护
无效、过期或已用授权码 SHALL 使用既有凭证无效错误；依赖网络、429/5xx、超时使用 503 IDP_UNAVAILABLE；服务端凭据配置错误或无法安全归类的协议异常使用固定消息 500。未经签名验证的错误体不得被用作用户凭证无效证据。code、支付宝令牌、私钥、签名、完整上游响应和资料不得进入日志、审计或错误响应；第三方令牌只用于本次验证内存流程。

#### Scenario: 已签名的无效授权码错误
- **WHEN** 上游明确返回且验签通过的无效授权码错误
- **THEN** 返回 400 IDP_CREDENTIAL_INVALID，不误映射为消费者 Bearer 401

#### Scenario: 未签名错误体
- **WHEN** 网关返回 HTTP 200 的未签名错误体或无法归类的异常节点
- **THEN** 不建立身份且返回固定内部错误，不将其归为用户凭证无效，不回显原文

### Requirement: 领域与持久化边界
支付宝 SHALL 复用既有身份策略、注册补全、绑定解绑、生命周期和会话来源规则。仅验证凭证不得提前保存账号资料；资料、身份、会话相关写入 SHALL 随领域事务提交或回滚。pending 只保存现有加密证明中已净化的标识和资料，不保存第三方令牌；完成、失败或重放不得再次调用上游补资料。无需数据库结构变化或改写迁移。

#### Scenario: 登录进入补全
- **WHEN** 首次支付宝身份不足以满足 Required
- **THEN** 返回既有 pending，不提前创建用户；资料随加密证明暂存，完成时才填充目标账号的空字段

#### Scenario: 失败与重放
- **WHEN** 身份占用、账号状态拒绝、元数据冲突、事务失败或已完成流程重放
- **THEN** 不留下该次资料补填或部分会话写入，保持既有错误及重放行为
