# Proposal

## Why

用户资料已有显示名，缺少通用头像地址，宿主无法通过统一资料接口读取和修改头像。用户已确认本次只新增 avatar_url，并明确要求直接修改当前 0001，不新增 0002。

## What Changes

- 新增内置可选 HTTPS 头像地址，与显示名一起提供原子资料更新。
- 本人、管理面和 Go 批量公开资料返回头像；冷静期保留、公开资料隐藏、最终匿名化清空。
- 限定修改当前 0001 的账号表，更新当前哈希和运行时结构预检；旧同号结构无写拒绝。
- 保留现有显示名 Go 方法及 HTTP Service 实现兼容性。

## Capabilities

### New Capabilities

- `user-basic-profile`: 内置头像和显示名的读写、校验及生命周期。

### Modified Capabilities

- `host-account-contracts`: 最小批量公开资料包含头像。
- `versioned-auth-storage`: 本次头像列初始定义与旧结构拒绝契约。

## Impact

涉及 user 领域和查询、sqlc 生成结果、消费者与管理 DTO、迁移保护、兼容文档及验证。无新增依赖。旧 schema 不会自动增加头像列，需新空 schema；本次不提供旧数据迁移。
