# Tasks

## 1. 类型与应用配置

- [x] 1.1 在 `enum` 追加内置 QQ，同步 `builtinKindOrder`、`providerKinds` 与 OIDC 保留名检查，保持 FACEBOOK、ALIPAY 与其他类型；以枚举、注册表、OIDC 占用 QQ 启动失败及端点类型限制测试验证，无 SQL/schema 变化。
- [x] 1.2 增加 `QQAppIDs`、独立 `QQSubjectMode` 与 `QQAPIBaseURL` 库配置及纯函数静态校验；以空列表禁用、空项/重复/非数字/超长 AppID、空白/小写/AUTO 模式、非 HTTPS 非回环地址、带 userinfo/查询/fragment 地址及配置深拷贝测试验证，错误不回显配置值。
- [x] 1.3 在 `ConfigFromEnv(prefix)` 增加 QQ_APP_IDS、QQ_SUBJECT_MODE、QQ_API_BASE_URL 解析，更新 README 配置表；以自定义前缀、未设置、非法显式配置测试验证，确认无文件/网络 I/O。

## 2. 令牌校验

- [x] 2.1 实现 `idp.QQ` 的 credential 严格解码与允许列表检查；测试缺字段、未知字段（openid/unionid）、非对象、尾随内容及未知 AppID 均在任何 HTTP 请求前拒绝。
- [x] 2.2 实现 `/me` 请求：`fmt=json`、仅 UNIONID 模式附加 `unionid=1`、10 秒预算、64 KiB 上限、禁止重定向、无自动重试、传输错误去除 URL；本地 HTTP 测试覆盖取消、超时、超限、3xx/429/5xx 及日志与错误不含令牌。
- [x] 2.3 实现 `/me` 响应严格解析与错误分类：拒绝 JSONP、重复键、尾随内容、错误类型；按 design 表映射 100013–100016、100048、未知码、client_id 不符与缺字段；先核对官方公共返回码页面并以合成夹具固化，测试另一已登记 AppID 的令牌返回 IDP_APP_NOT_ALLOWED。
- [x] 2.4 实现 UNIONID/OPENID subject、标识格式校验、`UnionID`/`OpenIDs[app_id]` 与 `PolicyFingerprint`；测试跨 App 同 UnionID、OpenID 按应用隔离、UNIONID 缺失或 100048 不回退 OPENID、OPENID 模式不受 100048 影响、列表重排指纹不变而模式/地址/应用变化指纹改变。

## 3. 资料查询与头像改写

- [ ] 3.1 实现 `get_user_info` 查询（同一令牌、`oauth_consumer_key`、已验证 openid）及 nickname、figureurl_qq_2→figureurl_qq_1 映射；测试仅取这两类字段、不使用 QQ 空间头像及其他字段。
- [ ] 3.2 实现 QQ 头像改写：HTTPS 原样、名单内 HTTP 主机无端口/userinfo 时升级、其他丢弃；单元测试覆盖大小写主机、带端口、带 userinfo、名单外主机及改写结果仍经既有 `providerProfileFrom` 净化。
- [ ] 3.3 实现 3 秒独立资料预算与失败放行：ret 非 0、is_lost=1、超时、5xx、解析失败记脱敏告警与 `qq_profile_unavailable` 并返回无资料身份；正常缺字段与头像被名单丢弃不计降级；外层取消返回 `ctx.Err()`；测试逐项验证并断言返回 `idp.Identity` 与日志不含令牌或资料原文。
- [ ] 3.4 更新 `user/idp` 包注释说明 QQ 提供方，验证 `go vet ./user/idp/...` 与该包测试通过。

## 4. 补填集合收敛与领域行为

- [ ] 4.1 新增 `refillsProfileOnSignIn`（ALIPAY、QQ）替换 `user/service_idp.go` 与 `user/service_signin_completion.go` 三处支付宝判断并更新注释；支付宝既有单元与数据库测试全部不改动且通过。
- [ ] 4.2 为 QQ 补充真实数据库测试：首次成功、首次资料失败后重登补填、按字段补填、非空保留、主动清空后补填、不使用历史 profile 兜底，以及 GOOGLE 再登录不补填的回归。
- [ ] 4.3 为 QQ 补充 pending 创建/关联既有账号/并发归属/完成重放、用户并发 PATCH、冻结/注销/匿名化竞争及会话后段失败的真实数据库测试，确认完成不访问上游、不保存令牌、不留下部分事务结果。
- [ ] 4.4 验证 QQ 多 App 首次登录唯一冲突、普通绑定/解绑、同一令牌重复登录不报重放；更新 `docs/compatibility.md` 的补填集合、令牌可重放与模式切换说明。

## 5. 库装配、服务与观测

- [ ] 5.1 在 `accountkit.go` 装配 QQ 提供方并同步 Enabled/Required 与策略指纹；集成测试验证仅 QQ 且 Required=[]、需要邮箱补全、禁用后历史 QQ 身份可列出/解绑但不能登录、配置变更拒绝旧 pending。
- [ ] 5.2 更新 accountsvc 的 QQ 变量读取、显式启用无应用时指出 ACCOUNTKIT_QQ_APP_IDS、库错误到变量名的映射与纯第三方启动校验；配置/服务测试验证纯 QQ 无 SMTP 可启动、migrate 未配置可运行、已提供非法配置拒绝，Facebook/Alipay/OIDC 行为不变。
- [ ] 5.3 追加 `qq`、`verify_qq`、`qq_profile_unavailable` 观测取值、`user/observability.go` 映射及 metricdata 允许列表；测试两次网络请求只计一次验证器调用、资料降级登录成功、令牌失败不计降级、外层取消、标签不含 AppID/标识/令牌，更新指标文档。
- [ ] 5.4 更新 `docs/go-integration.md`、`docs/accountsvc.md`、README 与 `deploy/accountsvc/.env.example`；核对模式默认值、UnionID 申请、OPENID 适用场景及示例不含真实凭据。

## 6. App 接入说明与库侧契约验收

- [ ] 6.1 编写 QQ App SDK 接入文档：SDK 登录取得 access_token、提交统一凭证、不提交 openid、令牌保密与可重放风险、UnionID 打通、头像名单与 HTTPS 说明；核对官方文档链接与配置示例。
- [ ] 6.2 使用正式根门面和 HTTP handler、实际 QQ Provider、本地模拟 `/me` 与 `get_user_info` 及真实 PostgreSQL/Redis，验证登录、本人资料读取、刷新、凭证拒绝、跨应用令牌拒绝及资料失败后补填；`TestQQHTTPLoginContract` 实际通过并加入 `scripts/required-tests.txt`。
- [ ] 6.3 经正式 HTTP 入口配置两个 AppID，验证 UNIONID 同号单账号/身份及 OpenID 元数据合并、OPENID 应用隔离与跨 App 空字段补填；`TestQQHTTPMultiAppIdentityContract` 实际通过并加入必需清单，不将模拟值描述为真实平台打通验证。
- [ ] 6.4 执行 `go build ./...`、`go vet ./...`、`go test -count=1 ./...`（含 SERVER_TEST_DB_DSN）、`bash scripts/check-generated.sh` 与 `openspec validate --all --strict`，并把实际结果、宿主联调清单（真机 SDK、UnionID 返回、头像主机 HTTPS）的未执行状态写入 `verification.md`。

## Workflow follow-up

- 归档本变更前先归档 `add-facebook-sign-in`，或在其注册表 delta 中补入 QQ 文本，避免覆盖本变更的需求。
- 归档作为单独步骤执行，归档后核对主规格中 ALIPAY、FACEBOOK、QQ 文本完整。
