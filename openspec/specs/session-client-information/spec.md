# session-client-information Specification

## Purpose

为会话提供可选、有界且可解释的浏览器、系统、App 和设备描述，允许宿主与不同版本客户端渐进接入，并区分登录时环境与最近认证活动观察值，不把自报描述转化为可信设备证明或普通业务请求的写入负担。

## Requirements

### Requirement: 可选结构化客户端描述

系统 SHALL 支持 client_type、os_name/os_version、browser_name/browser_version、app_name/app_version、device_type、device_model。客户端类型 SHALL 区分 WEB/NATIVE，系统和设备类别 SHALL 独立表达；device_type 支持 DESKTOP/PHONE/TABLET。未知值 SHALL 缺省，不将枚举 0 作为有效输出。原 device_id/device_name 及其 X-Device-Id/X-Device-Name 输入和同设备登录规则 SHALL 保持。

#### Scenario: 移动浏览器与原生 App
- **WHEN** Android 上的 Chrome 与 Android 原生 App 分别提供客户端描述
- **THEN** 前者可表达 WEB + Android + Chrome，后者可表达 NATIVE + Android + App 信息，不用单一平台值混合表达客户端类型和操作系统

#### Scenario: 旧客户端
- **WHEN** 客户端仅提供原设备头
- **THEN** 原登录可正常执行；不能识别的新描述字段保持缺失，不伪造型号或版本

### Requirement: 有界采集及明确输入契约

HTTP SHALL 从可选 X-Client-Type、X-Client-OS-Name、X-Client-OS-Version、X-Client-Browser-Name、X-Client-Browser-Version、X-Client-App-Name、X-Client-App-Version、X-Device-Type、X-Device-Model 接收描述，不向认证请求体添加这些字段。合法显式值 SHALL 优先，缺失项由可用的标准 UA Client Hints、再由 UA 解析补充；宿主 SHALL 可提供解析替代能力。解析 SHALL 无网络 I/O，不要求浏览器额外高熵数据。缺失或自动识别失败 SHALL 不阻断认证；自报值 SHALL 不成为授权、限流身份或可信设备证明。

名称/型号 SHALL 最多 64 个字符，版本最多 32 个字符，文本有效 UTF-8、无控制字符，规范化描述合计最多 2048 字节。显式非法输入 SHALL 在凭证消费前拒绝：普通端点返回 400 INVALID_ARGUMENT、reason INVALID_CLIENT_INFO，token 端点使用 OAuth invalid_request。用于自动解析的 UA 和 Client Hints SHALL 分别受 2048 字节总量限制，畸形或超限只忽略该自动解析输入，不保存或输出原值。

#### Scenario: 自动识别不完整
- **WHEN** UA 被精简或 Client Hints 缺失、畸形、超限
- **THEN** 保留可用的合法字段，其余未知；不拒绝有效登录、不发起网络探测

#### Scenario: 显式非法值
- **WHEN** 客户端显式提交非法 client_type、超长版本或含控制字符的名称
- **THEN** 按端点协议返回 400，不消费登录/重新认证凭证、不轮换 refresh、不写入描述

#### Scenario: 元数据不能充当凭证
- **WHEN** 调用者伪造 device_type、系统、浏览器或 App 名称
- **THEN** 仍须通过原有凭证、scope、身份归属与近期认证检查，不获得额外权限或会话合并资格

#### Scenario: BFF 代转
- **WHEN** 请求由宿主 BFF 代转且默认解析只能看到 BFF 的客户端库
- **THEN** 不声称已识别终端设备；宿主可通过明确适配提供原客户端描述，库不无条件信任任意转发头

### Requirement: 初始及最近快照

普通成功登录有有效描述时 SHALL 保存初始和最近快照及服务器观察时间。pending SHALL 在加密流程中保存首次描述；最终完成时 SHALL 保留初始快照，并用本次合法描述更新最近快照，缺失则沿用首次描述。成功重新认证和新一轮刷新 SHALL 只更新最近快照，不改变已有初始快照、device_id 或认证来源。迁移来的旧会话只在后续观察时建立最近快照，不伪造初始环境。

有值的新字段 SHALL 更新对应最近值，缺失字段保留；同一描述中类型显式切换为 NATIVE 时清除 browser 字段，切换为 WEB 时清除 app 字段。名称改变且未提供相应版本 SHALL 清除旧版本；仅提供版本而无已知对应名称 SHALL 不产生虚假组合。没有有效新字段 SHALL 不写入描述更新时间。失败或事务回滚 SHALL 不持久化描述。

#### Scenario: pending 后续设备字段省略
- **WHEN** 初次证明带浏览器描述，后续补全请求未重复提供
- **THEN** 最终会话沿用已加密保存的初始描述，补全设备 ID/名称一致性保持原契约

#### Scenario: 刷新时 App 升级
- **WHEN** 新一轮成功刷新提供同一 App 的新版本
- **THEN** 最近描述与观察时间更新，初始描述、认证来源及原刷新轮换规则保持

#### Scenario: 旧会话首次观察
- **WHEN** 旧会话缺少两类描述，成功重新认证提供客户端信息
- **THEN** 新增最近快照，初始快照仍缺省，不将当前环境冒充登录时环境

#### Scenario: 补全时才获得描述
- **WHEN** pending 首次证明未保存描述，包括升级前创建的流程，而完成时提供合法描述
- **THEN** 完成可以成功，仅保存最近快照；不因 payload 缺少新增可选字段拒绝原票据，不伪造首次证明环境

#### Scenario: 软件切换时版本清理
- **WHEN** 最近浏览器为 Chrome 版本 X，新观察明确为 Safari 但没有版本
- **THEN** 最近浏览器改为 Safari，版本未知，不能保留 Chrome 的版本 X

### Requirement: 重试不写入额外活动

合格 refresh 宽限重试和已完成补全的幂等重放 SHALL 返回既有结果，不更新客户端描述或观察时间。普通 access 验证、内省和业务请求 SHALL 不因本能力增加数据库查询、写入或外部解析服务。客户端描述观察时间 SHALL 不被描述为用户最后一次业务操作时间。

#### Scenario: 宽限重复刷新
- **WHEN** 胜者刷新已保存最近描述，后续同轮宽限请求携带不同描述
- **THEN** 返回原 pair，不再次更新快照或时间，不延长会话期限

#### Scenario: 普通业务鉴权
- **WHEN** 宿主调用 Authenticate 或远程内省处理普通业务请求
- **THEN** 沿用 JWT 与 Redis 吊销校验，不为采集客户端信息访问数据库

### Requirement: 服务端查询和数据清理

当前会话详情 SHALL 可返回 initial_client_info、client_info（最近描述）和 client_info_update_time；消费者和管理员会话列表 SHALL 可返回最近描述及其观察时间，使用各自明确 DTO。缺失字段 SHALL 省略，观察时间使用 RFC3339。描述 SHALL 不包含原始 UA、IP/位置、硬件标识、完整身份或凭证，不写入日志正文、审计详情或指标标签。新增数据 SHALL 随有界会话清理删除，账号匿名化覆盖全部历史会话且遵守同事务回滚。

#### Scenario: 两面查询
- **WHEN** 消费者查询本人会话或已授权管理员查询目标账号会话
- **THEN** 返回可用的最近结构化描述；消费者不能看到其他账号数据，管理员沿用既有角色检查

#### Scenario: 缺少描述
- **WHEN** 旧会话或当前请求没有可识别信息
- **THEN** 查询保留原设备字段，新描述可缺省；这不影响来源已知标记，也不自动触发退出

#### Scenario: 删除与回滚
- **WHEN** 历史会话被清理或账号完成匿名化
- **THEN** 初始和最近描述均被覆盖；匿名化失败则描述清理与其他认证数据变更一起回滚
