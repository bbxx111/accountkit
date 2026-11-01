# accountkit-flutter-sdk Specification

## Purpose

为 Flutter 消费应用提供 accountkit 官方账号客户端，统一消费者 HTTP 契约、凭据与登录流程生命周期以及可复用的业务请求认证行为。应用无需复制账号协议或令牌刷新逻辑即可通过独立配置接入 accountsvc 或兼容的嵌入式宿主，并保留界面与平台授权的自主装配。

## Requirements

### Requirement: 消费者 HTTP 能力覆盖与地址配置

SDK SHALL 覆盖正式基线的消费者登录及补全、令牌刷新与撤销、本人基础/扩展资料、身份绑定/解绑/换绑、重新认证、当前与其他会话、账号注销与恢复操作，并维护逐端点覆盖表。服务地址和挂载前缀 SHALL 可配置，默认前缀为 /v1；可选宿主能力未配置时 SHALL 暴露实际错误，不伪造空结果或成功。SDK SHALL 不提供内部内省、purge-feed、管理员或数据库接口。

#### Scenario: 独立账号服务与嵌入式宿主
- **WHEN** 应用分别配置默认挂载的 accountsvc 和在其他前缀挂载相同 HTTP 面的宿主
- **THEN** 同一账号操作使用对应配置生成请求，保留协议资源名和身份边界，不拼接硬编码产品地址

#### Scenario: 可选能力未配置
- **WHEN** 宿主对当前会话查询或换绑返回对应的 503 未配置错误
- **THEN** SDK 返回包含原 reason 的类型化错误，不返回空会话或声称换绑成功

### Requirement: 凭证与响应模型忠于正式协议

SDK SHALL 使用 kind/credential 信封，支持正式内置提供方凭证及显式指定合法类型名的扩展提供方凭证；平台原生授权由调用方完成。PHONE/EMAIL SHALL 携带 target、code_id、code。SDK SHALL 保留未知身份类型的原字符串，不能因新增提供方破坏已有身份列表。响应新增字段 SHALL 可忽略，畸形必填结构 SHALL 返回协议错误；缺失、null、空字符串的不同含义 SHALL 保留。本人基础资料 SHALL 支持显示名与头像地址的部分更新；扩展资料 SHALL 按服务端补丁语义发送，不私自合并或补默认值。

#### Scenario: 扩展身份类型
- **WHEN** 应用提交合法的自定义第三方类型及凭证，或读取含该类型的身份列表
- **THEN** 请求按统一信封发送，响应保留类型值，不调用特定平台 SDK、不把未知类型改写为其他提供方

#### Scenario: 部分资料更新
- **WHEN** 应用只修改 display_name，随后显式把 avatar_url 设置为空字符串
- **THEN** 第一个请求不发送 avatar_url，第二个请求明确清空头像；扩展属性的 null 按协议删除语义传递

### Requirement: 唯一会话状态与凭据持久化

同一 SDK 实例的账号操作和业务请求 SHALL 共用一套凭据状态、刷新协调和串行持久化。只有持久化成功的 token 结果 SHALL 被报告为登录成功。SDK SHALL 从响应及存储的 scope 推导 SignedOut、NeedsBinding、PendingDeletion、SignedIn，不通过解码 JWT 推导；无法识别的 scope SHALL 报告协议错误，不能默认为完整登录。重新认证返回的新 access SHALL 保留当前 refresh，不覆盖并发刷新产生的新凭据。登出、切换账号或新登录 SHALL 使旧流程失效，旧结果不得写入或清理新会话。

#### Scenario: 登录结果晚于账号切换
- **WHEN** 账号 A 的旧登录或刷新响应在账号 B 已建立会话后返回
- **THEN** 旧响应被丢弃，B 的凭据和登录态不变

#### Scenario: 存储失败
- **WHEN** 服务端登录成功但安全存储写入失败
- **THEN** SDK 报告可识别的存储失败，不发布已成功持久化的登录态，不输出凭据内容

#### Scenario: 重新认证与刷新交错
- **WHEN** 重新认证结果返回前发生刷新轮换
- **THEN** SDK 按会话和操作版本拒绝或协调过时结果，不恢复旧 refresh 或以旧 access 覆盖更新结果

### Requirement: 有界刷新与错误分类

SDK SHALL 在 access 接近过期时协调一次刷新，并合并同一会话的并发刷新。符合认证重试契约的请求收到 401 后 SHALL 最多刷新一次并在允许重放时重试一次；重放后确认仍为认证失效 SHALL 结束该会话，不形成循环。刷新返回 invalid_grant SHALL 清理原会话并区分冻结原因；网络、超时、429、503 SHALL 保留凭据并暴露故障。普通 403 INSUFFICIENT_SCOPE、角色拒绝和 400 REAUTHENTICATION_REQUIRED SHALL 不触发自动刷新或登出。绑定或恢复成功后的显式 scope 同步 SHALL 与普通错误处理分开，不重发已完成的操作。

#### Scenario: 并发业务请求需要刷新
- **WHEN** 同一会话的多个请求同时发现 access 过期
- **THEN** 只发起一次刷新，所有仍有效的请求使用已保存的新凭据，取消的请求不继续发送

#### Scenario: 依赖故障与权限不足
- **WHEN** 刷新返回 503，或普通请求返回 403 INSUFFICIENT_SCOPE
- **THEN** 前者保留凭据并报告依赖故障，后者直接报告 scope 不足；两者均不自动登出或无限重试

#### Scenario: 绑定已成功但同步失败
- **WHEN** 身份绑定已成功而随后的 scope 刷新遇到网络故障
- **THEN** 保留绑定成功事实，单独暴露会话同步状态，不能把结果包装为绑定失败并自动重发绑定

### Requirement: 登录补全为有界临时流程

SDK SHALL 将 token 与 COMPLETE_IDENTITY pending 解析为互斥结果。pending SHALL 仅存在内存，不是第五种登录态，不进入 token 存储、URL或可打印凭据。发码与补全 SHALL 使用原流程设备快照，不注入 Bearer、不触发刷新；取消、到期、切换输入或账号后 SHALL 阻止旧响应推进流程。补全期限 SHALL 不因重试延长。未知 next_action 或无可执行补全渠道 SHALL 返回可恢复协议错误。结果未知的网络/5xx故障 SHALL 只允许用户在原有效流程内手动重放原请求，不能后台自动补全或使用新输入重放旧票据。

#### Scenario: 取消后迟到成功
- **WHEN** 用户取消补全后原提交返回 token
- **THEN** SDK 不保存该 token、不恢复已取消流程，也不影响随后创建的登录

#### Scenario: 补全结果未知
- **WHEN** 提交后网络断开而流程尚未过期，用户选择重试
- **THEN** 仅重发同一流程保存的原票据、凭证和设备快照；过期或编辑凭证后不允许该重放

### Requirement: 验证码轮次及等待约束

SDK SHALL 将验证码轮次与用途、目标和当前流程代次关联，并返回 code_id 与 expire_time。重发、修改目标、退出页面流程或会话改变 SHALL 使旧关联失效。CODE_INVALID SHALL 不被解释为已知剩余次数；CODE_EXPIRED SHALL 结束该轮次；429 SHALL 暴露服务端等待秒数并支持等待状态，不自动重发验证码或提交。验证码和完整目标 SHALL 不出现在默认日志及可打印对象中。

#### Scenario: 目标变化后旧发码响应返回
- **WHEN** 用户从目标 A 改成 B，随后 A 的发码响应返回
- **THEN** A 的 code_id 不能绑定到 B 的输入，也不能参与 B 的提交

#### Scenario: 目标失败预算耗尽
- **WHEN** 服务端返回 TARGET_VERIFY_LIMIT 及等待秒数
- **THEN** SDK 保留会话并反映等待时间，不猜测新的比较预算、不自动重试

### Requirement: 成功身份变更与会话失效分离

解绑或换绑成功后，SDK SHALL 按有界确认策略同步当前会话；确认故障 SHALL 不覆盖已确认的操作成功。仅确认原会话失效时 SHALL 清理原凭据。换绑结果未知时 SHALL 不自动重发，调用方可在仍有效会话或重新登录后读取身份列表核实。确认或清理的迟到结果 SHALL 不影响新账号。

#### Scenario: 换绑撤销发起会话
- **WHEN** 换绑返回新身份且当前会话来源包含旧身份，随后确认原会话失效
- **THEN** SDK 保留换绑成功及新身份结果，同时转为退出态，不能撤销或重复换绑

#### Scenario: 解绑后确认不可用
- **WHEN** DELETE 成功返回 204，但当前会话确认遇到 503 或超时
- **THEN** SDK 在有限时间内返回解绑成功，保留未被证实失效的凭据并提供同步状态，不重发 DELETE

### Requirement: 业务请求认证的范围和重放控制

SDK SHALL 提供复用同一消费者会话的业务请求认证接入方式，调用方 SHALL 显式声明受信任服务 origin 与路径范围。未授权目标、跨域重定向或匿名账号端点 SHALL 不获得消费者凭据。请求重放 SHALL 受方法、响应类别和请求体可重放声明约束；不可重放请求在需要重试时 SHALL 返回明确结果。登录、补全、刷新、撤销及结果未知的敏感写操作 SHALL 不进入通用自动重试链。

#### Scenario: 外部地址
- **WHEN** 业务请求指向未授权 origin，或返回重定向到其他地址
- **THEN** SDK 拒绝认证请求或停止跟随，不向目标发送 access、refresh 或设备身份

#### Scenario: 一次性请求体
- **WHEN** 流式上传收到认证失败且原流已消费
- **THEN** SDK 不自动再次发送该请求体，调用方得到可识别的不可重放结果

### Requirement: 平台适配与可测试的生命周期

SDK SHALL 提供可替换的凭据存储、设备身份、客户端描述来源、时钟和网络实现，以及独立测试入口。默认平台适配 SHALL 不要求应用提供品牌常量；设备 ID SHALL 本地持久化，描述 SHALL 仅为非可信展示数据。描述读取失败 SHALL 有界退化，不阻断认证或记录原始描述。释放 SDK 或控制器 SHALL 停止其拥有的计时器和订阅，并使未完成流程失效；不会关闭调用方拥有的组件。测试替身 SHALL 能模拟 scope、刷新、补全、失效和延迟，无需平台通道。

#### Scenario: 无平台通道测试
- **WHEN** 应用注入内存存储、固定设备信息、时钟和网络替身
- **THEN** 能测试完整账号状态变化及迟到响应，不依赖真机或平台插件

#### Scenario: 释放后返回
- **WHEN** 控制器释放后平台描述或网络请求返回
- **THEN** 不再发起旧认证请求或发布状态变化，调用方持有的网络组件仍可使用
