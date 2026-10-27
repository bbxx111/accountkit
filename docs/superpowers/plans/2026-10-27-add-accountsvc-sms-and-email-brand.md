# accountsvc 阿里云短信与邮件品牌 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** accountsvc 通过阿里云发送中国大陆手机号验证码，不支持的号码在占用额度前被拒绝，邮件可加品牌前缀。

**Architecture:**
- 库新增可选的发送器号码支持能力（`SupportsTarget`）与 `sender.ErrTargetNotSupported`，发码流程在 `issueChallenge` 前检查。
- 新增可选包 `user/sender/aliyun`，使用 RPC v1 HMAC-SHA1 签名的 SendSms。
- accountsvc 解析 `ACCOUNTSVC_SMS_*` 并按 PHONE 规则装配。
- `smtp.LegacyChineseRenderer` 新增 `Brand` 字段。

**Tech Stack:** Go 1.26 标准库（crypto/hmac、crypto/sha1、net/http），不新增依赖。

**Spec:** `openspec/changes/add-accountsvc-sms-and-email-brand/`（proposal.md、design.md、specs/、tasks.md）

## Global Constraints

- 首版只发 `+86` 号码；PhoneNumbers 为去掉 `+86` 后的 11 位号码。
- 签名：`Base64(HMAC-SHA1(Secret+"&", Method&%2F&enc(sorted query)))`，编码保留 `A-Za-z0-9-_.~`，空格为 `%20`，`*` 为 `%2A`。
- 官方固定向量（已在本地独立复算一致）：
  - 输入：GET、AccessKeyId=testid、Secret=testsecret、Action=DescribeDedicatedHosts、Version=2014-05-26、Format=JSON、RegionId=cn-beijing、SignatureMethod=HMAC-SHA1、SignatureNonce=edb2b34af0af9a6d14deaf7c1a5315eb、SignatureVersion=1.0、Timestamp=2023-03-13T08:34:30Z；
  - 结果：Signature=`9NaGiOspFP5UPcwX8Iwt2YJXXuk=`。
- SendSms 参数：Action=SendSms、Version=2017-05-25、RegionId=cn-hangzhou、Format=JSON、PhoneNumbers、SignName、TemplateCode、TemplateParam=`{"code":"…"}`；以 POST 表单发送，Signature 放在表单中。
- 成功条件：HTTP 200 且 JSON `Code=="OK"`。其余一律返回 `sender.ErrUnavailable`，错误链只带阶段、`Code` 与 `RequestId`。响应上限 64 KiB，不重定向，不重试，默认超时 10 秒。
- HTTP 错误：400 `FAILED_PRECONDITION` / `TARGET_NOT_SUPPORTED`；审计原因 `TARGET_NOT_SUPPORTED`；观测归入既有的 `ReasonInvalidArgument`。
- 品牌：1 至 32 个码位，无控制字符，无首尾空白。标题为 `brand+purpose+"验证码"`，正文首行前加 `【brand】`；空品牌时输出逐字不变。
- 环境变量：`ACCOUNTSVC_SMS_PROVIDER`（空或 `aliyun`）、`ACCOUNTSVC_SMS_ALIYUN_{ACCESS_KEY_ID,ACCESS_KEY_SECRET,SIGN_NAME,TEMPLATE_SIGN_IN,TEMPLATE_BIND,TEMPLATE_REAUTH,TEMPLATE_SIGN_IN_COMPLETE,ENDPOINT}`、`ACCOUNTSVC_SMTP_BRAND`；migrate 不解析。

## Review Focus

- 非 +86 号码必须在占用轮次、冷却、额度之前被拒绝，四种用途都适用（Task 1 数据库测试）。
- 服务商频控必须是 503，不能是 429（Task 2 错误测试，Task 3 进程测试）。
- 任何错误或日志都不能出现手机号、验证码、Secret 或服务商 Message 原文（Task 2）。
- 省略 Enabled 时，即使配置了短信，短信渠道仍保持禁用（Task 3）。
- 品牌中的 CR/LF 必须在配置阶段被拒绝，空品牌时邮件逐字不变（Task 4）。

---

### Task 1: 号码支持能力（tasks 1.1–1.2）

**Files:** `user/sender/disabled.go`（新增错误）、`user/service_signin.go`（`sendCodeUntil`）、`user/observability.go`（分类）、`httpapi/enduser/response.go`（映射）。测试：`user/service_target_support_test.go`（数据库）、`httpapi/enduser` 响应测试。

**Interfaces:**
- `sender.ErrTargetNotSupported`；
- 可选能力 `interface{ SupportsTarget(e164 string) bool }`。

- [ ] 测试：注入实现 `SupportsTarget` 的计数假发送器，对登录、绑定、重新认证、补全四种发码分别断言：返回 `ErrTargetNotSupported`；`code` 存储无新键、冷却或额度；发送计数为 0；审计原因为 `TARGET_NOT_SUPPORTED`。另断言 +86 号码正常发送，未实现该能力的发送器不受影响。
- [ ] 实现：在 `Enabled()` 检查之后加入以下逻辑：

```go
if channel == enum.IdentityPhone {
	if capability, ok := delivery.(interface{ SupportsTarget(string) bool }); ok && !capability.SupportsTarget(norm) {
		base.Type, base.Result, base.Reason = enum.EventCodeSendRejected, enum.ResultFailure, "TARGET_NOT_SUPPORTED"
		s.record(ctx, base)
		return CodeChallenge{}, sender.ErrTargetNotSupported
	}
}
```

  `response.go` 映射为 `apierror.New(apierror.StatusFailedPrecondition, "TARGET_NOT_SUPPORTED", "verification target is not supported by this deployment")`；`observability.go` 归入 `ReasonInvalidArgument`。
- [ ] 提交 `feat(user): reject unsupported SMS targets before issuing codes`。

### Task 2: 阿里云发送器（tasks 2.1–2.4）

**Files:** 新建 `user/sender/aliyun/aliyun.go`、`sign.go`，测试 `aliyun_test.go`、`sign_test.go`。

**Interfaces:**
- `type Config struct { AccessKeyID, AccessKeySecret, SignName string; Templates map[enum.CodePurpose]string; Endpoint string; HTTPClient *http.Client; Timeout time.Duration }`
- `func New(Config) (*Sender, error)`
- `(*Sender).SendSMS(ctx, e164, sender.CodeMessage) error`
- `(*Sender).SupportsTarget(e164) bool`
- 内部函数 `sign(method, secret string, params url.Values) string`

- [ ] 测试 `sign_test.go`：使用上面的固定向量，断言结果为 `9NaGiOspFP5UPcwX8Iwt2YJXXuk=`；另断言编码规则：空格编码为 `%20`，`*` 编码为 `%2A`，`~` 不编码。
- [ ] 测试 `aliyun_test.go`：
  - `New` 的缺项与非法 endpoint 用例，错误不含 Secret；
  - 用 httptest TLS 假服务校验：方法为 POST、表单含全部公共参数、PhoneNumbers 为 11 位、TemplateCode 与用途对应、TemplateParam 为 JSON；
  - 假服务按收到的表单复算 Signature 并比对；
  - 非 +86 不发请求；
  - 错误场景：`Code=isv.BUSINESS_LIMIT_CONTROL`、500、302、非 JSON、超过 64 KiB、超时、调用方取消，错误文本不含号码、验证码、Secret 与 Message。
- [ ] 实现，然后提交 `feat(sender): add Aliyun SMS code sender`。

### Task 3: accountsvc 装配（tasks 3.1–3.3）

**Files:** `internal/accountsvc/config.go`（`Config.SMS`、`Config.SMTPBrand` 及解析）、`run.go`（装配）；测试为 `config_test.go`、`run_test.go`，以及新建的 `sms_integration_test.go`（进程级）。

- [ ] config 测试：
  - provider 为未知值；
  - 每个必填变量缺失时都指出变量名；
  - production 下使用 HTTP endpoint 被拒绝；
  - 显式 PHONE 但无服务商时，仍为原错误；
  - 显式 PHONE 且有服务商时，正常启动；
  - 省略 Enabled 但有服务商时，短信保持 Disabled；
  - migrate 不解析 SMS 变量（即使变量非法也通过）。
- [ ] 进程测试：endpoint 指向假服务，PHONE 登录全流程通过；假服务返回频控时为 503；`+1…` 号码返回 400 `TARGET_NOT_SUPPORTED`。
- [ ] 实现，然后提交 `feat(accountsvc): configure Aliyun SMS provider`。

### Task 4: 邮件品牌（tasks 4.1–4.2）

**Files:** `user/sender/smtp/legacy_renderer.go`、`renderer_test.go`、`internal/accountsvc/config.go`、`run.go`。

- [ ] 测试：
  - 空品牌时四种用途的输出与 golden 逐字一致；
  - 品牌为“食方”时，标题为“食方登录验证码”，正文首行为“【食方】您正在进行登录。”；
  - `ValidateBrand` 拒绝 CR/LF、`\t`、`\x00`、33 个码位、首尾空白；
  - `Render` 遇到不合法品牌时返回 `ErrUnavailable`；
  - accountsvc 遇到不合法的 `ACCOUNTSVC_SMTP_BRAND` 时启动失败并指出该变量，EMAIL 禁用时不解析。
- [ ] 实现，然后提交 `feat(smtp): optional brand prefix for built-in Chinese renderer`。

### Task 5: 文档（task 5.1）

更新 `docs/accountsvc.md`、`deploy/accountsvc/.env.example`、README 发送器一节、`docs/openapi/enduser.json`（四个发码端点的 400 `TARGET_NOT_SUPPORTED`）、`docs/compatibility.md`，然后提交 `docs: Aliyun SMS provider and email brand`。

### Task 6: 验证与记录（tasks 6.1–6.3）

依次运行：

1. 连接真实数据库的 `go test`；
2. `check-generated.sh`、`check-migrations.sh`、`openspec validate`；
3. 在 Linux 镜像中运行 `verify.sh` 与 `verify-accountsvc.sh`，使用一次性 Docker 资源；
4. 维护 required-tests 列表；
5. 由一个独立审查代理复核整条分支；
6. 编写 `verification.md`，提交。
