# user-basic-profile Specification

## Purpose

为消费者与嵌入式宿主提供一致的基础账号资料，支持显示名和头像地址的读取及原子部分更新，并在账号冻结、注销恢复及最终匿名化时保持明确的资料访问和隐私边界。

## Requirements

### Requirement: 内置头像资料

账号 SHALL 保存可选 avatar_url；未设置以数据库 NULL 表示，Go 和 HTTP 读取返回空字符串。本人资料、管理列表及详情 SHALL 返回该字段。头像 SHALL 独立于可选 profile_attributes Schema，扩展顶层不得重复声明 avatar_url。头像文件上传、存储、默认头像和第三方头像同步由宿主负责。

#### Scenario: 新账号和已设置头像
- **WHEN** 读取新账号或已设置头像的账号
- **THEN** avatar_url 分别为空字符串或已保存的地址，不增加 JWT 或内省字段

### Requirement: 有界 HTTPS 地址

头像输入 SHALL 去除首尾空白，规范化后不超过 2048 字节；非空值必须是合法 UTF-8、具有非空主机的绝对 HTTPS URL，不含用户凭据、fragment、反斜线、内部空白或控制字符。校验失败 SHALL 返回参数错误，不回显原始地址。库 SHALL 不访问该地址。

#### Scenario: 地址校验
- **WHEN** 提交合法 HTTPS 地址或空字符串
- **THEN** 分别设置头像或清空头像

#### Scenario: 非法输入
- **WHEN** 提交 HTTP、相对路径、data URL、带凭据/fragment 的地址、非法编码或超长地址
- **THEN** 整次更新返回 400 INVALID_ARGUMENT，原资料及 update_time 保持

### Requirement: 原子部分更新与兼容

PATCH /users/me SHALL 支持 display_name 和 avatar_url，省略或 null 不修改，空字符串清空，无有效字段返回 NO_FIELDS。新增 Go 原子更新能力 SHALL 在账号行锁内复核 ACTIVE 状态；不存在账号返回未找到，非法 ID 返回参数错误。同次更新任一值错误 SHALL 不写入。既有 UpdateDisplayName 和消费者必需 Service 方法集 SHALL 保持兼容；旧适配器的显示名更新仍可用，头像请求缺少能力时返回 503 PROFILE_UPDATE_NOT_CONFIGURED。

#### Scenario: 两字段一次修改
- **WHEN** 本人提交合法显示名和头像
- **THEN** 两者一次原子保存并返回完整本人资料

#### Scenario: 并发部分更新
- **WHEN** 两个请求分别更新显示名和头像
- **THEN** 最终保留双方修改；等待锁期间变为非 ACTIVE 的新原子更新拒绝

#### Scenario: 旧适配器
- **WHEN** 宿主仅实现原消费者 Service
- **THEN** 原显示名请求继续工作，含非 null 头像的请求返回固定 503，不部分更新显示名

### Requirement: 头像隐私生命周期

冻结和注销冷静期 SHALL 保留头像，恢复后仍可读取；PENDING_DELETION/DELETED 的批量公开资料 SHALL 隐藏头像。最终匿名化 SHALL 在既有事务内清空 avatar_url，宿主回调失败时整体回滚。新原子更新 SHALL 不允许在匿名化后重新写入头像。

#### Scenario: 恢复与清理回滚
- **WHEN** 账号在冷静期内恢复，或最终匿名化的宿主回调失败
- **THEN** 原头像保留；成功匿名化后头像为空
