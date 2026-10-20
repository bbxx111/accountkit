## MODIFIED Requirements

### Requirement: 可选结构化客户端描述

系统 SHALL 支持可选客户端类型、系统、设备、App、浏览器及有界扩展。JSON SHALL 使用 `type`、`os{name,version}`、`device{type,manufacturer,model}`、`app{name,version,build}`、`browser{name,version}`、`extensions`；客户端类型 WEB/NATIVE，设备类型 DESKTOP/PHONE/TABLET，未知值缺省且枚举0不输出。app与browser SHALL 可并存。Go模型 SHALL 保留现有平坦字段并新增制造商、build与扩展。原设备标识/名称、设备头及同设备登录规则 SHALL 保持。

#### Scenario: 移动浏览器与原生 App
- **WHEN** Android Chrome 和含 WebView 的原生 App 分别提供描述
- **THEN** 可分别表达 WEB+Android+Chrome、NATIVE+Android+App+browser，不以类型清除有效信息

#### Scenario: 旧客户端
- **WHEN** 客户端只提供原设备头
- **THEN** 登录可正常完成，未知描述缺省，不伪造型号或版本

### Requirement: 有界采集及明确输入契约

HTTP SHALL 保持既有九个可选描述头，合法显式值优先、缺失由有界标准 Client Hints 再由 UA 补充；宿主可替代解析。扩展 SHALL 只通过直接 Go 或宿主 ClientInfo 回调提供，不新增任意 JSON 请求体或头。解析 SHALL 无网络I/O、不索取额外高熵数据；缺失/自动失败不阻断认证，自报值不作授权或可信设备证明。名称、型号、制造商最多64字符，版本32，build64；有效UTF-8且无控制字符，规范化完整JSON最多2048字节。显式非法输入 SHALL 在凭证消费前按普通400 INVALID_ARGUMENT/reason INVALID_CLIENT_INFO或OAuth invalid_request拒绝；UA/Hints各自2048字节上限、异常忽略且不保存原值。

#### Scenario: 自动识别不完整
- **WHEN** UA 精简或 Hints 缺失、畸形、超限
- **THEN** 保留可用合法字段，不拒绝有效登录或网络探测，原始输入不存储/输出

#### Scenario: 显式非法值
- **WHEN** 显式类型非法、字段超长、控制字符或完整规范化JSON超过2048字节
- **THEN** 按端点协议400拒绝，不消费凭证、不轮换refresh、不持久化描述

#### Scenario: 元数据不能充当凭证
- **WHEN** 调用者伪造设备、系统、浏览器、App或扩展
- **THEN** 仍执行原凭证、scope、归属和新鲜度检查，不增加权限或会话合并资格

#### Scenario: BFF 代转
- **WHEN** 默认解析只能看见 BFF 客户端库
- **THEN** 不声称识别终端设备，宿主可显式适配，不无条件信任转发头

#### Scenario: 数字经过 JSONB 往返
- **WHEN** 描述包含指数、小数或大整数
- **THEN** 业务代码在写入前按精确十进制规范化并计算展开长度，完整JSON仍受2048字节上限；超限在凭证消费前拒绝，合法数值经JSONB往返可读且不丢精度

#### Scenario: 原始 JSON 解析资源
- **WHEN** 原始JSON或扩展RawMessage超过16KiB，或扩展根键数超过64
- **THEN** 在解码或建立输出map前拒绝；最终规范化JSON仍最多2048字节，不能用巨大空白或容器绕过解析资源边界

### Requirement: 初始及最近快照

系统 SHALL 每会话至多保留一份完整客户端描述和服务器观察时间，不保留INITIAL或快照种类。成功登录有描述时保存一份；pending首次描述及时间 SHALL 加密保存，最终完成有本次有效描述时整体使用本次及本次观察时间，否则整体沿用首次描述和首次时间。成功重新认证和新一轮刷新 SHALL 有有效描述即整体替换，无描述保持值及时间；不改变device_id或来源。缺失字段 SHALL 不跨请求保留，名称与版本不跨请求拼接。失败/回滚 SHALL 不持久化。

#### Scenario: pending 后续设备字段省略
- **WHEN** 首次证明有浏览器描述，完成请求无有效描述
- **THEN** 最终保存首次完整描述与原观察时间；设备ID/名称一致性仍按原契约

#### Scenario: 刷新时 App 升级
- **WHEN** 新一轮刷新提供包含新版本的完整观察
- **THEN** 整体替换描述并更新观察时间，不保留旧观察缺失字段；来源与轮换规则保持

#### Scenario: 旧会话首次观察
- **WHEN** 当前合法会话无描述，成功重新认证提供有效描述
- **THEN** 新增唯一一条描述及本次观察时间，不创建初始快照

#### Scenario: 补全时才获得描述
- **WHEN** pending首次无描述，完成提供合法描述
- **THEN** 只保存本次描述和时间，payload缺少可选字段不拒绝票据，不伪造首次环境

#### Scenario: 软件切换时版本清理
- **WHEN** 旧观察为Chrome版本X，新观察为Safari且缺版本
- **THEN** 新描述只有Safari且版本缺省，旧版本X不保留

#### Scenario: 部分新观察整体替换
- **WHEN** 旧观察含系统/设备/App，新观察仅含browser
- **THEN** 唯一描述替换为本次browser，旧系统/设备/App不残留

#### Scenario: 完成本次描述优先
- **WHEN** pending首次含系统和App，完成本次仅含browser
- **THEN** 最终仅保存本次browser与本次时间，不拼接首次字段

### Requirement: 服务端查询和数据清理

当前详情及两面会话列表 SHALL 可返回单份nested client_info和RFC3339 client_info_update_time，各面明确DTO；不再返回initial_client_info或known字段。缺失描述 SHALL 省略，存在描述的JSON必须经业务校验；存储中非法描述 SHALL 为内部错误而非静默丢失。描述 SHALL 不含原始UA、IP/位置、硬件ID、完整身份或凭据，不写日志正文/审计详情/指标标签。清理 SHALL 覆盖该会话唯一描述，匿名化覆盖全部历史并遵守同事务回滚。

#### Scenario: 两面查询
- **WHEN** 消费者查询本人会话或授权管理员查询目标账号会话
- **THEN** 返回同结构nested描述和观察时间，保持用户隔离及管理员角色检查

#### Scenario: 缺少描述
- **WHEN** 合法会话没有可识别客户端信息
- **THEN** 原设备字段保留，client_info缺省，不影响非空认证来源也不自动退出

#### Scenario: 删除与回滚
- **WHEN** 历史会话清理或账号匿名化
- **THEN** 唯一描述被覆盖，匿名化失败则与其他变更一起回滚

## ADDED Requirements

### Requirement: 扩展JSON有界且深拷贝

扩展 SHALL 为对象，所有层级键匹配 `[a-z][a-z0-9_]{0,63}`，值支持JSON primitive/null/object/array。根扩展对象不计节点和深度，直接值深度1，每个值含容器/数组元素计节点；总节点最多64，深度4，每数组16项，每字符串256字符。规范化 SHALL 深拷贝map和原始字节，拒绝重复键、无效UTF-8、控制字符、非法数值及超限；完整JSON包含扩展的2048字节限制仍适用。核心对象 SHALL 拒绝未知键、重复键和错误类型。

#### Scenario: 边界与精度
- **WHEN** 合法扩展达到64节点、深度4、数组16项或字符串256字符且完整JSON未超限
- **THEN** 可接受；超过任一上限即拒绝，合法大整数往返不因浮点转换损失精度

#### Scenario: 输入引用变更
- **WHEN** 调用方修改已传入扩展map或RawMessage底层字节
- **THEN** 已规范化、持久化或输出结果不被改变，不共享可变引用

#### Scenario: JSON隐藏覆盖
- **WHEN** 原始扩展JSON存在重复键或核心JSON有未知字段
- **THEN** 拒绝整个描述，不按最后一个值覆盖掩盖错误

### Requirement: 扩展隐私由已知防线与宿主白名单共同承担

系统 SHALL 在扩展各层拒绝已知敏感字段名，包括凭证/验证码、raw UA、IP/位置和硬件ID的明确字段或词段；非法值按客户端描述输入错误处理。库 SHALL 不声称能识别任意正常字段值中的隐私，宿主 SHALL 负责扩展语义、白名单及数据来源，并向调用者说明这些限制。

#### Scenario: 嵌套敏感键
- **WHEN** 扩展包含token、password、raw_user_agent、ip_address、location或hardware_id等已知敏感键
- **THEN** 即使位于嵌套对象/数组也拒绝，不记录被拒绝的敏感内容

#### Scenario: 普通字段夹带隐私
- **WHEN** 宿主拟通过普通字符串扩展承载额外数据
- **THEN** 宿主负责判断其隐私与业务语义，不能依赖库的结构验证作为自动匿名化保证
