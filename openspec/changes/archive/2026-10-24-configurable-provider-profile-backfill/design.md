# Design

## Context

当前补填逻辑集中在 `user` 包：

- `seedEmptyProviderProfile`（`user/service_idp.go`）在锁账号行后复核 ACTIVE 与空字段，只写空列。它已满足原子性与并发要求，本变更不改它的写入语义。
- `createProviderIdentity`（`user/service_identity.go`）在创建第三方身份行后无条件调用 `seedEmptyProviderProfile`。调用方包括：直接注册（`signInVerified` 新账号分支）、补全创建新账号或给已有账号新建身份（`finalizeSignIn`）、绑定（`BindWithIdp`）。
- `refillsProfileOnSignIn`（ALIPAY、QQ、WEIBO）控制三处额外调用：`signInVerified` 已有身份分支、`finalizeSignIn` 已归属身份分支，以及 `verifyIdp` 对声明的预先净化。
- `BindWithIdp` 的同账号重复绑定分支只调用 `refreshProviderMeta`，不补填。

开关的既有先例是 `SensitiveOpVerification *bool`：`Config` 与 `user.Deps` 各有一个字段，`nil` 视为 `true`，`ConfigFromEnv` 用 `strconv.ParseBool` 解析并在失败时报告配置名。

## Goals / Non-Goals

**Goals:**

- 所有第三方身份类型使用同一套补填判断，删除按类型的集合。
- 新建账号始终填充；已有账号按 `ProviderProfileBackfill` 补填。
- 配置默认开启，库与 accountsvc 均可关闭。

**Non-Goals:**

- 不改变资料净化、头像校验、QQ 头像改写及 `provider_meta.profile` 的合并规则。
- 不按提供方分别配置开关，不增加“仅首次”模式或来源标记。
- 关闭开关时不跳过支付宝、QQ、微博的资料接口调用：新建账号仍需要这些声明，验证发生在判断账号是否存在之前。
- 不改 HTTP、数据库、指标和审计。

## Decisions

### 1. 用“账号是否本次新建”判定，而不是“身份是否本次新建”

开关控制的是账号已存在时是否改写资料。补全流程里既可能新建账号，也可能给已有账号新建身份；绑定总是在已有账号上新建身份。因此以账号为准：

| 路径 | 账号 | 行为 |
|---|---|---|
| `signInVerified` 新账号分支 | 新建 | 始终填充 |
| `finalizeSignIn` `isNew` 时新建的第三方身份 | 新建 | 始终填充 |
| `signInVerified` 已有身份分支 | 已有 | 按配置 |
| `finalizeSignIn` 已归属身份 / 给已有账号新建身份 | 已有 | 按配置 |
| `BindWithIdp` 新建身份 / 同账号重复绑定 | 已有 | 按配置 |

备选方案是继续以“身份第一次写入”为准，只给已有身份的登录加开关。但这样关闭开关后绑定仍会改写已有账号资料，与“已有账号按配置”的要求不一致，因此不采用。

### 2. 补填调用从 `createProviderIdentity` 移到调用方

`createProviderIdentity` 只负责创建身份行和元数据，不再调用 `seedEmptyProviderProfile`。新增小函数：

```go
// backfillProviderProfile 在新建账号或已开启补填时用本次声明填充空资料。
func (s *Service) backfillProviderProfile(ctx context.Context, q *db.Queries, userID string, ident idp.Identity, newAccount bool) error {
	if !newAccount && !s.providerProfileBackfill {
		return nil
	}
	return s.seedEmptyProviderProfile(ctx, q, userID, ident)
}
```

各调用点在原先调用 `seedEmptyProviderProfile` 的位置（身份行创建或 `refreshProviderMeta` 之后）调用它，事务边界与锁序不变。`BindWithIdp` 的同账号分支在 `refreshProviderMeta` 后同样调用（`newAccount=false`）。

### 3. `verifyIdp` 对所有提供方统一净化声明

原先只对集合内提供方把 `ident.Name/Picture` 替换为 `providerProfileFrom` 的净化结果，其他提供方在 pending 中保存原始声明，补填时才净化。统一后对所有提供方执行同一替换。`seedEmptyProviderProfile` 与 `mergeProviderMeta` 对已净化的值再次净化结果不变，pending 载荷字段不变，只是值提前净化。进行中的 pending 不受影响：完成时仍对载荷值净化。

### 4. 配置接线

- `Config.ProviderProfileBackfill *bool`，在填充默认值处（与 `SensitiveOpVerification` 相邻）把 `nil` 设为 `true`；`ConfigFromEnv` 解析 `PROVIDER_PROFILE_BACKFILL`，非法值经既有 `fail` 以 `accountkit: <prefix>PROVIDER_PROFILE_BACKFILL: ...` 形式报告，与 `SENSITIVE_OP_VERIFICATION` 相同。
- `user.Deps.ProviderProfileBackfill *bool`，`nil` 视为 `true`；`Service` 保存解析后的 `bool`。
- accountsvc 已通过 `ConfigFromEnv("ACCOUNTKIT_")` 加载，`safeLibraryConfigError` 的 `libraryErrorName` 正则可直接提取配置名，无需新增映射；补一个测试确认。

## Risks / Trade-offs

- [默认开启改变 GOOGLE、OIDC、FACEBOOK 的后续登录行为，用户主动清空的资料会被重新填充] → 兼容指南写明，需要旧行为的宿主设置 `PROVIDER_PROFILE_BACKFILL=false`。
- [关闭后 ALIPAY、QQ、WEIBO 失去后续补填，首次资料失败的新账号资料将一直为空] → 文档说明这是关闭开关的预期结果。
- [关闭后绑定不再填充，与之前所有提供方都会填充不同] → 列为行为变更，写入兼容指南。
- [每次已有账号登录多一次账号行锁] → 原先集合内提供方已如此；`seedEmptyProviderProfile` 在声明为空时直接返回，不锁行；登录事务本就锁同一账号行，额外开销很小。

## Migration Plan

无数据库迁移。升级后默认即为统一补填。回退到旧版本只恢复旧规则，已补填的资料保留。
