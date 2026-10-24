# Design

## Context

动机与范围见 [proposal.md](proposal.md)，行为契约见 `specs/`。本变更的结构几乎是 QQ App 登录（[归档变更](../archive/2026-10-24-add-qq-app-sign-in/design.md)）的同构复制，可直接复用的现状：

- `user/idp/qq.go`、`qq_api.go`、`qq_config.go`：令牌校验 → 独立预算资料查询 → 资料失败记降级但登录继续；宿主 HTTPClient 浅拷贝并禁用重定向；传输错误只返回固定阶段字符串；响应 64 KiB 上限。
- `user/idp/alipay_json.go` 的 `alipayObject` / `alipayString`：包内通用严格 JSON 解析（拒绝重复键、尾随内容、非对象），QQ 已直接调用。
- `user/service_idp.go` 的 `refillsProfileOnSignIn` 集合（ALIPAY、QQ），三处调用点已统一走该判定。
- 提供方登记点：`enum` 内置类型、`user/identity_policy.go` 的 `builtinKindOrder`、`config.go` 的 `providerKinds`/字段/`ConfigFromEnv`、`accountkit.go` 装配、OIDC 保留名检查、`user/observability.go` 的方法值与依赖值映射、`internal/accountsvc/config.go` 的显式启用校验与变量名映射。

与 QQ 的本质差异只有三点：uid 是全局标识（无模式、无元数据）；令牌校验接口返回剩余有效期；错误以非 200 状态加 `error_code` 体返回，且字段类型在字符串与整数之间不固定。

## Goals / Non-Goals

**Goals:**

- 在现有 Provider 接口内完成微博令牌校验与资料查询，网络与领域事务分离。
- 微博与 QQ 的代码形态保持平行，便于对照审查；只在上述三点差异处分叉。

**Non-Goals:**

- 不抽取 QQ 与微博的公共 HTTP 客户端或配置类型，不修改 QQ、支付宝实现；仅调用其包内既有严格 JSON 函数。
- 不把补填集合做成公开配置或 Provider 可选接口。

## Decisions

### 1. 提供方与凭证

新增 `idp.Weibo` 实现 Provider，文件按 QQ 拆分为 `weibo.go`、`weibo_api.go`、`weibo_config.go`。`Verify` 用 `decodeCredential` 严格解码 `{app_id, access_token}`，在任何网络调用前检查允许列表。`enum` 增加 `IdentityWeibo = "WEIBO"`，同步 `builtinKindOrder`、`providerKinds` 与 OIDC 保留名检查。

`Config` 增加 `WeiboAppIDs []string`、`WeiboAPIBaseURL string`。字段名用 `AppIDs` 而非 `AppKeys`：与凭证字段 `app_id` 及 QQ/微信/支付宝一致，文档注明即微博 AppKey。

备选：凭证字段改为 `app_key`。未采用，同一 HTTP 契约内第三方应用标识字段统一为 `app_id`，客户端分派逻辑更简单。

### 2. 令牌校验流程

```
Verify(app_id, access_token)
  |-- 形状/允许列表检查（无 I/O）
  |-- POST {base}/oauth2/get_token_info   form: access_token   预算 10s
  |     非 200 且体含 error_code -> 错误码分类（见 5）
  |     appkey != app_id         -> ErrAppNotAllowed
  |     expire_in < 0            -> ErrInvalidCredential
  |     uid/appkey/expire_in 缺失或类型非法 -> ErrMisconfigured
  |-- subject = uid:<uid>,  ExpireTime = now + expire_in
  |-- GET {base}/2/users/show.json?access_token&uid   预算 3s
  |     任意失败 / id 与 uid 不符 -> 记降级，资料为空
  |     成功 -> name=screen_name, picture=first(avatar_hd, avatar_large, profile_image_url)
  `-- 返回 Identity（不含令牌）
```

- get_token_info 用 POST 表单，令牌不进入 URL；资料接口为 GET，令牌在查询串中，因此两者都不包装底层传输错误。
- `appkey` 必须等于请求的 `app_id`，理由同 QQ：多 AppKey 实例中不得以本 App 名义接受另一 App 的令牌。
- `expire_in` 为负按官方说明表示已过期，映射凭证无效；非负时填 `Identity.ExpireTime`，与 Apple/Facebook/OIDC 一致：该期限只用作 pending 登录补全流程截止时间的上限（`createPending` 取二者较早者），不改变已建立会话的刷新与过期规则。
- 不读取 `scope`、`create_at`：不参与任何判定，读取只会增加解析失败面。

### 3. 数字/字符串双形态字段

官方表格称 `uid`、`appkey`、`expire_in` 为 string，示例却是数字。新增包内函数 `weiboDecimal(object, key) (string, error)`：

- 值为 JSON 字符串时取其内容；为 JSON 数字时取原始字面量文本（`json.RawMessage`，不经 float64）。
- 结果须匹配 `^-?[0-9]{1,20}$`；uid、appkey 不允许负号。浮点、指数、布尔、null、对象一律拒绝。
- `expire_in` 为负表示令牌已失效；为 0 或超出 int64 / `time.Duration` 可表示范围时按未知期限处理（`ExpireTime` 保持零值）。0 若换算为当前时刻会使 pending 补全立即失效，超大值换算会溢出成过去的截止时间；两者都不是令牌失效的证据。

`error_code` 同样走该函数（官方示例为字符串）。

备选：只接受一种类型、联调后再定。未采用：类型不一致是官方文档本身的事实，联调只能验证一个时间点的行为，严格单一类型会把上游的无害变化变成全量登录故障。

### 4. 资料映射

资料在 `Verify` 内完成，pending 只保存净化后的声明。可信条件：HTTP 200、无 `error_code`、`id`（或 `idstr`，任一存在即可，二者都存在时须一致）经 `weiboDecimal` 后等于已验证 uid。头像取三个字段中第一个非空值，仅当其 scheme 为 `https` 时交给既有 `providerProfileFrom` 净化，否则丢弃头像，不回退到下一个字段：回退会让同一用户在不同时刻得到不同尺寸，且 HTTP 地址出现本身说明该资料的头像不可用。

不做 QQ 式 http→https 改写：微博头像主机为多个 `*.sinaimg.cn` 子域，固定精确名单难以维护，后缀匹配又放宽了信任；当前返回以 HTTPS 为主，名单外直接丢弃不影响登录。若联调发现大量 HTTP 头像，再单独评估。

### 5. 错误分类

微博错误响应通常为 HTTP 4xx 加 `{"error","error_code","request"}`。处理顺序：

1. 传输错误、超时、3xx、429、5xx → `ErrUnavailable`（429/5xx 不解析体）。
2. 其他非 200：体可严格解析且含合法 `error_code` 时按下表分类，否则 `ErrUnavailable`。
3. 200：体含 `error_code` 时同样按下表分类；否则按成功响应解析。

| 来源 | 条件 | 哨兵 |
|---|---|---|
| get_token_info | 21314、21315、21316、21317、21319、21327、21332 | `ErrInvalidCredential` |
| get_token_info | 10005、10014、21321 | `ErrMisconfigured` |
| get_token_info | 其他 error_code | `ErrUnavailable` |
| get_token_info | appkey 与 app_id 不符 | `ErrAppNotAllowed` |
| get_token_info | 成功体缺字段或类型非法 | `ErrMisconfigured` |
| users/show | 任意失败（含 10022–10024 等限流码、21335） | 不返回错误，记 `weibo_profile_unavailable` |

`error` 文本与 `request` 字段不参与判断也不输出。调用方上下文已取消时返回 `ctx.Err()`，与 QQ 相同。

### 6. 补填集合

`refillsProfileOnSignIn` 集合加入 `enum.IdentityWeibo`，注释同步。不改调用点。微博增加与 QQ 对应的补填、并发 PATCH、pending 与回滚测试。

### 7. 配置与装配

| 库配置 | 环境变量后缀 | 规则 |
|---|---|---|
| WeiboAppIDs | WEIBO_APP_IDS | 逗号分隔，1–20 位 ASCII 数字，不重复、无空项；空表示禁用 |
| WeiboAPIBaseURL | WEIBO_API_BASE_URL | 默认 `https://api.weibo.com`；HTTPS，HTTP 仅数值回环地址 |

`Config.Validate` 与 `NewWeibo` 共用纯函数 `ValidateWeiboConfig`，不做 I/O。地址规范化在 `weibo_config.go` 内按 `normalizeQQBaseURL` 相同规则实现，不修改 QQ 函数。`PolicyFingerprint` 覆盖排序去重后的 AppKey 与规范化地址。accountsvc 在显式启用 WEIBO 而列表为空时以 `ACCOUNTKIT_WEIBO_APP_IDS` 报错，并把库校验错误映射到 `ACCOUNTKIT_WEIBO_*` 变量名。

### 8. 观测

`observability` 增加方法值 `weibo`、依赖值 `verify_weibo`、降级场景 `weibo_profile_unavailable`，在 `user/observability.go` 两处映射加分支，同步 metricdata 有界取值集合与项目测试。Observer 接入方式同 QQ。

## Risks / Trade-offs

- [令牌可重放：泄露的 access_token 在有效期内可用于登录] → 与 QQ 一致接受；`appkey == app_id` 防止跨应用令牌；文档要求只经 HTTPS 提交、不记录令牌。
- [uid 公开：攻击者知道某用户 uid 并不能登录，但 uid 不能当作秘密使用] → 主标识只来自服务端令牌校验结果，客户端无法提交 uid；文档说明。
- [官方文档陈旧，接口行为可能已变] → 双形态字段、严格解析和未知错误码归 503，避免误建身份；联调清单覆盖真实字段类型与 appkey。
- [21321 等归为配置错误会让未审核应用的超限用户看到 500] → 这确实需要运维处理（提交审核或加测试账号），不是用户可重试的 503；固定消息不泄露上游原文。
- [users/show 对新应用权限或限流收紧] → 资料降级不阻断登录；后续登录按补填规则补齐。
- [HTTP 头像被丢弃导致部分用户无头像] → 不影响登录；联调后据实际比例决定是否另开改写变更。

## Migration Plan

无数据库结构变化，无迁移。部署方按需设置 `WEIBO_APP_IDS` 后重启；未设置时行为不变。回退到不含微博的版本后，已有微博身份按"未登记的历史类型"规则可列出、解绑、匿名化但不能登录。uid 主标识不依赖配置，增删 AppKey 不改变既有身份命中。

## Open Questions

- 真实移动 SDK 令牌下 `get_token_info` 的字段类型与 `appkey` 是否恒为移动应用 AppKey：双形态解析已覆盖类型差异，联调结果只记入 verification.md。
- 令牌失效时的实际 HTTP 状态码与 error_code（21327 与 21332 哪个更常见）：分类表已覆盖两者，联调只补合成样本。
