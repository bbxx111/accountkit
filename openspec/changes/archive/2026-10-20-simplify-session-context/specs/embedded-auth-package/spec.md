## MODIFIED Requirements

### Requirement: 独立消费和完整功能保留

包 SHALL 独立构建/测试/导入，不依赖外部源码工作区；保留固定基线公开能力，已明确声明的包名、迁移安全、验证码轮次、身份策略/补全与通用审计升级按调用迁移说明适配，不留旧验证旁路。会话上下文尚未发布，本轮 SHALL 明确移除known/initial字段和快照枚举，来源非空、客户端JSON嵌套；Go保留平坦描述字段并增加制造商/build/扩展，提供字段名初始化示例及位置式初始化风险。当前查询 SHALL 仍为可选宿主能力，不扩大必需接口方法集；JWT、refresh及非审计资源格式保持。

#### Scenario: 独立宿主构建
- **WHEN** 独立Go项目依赖accountkit并注入依赖
- **THEN** 可编译并挂载两面接口，不需要外部宿主源码、accountsvc进程或特定管理员OIDC包

#### Scenario: 自定义审计升级
- **WHEN** 宿主按通用审计迁移说明适配记录器
- **THEN** 接收通用事件而不依赖sqlc行，可核对公开Go类型、审计UUIDv7/游标、查询配置及历史数据迁移边界

#### Scenario: 旧自定义认证适配器
- **WHEN** 宿主只实现原消费者必需接口
- **THEN** 原接口仍工作，当前查询返回503 SESSION_QUERY_NOT_CONFIGURED而非空来源成功

#### Scenario: 未发布会话描述调用迁移
- **WHEN** 调用者使用前一开发版known/initial字段、快照枚举或平坦JSON
- **THEN** 按兼容说明移除旧引用，改读非空来源和单份nested描述；扩展由Go/回调提供并配置宿主白名单，不承诺旧源码无修改编译

### Requirement: 登录及身份管理兼容

包 SHALL 保留PHONE/EMAIL验证码、微信/Apple登录和绑定解绑，包括格式、有效期、限流、IdP验证及冲突。PHONE/EMAIL认证/绑定/reauth/换绑 SHALL 使用必需code_id及原错误分类，跨轮次预算保持TARGET_VERIFY_LIMIT。登录 SHALL 执行Enabled/Required及any-of完整性，缺条件返回无user_id的pending，满足则注册；补全自动关联或注册，绑定仍拒绝跨账号占用。解绑 SHALL 同事务撤销来源命中的同账号未吊销会话（含当前/自然过期未清理），来源缺失按不变量拒绝；Go/HTTP同样校验scope、新鲜度、当前会话与身份完整性，成功仍204，不自动转换旧库或批量退出。

#### Scenario: 验证码登录
- **WHEN** 使用轮次协议和同轮目标/code_id/验证码及设备登录
- **THEN** 返回令牌或pending，保留原错误类别和发送额度，累计错误预算按新协议返回

#### Scenario: 第三方登录与绑定
- **WHEN** IdP返回有效凭证或执行绑定解绑
- **THEN** 保留凭证、nonce防重放及身份唯一性，按当前策略保护至少一个有效身份

#### Scenario: 未完成登录不是受限用户
- **WHEN** 未知第三方身份未满足Required
- **THEN** 仅签发pending票据，不创建用户/身份/会话或user:bind令牌

#### Scenario: 解绑来源身份
- **WHEN** 其他身份满足策略且合法解绑当前来源
- **THEN** 软删除和命中会话撤销同事务提交，当前会话可撤销，返回204；缺失来源不当作unknown正常态

### Requirement: 令牌与会话兼容

包 SHALL 保留签名校验、scope、轮换、宽限、查询和撤销；access默认15分钟、refresh滑动30天、宽限30秒。活跃列表排除已撤销/refresh到期，刷新/reauth锁后复核期限；已签发access仍按原到期/显式吊销。ACTIVE登录/新刷新/reauth按全部有效身份计算user或user:bind，宽限结果不重算。PHONE/EMAIL reauth保留sid/refresh并原子替换非空来源及auth_time；第三方用正常重新登录/同设备替换。刷新/绑定不改来源。当前详情查库返回最新来源及可选nested描述，JWT/内省不加字段，普通Authenticate不加DB，描述缺失不阻断认证。

#### Scenario: 刷新重试
- **WHEN** 宽限内重试且会话未撤销/未到期
- **THEN** 返回同轮结果且不更新描述/时间；宽限外旧凭据按原规则失效

#### Scenario: 敏感操作重新认证
- **WHEN** 默认敏感检查启用且认证超过5分钟
- **THEN** 要求重新认证；有效会话保留原refresh协议，到期需重新登录

#### Scenario: 活跃会话展示
- **WHEN** refresh到期但尚未吊销
- **THEN** 两面活跃列表和计数排除，保留期内数据及显式撤销能力仍存在

#### Scenario: 锁等待不延缓过期
- **WHEN** 等待锁期间refresh/reauth会话到期
- **THEN** 不按请求开始时状态续期，分别返回invalid_grant或认证失效

#### Scenario: 第三方重新登录满足近期认证
- **WHEN** 第三方重新取得合法证明并同设备登录
- **THEN** 建新会话、替换旧会话、返回新pair，客户端核对账号更新凭据，不视作旧会话无感reauth
