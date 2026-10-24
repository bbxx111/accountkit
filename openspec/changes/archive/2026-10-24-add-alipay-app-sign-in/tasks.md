# Tasks

## 1. 类型、应用配置与密钥校验

- [x] 1.1 在 `enum` 追加内置 ALIPAY，保持 FACEBOOK 与其他类型，禁止 OIDC 配置占用 ALIPAY；以枚举、注册表及类型限制测试验证，无 SQL/schema 变化。
- [x] 1.2 增加 `AlipayApp`、独立 subject mode 与库配置字段，实现多应用 PEM、RSA 位数、AppID、模式及网关静态校验；以缺字段、重复项、PKCS#1/PKCS#8/SPKI、非 RSA/弱密钥、空白模式、非法网关和配置深拷贝测试验证。
- [x] 1.3 在 `ConfigFromEnv(prefix)` 增加 APP_IDS 与逐 AppID 密钥解析，更新 README 配置表及无秘密示例；以自定义前缀、真实多行 PEM、空列表、非法显式配置和错误不泄密测试验证，确认不增加文件/网络 I/O。

## 2. OpenAPI 签名与换码验证

- [x] 2.1 在 `user/idp` 增加专用 RSA2 请求与响应处理，按 design 的原始字节规则验签；用独立生成的合成密钥、手工规范化预期字符串/独立签名夹具验证排序、编码、SHA-256/PKCS#1 v1.5、签名节点及 JSON 歧义拒绝。
- [x] 2.2 实现固定网关 POST、10 秒兑换上限、64 KiB 响应上限、禁止重定向和不自动重试；本地 HTTP 测试覆盖取消、超时、过大/截断响应、3xx/429/5xx，并断言 code 不被重新提交。
- [x] 2.3 实现 ALIPAY credential 严格解码、应用允许列表和 `alipay.system.oauth.token` 兑换；把官方接口的有效/已用/过期 code、配置错误、服务忙样本转为合成夹具验证映射，无签名或未知异常不得作为可信凭证拒绝依据。
- [x] 2.4 实现固定 UNIONID/OPENID subject、白名单标识及公钥规范化配置指纹；测试跨 App UnionID 一致、OpenID 隔离、缺必需标识、禁止 user_id 回退、列表重排不变及换公钥失效，更新公开 Go 类型与模式注释。

## 3. 资料查询与允许登录的降级

- [x] 3.1 实现换码后本次内存 token 的 `alipay.user.info.share` 查询，仅映射 nick_name/avatar 并核对 OpenID；测试 auth_token 请求签名、响应验签、标识不一致、无 token/OpenID、未知字段忽略及不得使用真实姓名/登录名兜底。
- [x] 3.2 增加独立 3 秒资料预算、固定分类脱敏告警和失败放行；用超时、权限错误、坏签名、畸形响应、正常缺字段及外层取消测试验证，确保只在外层请求仍有效时继续身份流程。
- [x] 3.3 确保返回的 `idp.Identity` 无第三方令牌，在领域入口净化支付宝资料后再进入 pending；用序列化、日志/错误/审计捕获测试验证秘密及不可信资料不外泄，并更新 `user/idp` 包注释和极简授权说明。

## 4. 领域资料补填与生命周期

- [x] 4.1 在已有 ALIPAY 身份登录分支增加账号锁内的空字段补填，保留创建身份的既有首次填充；真实数据库测试覆盖首次成功、首次失败后重登、按字段补填、非空保留、主动清空后补填及不使用历史 profile 兜底。
- [x] 4.2 覆盖 pending 创建、关联既有账号、同一身份并发归属、完成与结果重放；真实数据库测试确认资料只在完成事务落库、相应已归属 ALIPAY 分支也能补填、完成不访问上游且不保存 token。
- [x] 4.3 验证用户并发 PATCH、账号冻结/注销/匿名化、身份冲突以及会话后段失败；以受控并发/故障测试证明不覆盖先提交资料、不在失效账号补回数据、不留下部分事务结果，网络请求不持有数据库事务。
- [x] 4.4 复用并验证多 App 首次登录唯一冲突重试、普通绑定/解绑及会话来源；覆盖 GOOGLE/APPLE/FACEBOOK/OIDC 仅首次填充的回归和支付宝重复绑定行为，更新 `docs/compatibility.md` 的补填与模式切换语义。

## 5. 库装配、服务与观测

- [x] 5.1 在 `accountkit.go` 装配 Alipay 提供方并同步 providerKinds、Enabled/Required 与策略指纹；集成测试验证仅 ALIPAY 且 Required=[]、需要邮箱补全、禁用后历史身份访问、配置变更拒绝旧 pending，无新授权发起路由。
- [x] 5.2 更新 accountsvc 的动态配置错误映射、纯第三方启动校验及配置示例；配置/服务测试验证缺应用或密钥启动拒绝、纯 ALIPAY 无 SMTP、migrate 未配置可运行而已提供非法配置拒绝，保持 Facebook/OIDC 配置行为。
- [x] 5.3 追加固定 alipay、verify_alipay、alipay_profile_unavailable 观测取值与投影允许列表；测试两次网络请求仅一次验证器调用、资料降级登录成功、最终领域拒绝、外层取消、正常缺字段及秘密不进入标签，更新指标文档。
- [x] 5.4 更新 `docs/go-integration.md`、`docs/accountsvc.md`、README 与 `deploy/accountsvc/.env.example`；对照测试核对多 App PEM 配置、模式默认值、配置指纹以及库 New 无 I/O，示例不包含真实凭据。

## 6. App 接入说明与库侧契约验收

- [x] 6.1 提供 App 极简 SDK 接入文档：auth_user、随机 state 关联/一次性核对、SDK 回跳、安装检测/取消、提交统一凭证、code 消费后重试需重新授权；核对官方接口和配置示例，明确 Flutter 需极简桥接而不是直接套用完整版 auth 方法。
- [x] 6.2 使用正式根门面和 HTTP handler、实际 Alipay Provider、本地独立签名网关与真实 PostgreSQL/Redis，验证登录、本人资料读取、刷新、凭证拒绝及资料失败后补填；TestAlipayHTTPLoginContract 实际通过且纳入必需清单，记录库侧保证范围。
- [x] 6.3 经正式 HTTP 入口配置两个 AppID 与独立密钥，验证 UNIONID 同号单账号/身份及 OpenID 元数据合并、OPENID 应用隔离与不自动升级，以及跨 App 空字段补填；TestAlipayHTTPMultiAppIdentityContract 实际通过且纳入必需清单，不将模拟值相同描述为真实平台分组验证。

## 7. 综合验证与交付记录

- [x] 7.1 设置 GOWORK=off，运行 `go build ./...`、`go vet ./...`、`go test -count=1 ./...`，并提供真实数据库执行相关事务/并发测试；逐项记录结果与 skip，不将缺数据库的单元测试结果当作集成通过。
- [x] 7.2 按项目所需门禁维护 `scripts/required-tests.txt`，核对生成结果及迁移历史；若实施实际改了手写查询则执行 sqlc generate/check-generated，不修改任何历史迁移或冻结源，以差异检查和相应脚本验证。
- [x] 7.3 在具备环境时执行完整 `scripts/verify.sh`（含 race 和恢复演练），完成安全/兼容性审查并处理实质问题；检查 OpenSpec 全量严格校验，将命令、审查结论及未验证项统一记录到 verification.md，未完成验证不得勾选为通过。

## Workflow follow-up

- 宿主上线前独立验证真实 SDK、设备安装/取消/回跳 state、授权权限、昵称头像及同一控制台分组两个 App 的实际 UnionID 一致性；清单见 docs/alipay-sign-in.md。该宿主验收尚未执行，不作为库 CI 或本变更实施完成的阻塞任务，不计作已通过。
- 用户明确要求后再提交、推送、创建 PR 或归档，遵守独立 merge commit 的集成规则。
- Facebook 变更尚未归档；本次已对齐其重叠注册表增量与同步后的主规格，后续归档仍需智能合并并保留 ALIPAY 类型、凭证场景及资料规则，不能用旧增量覆盖新基线。
