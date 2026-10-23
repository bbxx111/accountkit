# Tasks

## 1. 类型与应用配置

- [ ] 1.1 在 `enum` 追加内置 WEIBO，同步 `builtinKindOrder`、`providerKinds` 与 OIDC 保留名检查，保持 FACEBOOK、ALIPAY、QQ 与其他类型；以枚举、注册表、OIDC 占用 WEIBO 启动失败及锚点端点类型限制测试验证，无 SQL/schema 变化。
- [ ] 1.2 增加 `WeiboAppIDs`、`WeiboAPIBaseURL` 库配置及纯函数 `ValidateWeiboConfig`；以空列表禁用、空项/重复/非数字/超长 AppKey、非 HTTPS 非回环地址、带 userinfo/查询/fragment 地址及配置深拷贝测试验证，错误不回显配置值。
- [ ] 1.3 在 `ConfigFromEnv(prefix)` 增加 WEIBO_APP_IDS、WEIBO_API_BASE_URL 解析，更新 README 配置表；以自定义前缀、未设置、非法显式配置测试验证，确认无文件/网络 I/O。

## 2. 令牌校验

- [ ] 2.1 实现 `idp.Weibo` 的 credential 严格解码与允许列表检查；测试缺字段、未知字段（uid/refresh_token）、非对象、尾随内容及未知 AppKey 均在任何 HTTP 请求前拒绝。
- [ ] 2.2 实现 `get_token_info` POST 表单请求：10 秒预算、64 KiB 上限、禁止重定向、无自动重试、传输错误只返回固定阶段字符串；本地 HTTP 测试覆盖取消、超时、超限、3xx/429/5xx 及日志与错误不含令牌。
- [ ] 2.3 实现 `weiboDecimal` 双形态字段解析；单元测试覆盖字符串与整数同值等价、超出 float64 精度的大整数、负号仅 expire_in 允许、浮点/指数/布尔/null/对象/超长拒绝。
- [ ] 2.4 实现令牌响应解析与错误分类：严格解析拒绝重复键与尾随内容；非 200 带 error_code 及 200 带 error_code 均按 design 表映射 21314/21315/21316/21317/21319/21327/21332、10005/10014/21321 与未知码；appkey 不符、expire_in 为负、缺字段分别映射；先核对官方错误码页面并以合成夹具固化，测试另一已登记 AppKey 的令牌返回 IDP_APP_NOT_ALLOWED。
- [ ] 2.5 实现 `uid:<uid>` subject、`ExpireTime` 与 `PolicyFingerprint`；测试两个 AppKey 同 uid 得到同一 subject、Identity 不含 UnionID/OpenIDs、列表重排指纹不变而应用或地址变化指纹改变。

## 3. 资料查询

- [ ] 3.1 实现 `2/users/show.json` 查询（同一令牌、已验证 uid）及 screen_name、avatar_hd→avatar_large→profile_image_url 首个非空值映射；测试 id/idstr 与 uid 一致性校验、仅读取这些字段。
- [ ] 3.2 实现头像 HTTPS 限制：选中字段非 HTTPS 时丢弃头像不回退下一字段，HTTPS 地址仍经既有 `providerProfileFrom` 净化；单元测试覆盖 http、无 scheme、HTTPS 超长或非法字符。
- [ ] 3.3 实现 3 秒独立资料预算与失败放行：error_code（含限流码）、429/5xx、超时、解析失败、id 不符记脱敏告警与 `weibo_profile_unavailable` 并返回无资料身份；正常缺字段与 HTTP 头像丢弃不计降级；外层取消返回 `ctx.Err()`；测试逐项验证并断言返回 `idp.Identity` 与日志不含令牌或资料原文。
- [ ] 3.4 更新 `user/idp` 包注释说明微博提供方，验证 `go vet ./user/idp/...` 与该包测试通过。

## 4. 补填集合与领域行为

- [ ] 4.1 `refillsProfileOnSignIn` 集合加入 WEIBO 并更新注释；支付宝与 QQ 既有单元与数据库测试不改动且通过。
- [ ] 4.2 为微博补充真实数据库测试：首次成功、首次资料失败后重登补填、按字段补填、非空保留、主动清空后补填、不使用历史 profile 兜底。
- [ ] 4.3 为微博补充 pending 创建/关联既有账号/并发归属/完成重放、用户并发 PATCH、冻结/注销/匿名化竞争及会话后段失败的真实数据库测试，确认完成不访问上游、不保存令牌、不留下部分事务结果。
- [ ] 4.4 验证微博多 App 并发首次登录唯一冲突、普通绑定/解绑、同一令牌重复登录不报重放；更新 `docs/compatibility.md` 的补填集合、uid 公开属性与令牌可重放说明。

## 5. 库装配、服务与观测

- [ ] 5.1 在 `accountkit.go` 装配微博提供方并同步 Enabled/Required 与策略指纹；集成测试验证仅 WEIBO 且 Required=[]、需要邮箱补全、禁用后历史微博身份可列出/解绑但不能登录、配置变更拒绝旧 pending。
- [ ] 5.2 更新 accountsvc 的微博变量读取、显式启用无应用时指出 ACCOUNTKIT_WEIBO_APP_IDS、库错误到变量名的映射与纯第三方启动校验；配置/服务测试验证纯微博无 SMTP 可启动、migrate 未配置可运行、已提供非法配置拒绝，QQ/Facebook/Alipay/OIDC 行为不变。
- [ ] 5.3 追加 `weibo`、`verify_weibo`、`weibo_profile_unavailable` 观测取值、`user/observability.go` 映射及 metricdata 允许列表；测试两次网络请求只计一次验证器调用、资料限流登录成功、令牌失败不计降级、外层取消、标签不含 AppKey/uid/令牌，更新指标文档。
- [ ] 5.4 更新 `docs/go-integration.md`、`docs/accountsvc.md`、README 与 `deploy/accountsvc/.env.example`；核对 app_id 即 AppKey 的说明、不需要 AppSecret，示例不含真实凭据。

## 6. App 接入说明与库侧契约验收

- [ ] 6.1 编写微博 App SDK 接入文档：SDK 登录取得 access_token、提交统一凭证、不提交 uid/refresh_token、令牌保密与可重放风险、uid 公开属性、未审核应用用户数限制与头像仅 HTTPS；核对官方文档链接与配置示例。
- [ ] 6.2 使用正式根门面和 HTTP handler、实际微博 Provider、本地模拟 `get_token_info` 与 `users/show` 及真实 PostgreSQL/Redis，验证登录、本人资料读取、刷新、凭证拒绝、跨应用令牌拒绝及资料失败后补填；`TestWeiboHTTPLoginContract` 实际通过并加入 `scripts/required-tests.txt`。
- [ ] 6.3 经正式 HTTP 入口配置两个 AppKey，验证同一 uid 单账号/身份、字符串与整数响应形态等价及跨 App 空字段补填；`TestWeiboHTTPMultiAppIdentityContract` 实际通过并加入必需清单，不将模拟值描述为真实平台行为。
- [ ] 6.4 执行 `go build ./...`、`go vet ./...`、`go test -count=1 ./...`（含 SERVER_TEST_DB_DSN）、`bash scripts/check-generated.sh` 与 `openspec validate --all --strict`，并把实际结果、宿主联调清单（真机 SDK、get_token_info 字段类型与 appkey、users/show 权限与限流、头像协议）的未执行状态写入 `verification.md`。

## Workflow follow-up

- 归档前核对主规格中 ALIPAY、FACEBOOK、QQ 文本未被本变更 delta 回退。
