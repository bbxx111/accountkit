# Spec Delta

## MODIFIED Requirements

### Requirement: 内置头像资料

账号 SHALL 保存可选 avatar_url；未设置以数据库 NULL 表示，Go 和 HTTP 读取返回空字符串。本人资料、管理列表及详情 SHALL 返回该字段。头像 SHALL 独立于可选 profile_attributes Schema，扩展顶层不得重复声明 avatar_url。头像文件上传、存储、默认头像和第三方头像的持续同步由宿主负责。其他规格规定的一次性填充可以把已通过地址校验的 URL 写入仍为空的 avatar_url；库 SHALL 不访问该地址。

#### Scenario: 新账号和已设置头像
- **WHEN** 读取新账号或已设置头像的账号
- **THEN** avatar_url 分别为空字符串或已保存的地址，不增加 JWT 或内省字段

#### Scenario: 一次性填充不转移文件责任
- **WHEN** 其他规格把通过地址校验的 URL 写入仍为空的 avatar_url
- **THEN** 读取返回该地址，库不访问它；宿主继续负责文件、默认头像和此后的同步

## ADDED Requirements

### Requirement: 显示名长度
显示名 SHALL 去除首尾空白。空字符串 SHALL 清空该字段。非空值 SHALL 为合法 UTF-8，不含控制字符或双向格式控制符，且不超过 128 个 Unicode 码位。超限或含禁止字符 SHALL 拒绝整次资料更新，不回显原值，原显示名和 update_time SHALL 保持。

#### Scenario: 接受较长显示名
- **WHEN** 本人提交不超过 128 个码位的合法显示名
- **THEN** 保存该值，本人资料和公开资料返回它

#### Scenario: 超过上限或含禁止字符
- **WHEN** 本人提交超过 128 个码位或含禁止控制符的显示名
- **THEN** 返回 400 INVALID_ARGUMENT，原显示名和 update_time 保持
