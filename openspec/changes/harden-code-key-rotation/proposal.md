## Why

当前验证码记录、目标冷却和目标日额度的 Redis 键仅由 active HMAC 密钥定位；正常轮换即使保留旧密钥，也会让旧验证码不可达，并分裂发送限制。需要让库及可选 accountsvc 在按约定滚动切换 active 时继续识别同一目标的全部状态，保留一次性消费和原有限流语义。

## What Changes

- 使用全部已配置 HMAC 版本生成并去重目标键，在单次 Lua 操作内统一处理发码与校验。
- 保留旧验证码的剩余有效期和尝试次数；跨 active 并发校验至多成功一次，重新发码替换全部版本的同用途旧记录。
- 跨版本共享目标冷却，合计目标日额度；每次成功占额仅增加一份目标计数和一份原有 IP 计数。
- 明确程序升级、密钥分发、active 滚动切换及旧密钥退役的条件；补充并发、旧数据接管与真实 Redis 验证。
- 保留周期性数据库密钥回填及现有默认间隔，不新增运维动作。

## Capabilities

### New Capabilities

- `code-key-rotation`: 验证码在 HMAC 正常轮换期间的键定位、原子消费、发送限制连续性及部署兼容条件。

### Modified Capabilities

无。新增规格细化轮换场景，现有登录、投递、用途隔离、错误映射及维护契约继续适用。

## Impact

- 主要实现范围：`user/code/store.go`、`user/code/scripts.go` 及相关测试；复用 `pii.Digester` 的多版本摘要能力。
- 库集成和 accountsvc 共用同一实现；预期无需修改公开接口、配置字段或默认值、HTTP 端点、数据库结构、SQL 生成结果及依赖。
- 更新 README 和相关兼容/部署说明；本次验收记录写入本 change 的 `verification.md`，原始输出存放 `.test-output/`。
- 连续性要求所有服务进程先升级到本实现，并在 active 切换前持有相同的完整轮换密钥集合和一致的验证码策略；不承诺旧程序、缺失密钥或任意配置混用时的连续性。

## Non-goals

- 不新增密钥管理 CLI、HTTP 管理端点、热加载、自动轮换、密钥分发或退役自动检测。
- 不改动 `rekey_digests` / `reencrypt_subjects` 的调度、方向或执行方式。
- 不新增独立验证码密钥，不改变 JWT、refresh、access 吊销或审计失败模式，不扩展 Redis Cluster 支持。
- 不恢复已丢失的 Redis 状态或已移除密钥对应的状态；不把紧急撤销泄露密钥视为无损轮换。
