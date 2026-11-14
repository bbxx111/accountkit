# Design

## Context

见 proposal.md。既有 oidcjwt 被管理员及服务调用复用；三类 Principal 和授权规则独立。当前管理员 New 同步 Load，服务启动后台 Load；Load 未进入请求期刷新合并。服务 azp 只识别客户端，不能证明机器主体。用户确认不兼容旧的无 scope 服务授权，并指定先完成 HTTP 解析、路由清单、管理员故障隔离。

## Goals / Non-Goals

**Goals:** 保持三个信任边界，统一 HTTP 凭据边界；远端管理员提供方故障不阻断消费者启动；机器令牌按主体绑定和最小权限处理。

**Non-Goals:** 不自建 OAuth 授权服务器，不合并 Principal，不改数据库、消费者 JWT、URL 或网关运行时，不发布或归档。

## Decisions

1. 新增 internal/httpcredentials，唯一 Authorization 头、大小写不敏感 scheme、明确空格边界和 16 KiB Bearer 上限。保留各入口错误映射、消费者可信 Principal 复用及严格认证重验证。共享解析器只解析，不授权。
2. accountsvc 集中声明路由分类与服务端点权限，装配实际消费清单；覆盖 consumer/admin/service/public/operations 类别。服务 handler 的权限检查仍必需。保持全部 URL、方法和认证先于业务解析的行为，不由路由表代替安全校验。
3. adminauth.New 保留内部签名但只静态装配；增加显式预加载方法。run 在监听前装好认证中间件，监听就绪后有界预加载管理员和服务公钥。预加载与请求刷新使用同一 single-flight/冷却机制；调用者取消不取消其他等待者。共享刷新有独立的验证器生命周期与有界超时，关闭时取消并等待本验证器在途刷新，防止停机后遗留网络操作。已知 key 缓存 1 小时，管理员刷新冷却 60 秒，服务 10 秒，保持现有边界。管理员每次等待最多 3 秒；整体 readiness 不因 OIDC 改变。固定类别日志报告预加载及刷新失败，不新增公开诊断端点。
4. 服务 Bearer 无条件授权 P∩S，无模式开关。scope 缺失/空是空集合，格式错误为401，缺所需权限为403；合法未知 scope 忽略，大小写敏感、ASCII 单空格分隔，无通配。非空 scope 按 RFC6749 字符范围 %x21 / %x23-5B / %x5D-7E 校验，首尾/连续空格、tab、引号、反斜杠、非ASCII均401，重复项按集合去重。Basic 为 P；两方式不自动回退。HasPermission 继续仅根据 P 判断能力配置。
5. 每个启用 bearer 的客户端显式配置 BEARER_SUBJECTS（稳定机器 sub，非空列表）；在验签、客户端识别后校验主体绑定，匹配失败401；未登记客户端保持403。提供方必须独占机器主体并禁用该客户端人类登录；不假定 sub=client_id，不相信自报 grant_type。Basic-only 不要求主体。用户已确认只实现通用方案。BEARER_SUBJECTS 使用 JSON 字符串数组，逐项精确匹配，允许逗号和空格，不裁剪；非空且非全空白、最多256字节、无控制字符、无重复。
6. 外部 verifier 支持显式 default 与 rfc9068 profile，分别在 ADMIN_TOKEN_PROFILE/SERVICE_TOKEN_PROFILE 选择，默认 default 保持现有提供方格式。rfc9068 校验 at+jwt/application/at+jwt 及 iss/aud/exp/sub/client_id/iat/jti；服务用 client_id，default 模式用 azp，不回退。两种 profile 的服务令牌都执行机器 sub 和 scope 约束。保持 RS256，不接受 token 自带远程 key URL。管理员始终以 issuer+sub 和配置角色授权，不应用服务 scope。

## Risks / Trade-offs

用户后续确认将原 `azp` profile 名称改为 `default`；不保留旧模式值别名。JWT `azp` 声明、管理员身份规则、服务主体与scope校验均不随命名变化。

- Basic 可获得 P → 要求强制凭据收窄的客户端仅启用 bearer，文档明确秘密泄露边界。
- 机器主体由提供方保证 → 通用主体绑定不能弥补签发方把同一主体发给人类，提供方验收必须核对独占性。
- 管理员预加载失败不再阻断启动 → readiness 不代表管理员可用，以固定类别失败日志与实际管理员请求验收。
- 强制 scope/sub 会拒绝旧 token → 用户明确接受，不保留回退；部署模板和迁移说明同步更新。

## Migration Plan

先发布代码前准备提供方 scope 与稳定服务主体配置，所有 bearer 客户端填写主体并获取含所需 scope 的令牌。选择 RFC9068 时提供方须满足整个 profile。应用与配置成套更新，回退旧二进制将恢复较宽权限语义，不将其描述为等价安全回退。保持 Basic 密钥轮换及跨客户端秘密禁止复用规则。
