## 1. 号码支持能力（库）

- [x] 1.1 新增 `sender.ErrTargetNotSupported`。在发码流程中，于 `Enabled()` 检查之后、`issueChallenge` 之前，调用可选的 `SupportsTarget(e164)`；不支持时记录审计 `CODE_SEND_REJECTED`，原因为 `TARGET_NOT_SUPPORTED`。验证：数据库测试确认登录、绑定、重新认证、补全四种发码都不产生轮次、冷却或额度变化，发送器未被调用；未实现该能力的发送器行为不变。
- [x] 1.2 HTTP 映射为 400 `FAILED_PRECONDITION` / `TARGET_NOT_SUPPORTED`，观测归入参数拒绝分类，不新增标签值。验证：enduser 响应测试与观测分类测试。

## 2. 阿里云发送器

- [x] 2.1 新建 `user/sender/aliyun`，实现 `Config`、`New`，覆盖必填项、四个用途模板、endpoint 规则与超时默认值。验证：构造测试覆盖缺项、非法 endpoint（非回环 HTTP、userinfo、查询串、fragment），并确认错误不回显 Secret；构造时无网络 I/O。
- [x] 2.2 实现 RPC 签名，以阿里云官方文档的签名示例作为固定向量（先核对官方原文，无法访问时暂停并请用户提供）。验证：固定向量测试通过。
- [x] 2.3 实现 `SendSMS`：只接受 +86 号码并去掉前缀，按用途选择模板，模板参数为 `{"code":...}`，表单 POST，不重定向、不重试，响应上限 64 KiB，并实现 `SupportsTarget`。验证：本地 HTTPS 假服务测试覆盖参数正确性、四种用途、非 +86 不发请求、调用方取消。
- [x] 2.4 错误分类：只有 200 且 `Code=OK` 算成功，其余（含 `isv.BUSINESS_LIMIT_CONTROL`、网络错误、超时、非 JSON、超限、3xx/5xx）返回 `sender.ErrUnavailable`，错误链只带阶段、`Code` 与 `RequestId`。验证：逐项测试，并断言错误与日志不含手机号、验证码、Secret 与 Message 原文。

## 3. accountsvc 装配

- [x] 3.1 解析 `ACCOUNTSVC_SMS_PROVIDER` 与 `ACCOUNTSVC_SMS_ALIYUN_*`：未知服务商、缺少必填项、非法 endpoint 时启动失败并指出变量名，不回显值；production 下 endpoint 只接受 HTTPS；migrate 不解析。验证：config 测试。
- [x] 3.2 修改 PHONE 规则：显式启用 PHONE 时须已配置服务商，否则沿用原拒绝；省略 Enabled 时短信渠道保持禁用；装配时只在 PHONE 已启用时注入阿里云发送器。验证：config 与 run 测试覆盖以上组合。
- [ ] 3.3 进程级集成测试：在 PHONE 启用、endpoint 指向本地假服务时，发码与登录成功；假服务返回频控时为 503；非 +86 号码为 400 `TARGET_NOT_SUPPORTED`。验证：`verify-accountsvc.sh`。

## 4. 邮件品牌前缀

- [x] 4.1 给 `LegacyChineseRenderer` 增加 `Brand` 字段，新增 `smtp.ValidateBrand`；`Render` 遇到不合法品牌时返回 `ErrUnavailable`。验证：渲染测试确认四种用途在空品牌时逐字不变，带品牌时标题与正文首行正确；CR/LF、控制字符、超长、首尾空白被拒绝。
- [ ] 4.2 accountsvc 读取 `ACCOUNTSVC_SMTP_BRAND` 并校验后传入渲染器，EMAIL 禁用时不解析。验证：config 测试与 SMTP 集成测试中的邮件标题。

## 5. 文档

- [x] 5.1 更新 `docs/accountsvc.md`（短信配置、PHONE 启用规则、品牌前缀）、`deploy/accountsvc/.env.example`、README 发送器一节、`docs/openapi/enduser.json` 中发码端点的 `TARGET_NOT_SUPPORTED`，以及 `docs/compatibility.md`。验证：链接检查；`rg TARGET_NOT_SUPPORTED` 在规格、代码、OpenAPI、文档中一致。

## 6. 验证与记录

- [ ] 6.1 运行 `go build`、`go vet`、`go test ./...`（连接真实数据库）、`check-generated.sh`、`check-migrations.sh`、`openspec validate --all --strict`。
- [ ] 6.2 在 Linux 镜像中运行 `verify.sh` 与 `verify-accountsvc.sh`，新增的必需测试同步写入 required-tests 列表。
- [ ] 6.3 编写 `verification.md`，写明真实阿里云发送需由部署方用真实凭据与模板验收。
