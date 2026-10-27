## Context

动机见 proposal.md。现状：

- `internal/accountsvc/config.go` 在显式启用 PHONE 时直接拒绝；`run.go` 中短信发送器固定为 `sender.Disabled{}`。
- 库已有可选的发送器能力约定：发码流程在 `issueChallenge`（占用轮次、冷却、额度）之前，检查发送器是否实现 `Enabled() bool`。关闭时返回 `sender.ErrDisabled`，HTTP 层为 400 `CHANNEL_NOT_ENABLED`。
- 发送失败统一为 `sender.ErrUnavailable`，服务按既有契约返回 503，已占额度不退。
- `smtp.LegacyChineseRenderer` 是无字段的结构体，标题为“<用途>验证码”。
- ai-food 已确认以下取舍（2026-10-27，经用户确认）：
  - 首版只发中国大陆号码，非 +86 号码在占用额度之前返回 400；
  - 服务商频控按 503 处理，不映射为 429；
  - 四个用途使用不同模板（服务允许相同），模板变量只有 `${code}`；
  - 只有一个签名名称；
  - 品牌限制在 32 个字符以内。

## Goals / Non-Goals

**Goals：**
- accountsvc 可以通过阿里云发送手机验证码，配置错误时启动失败并给出明确信息。
- 不支持的号码在消耗额度之前被拒绝，客户端能区分“号码非法”和“号码地区不支持”。
- 嵌入式宿主可以直接复用阿里云发送器与号码支持能力。

**Non-Goals：**
- 不做国际或港澳台短信、多签名、多服务商路由或故障转移。
- 不引入阿里云 SDK，不做模板管理或多语言模板。
- 不改变发码限流、冷却、额度或验证码校验的语义。
- 不在本地对真实阿里云账号做发送验收；这需要真实凭据与已审核的模板，由部署方验收。

## Decisions

### D1 新增可选包 `user/sender/aliyun`，与 `user/sender/smtp` 平级

服务商实现放在库的可选子包中，accountsvc 与嵌入式宿主共用，库的核心不依赖它，与 SMTP 的先例一致。构造入口为 `aliyun.New(Config) (*Sender, error)`，`Config` 字段如下：

- `AccessKeyID`、`AccessKeySecret`、`SignName`；
- `Templates map[enum.CodePurpose]string`，四个用途必填；
- `Endpoint`，默认 `https://dysmsapi.aliyuncs.com/`；
- `HTTPClient`；
- `Timeout`，默认 10 秒。

### D2 使用 RPC 风格签名（HMAC-SHA1），以官方示例作为固定向量

SendSms（API 版本 2017-05-25）支持 RPC 风格签名，ai-food 现有实现即采用这种方式并在使用中，选它可以与已验证的行为对齐。所有参数加上公共参数（`SignatureMethod=HMAC-SHA1`、`SignatureVersion=1.0`、随机 `SignatureNonce`、UTC `Timestamp`、`Format=JSON`），按 RFC 3986 规则编码、排序后计算签名，以表单 POST 发送。

签名实现必须以阿里云官方文档的签名示例作为固定测试向量；实施时须核对官方原文，不得凭记忆写入期望值。若当时无法访问文档，暂停实施并请用户提供示例。

备选是 V3 签名（ACS3-HMAC-SHA256），更新、更安全，但在当前用法下没有实际收益，且与已验证的实现不一致，可以作为后续独立改进。

### D3 号码支持能力：发送器上的可选方法

在库中新增可选能力 `interface{ SupportsTarget(e164 string) bool }`，由发码流程在 `Enabled()` 检查之后、`issueChallenge` 之前调用；不支持时返回新的错误 `sender.ErrTargetNotSupported`。HTTP 层映射为 400 `FAILED_PRECONDITION` / `TARGET_NOT_SUPPORTED`，与 `CHANNEL_NOT_ENABLED` 属于同类“部署能力不满足”。审计原因为 `TARGET_NOT_SUPPORTED`；指标归入既有的参数拒绝分类，不新增标签值，因此不改变指标规范。

阿里云发送器只对 `+86` 后 1 开头的 11 位手机号返回 true（审查后补充：排除同长度的固定电话，避免占用额度后才被服务商拒绝）。宿主的装饰器须转发 `SupportsTarget`，否则预检失效。`SendSMS` 自身也会做同样的检查，防止宿主绕过发码流程直接调用。

备选方案及否决理由：
- 复用 `INVALID_TARGET`：客户端无法区分号码写错与地区不支持。
- 在号码规范化阶段按配置的地区拒绝：会把发送器的属性泄漏进身份规范，与“不要写死在协议里”的要求冲突。

### D4 错误分类与隐私

只有 HTTP 200 且 `Code=OK` 才算成功。其余情况统一包装为 `sender.ErrUnavailable`，包括业务错误码、频控、网络错误、超时、非 JSON 与超限响应，响应体上限 64 KiB，不跟随重定向，不自动重试。错误链只附加阶段、HTTP 状态、服务商 `Code` 与 `RequestId`，不包含 `Message` 原文、手机号、验证码、Secret 或签名串。审查后补充：服务的发码流程会把投递错误收敛为通用 `ErrUnavailable`，不记录宿主发送器的错误文本（宿主错误可能含敏感信息），因此阿里云发送器自带可选 `Logger`，由它以 Warn 记录上述脱敏信息，accountsvc 注入服务日志器；非 200 响应（如 `SignatureDoesNotMatch`）的 JSON 错误码同样被解析。调用方取消保持 `context.Canceled` 语义。

### D5 accountsvc 环境变量

| 变量 | 说明 |
|---|---|
| `ACCOUNTSVC_SMS_PROVIDER` | 空或 `aliyun` |
| `ACCOUNTSVC_SMS_ALIYUN_ACCESS_KEY_ID` / `_ACCESS_KEY_SECRET` | 必填（provider=aliyun 时） |
| `ACCOUNTSVC_SMS_ALIYUN_SIGN_NAME` | 必填 |
| `ACCOUNTSVC_SMS_ALIYUN_TEMPLATE_SIGN_IN` / `_BIND` / `_REAUTH` / `_SIGN_IN_COMPLETE` | 必填 |
| `ACCOUNTSVC_SMS_ALIYUN_ENDPOINT` | 可选；production 只接受 HTTPS |

将来的国际短信使用独立的 `..._INTL_SIGN_NAME`、`..._INTL_TEMPLATE_*` 变量，不复用上述大陆变量；本次不实现。装配规则：

- 只有显式 Enabled 包含 PHONE 且已配置服务商时，才注入阿里云发送器；其余情况保持 `sender.Disabled{}`。
- 配置了服务商但 PHONE 未启用时，发送器不被使用。
- migrate 不解析这些变量。

### D6 品牌前缀

`LegacyChineseRenderer` 增加可选字段 `Brand string`，零值行为不变，现有的 `LegacyChineseRenderer{}` 字面量保持兼容。新增 `smtp.ValidateBrand(string) error`，供配置阶段调用；`smtp.NewWithRenderer` 对内置渲染器的品牌在构造时校验（审查后补充），嵌入式宿主不会在占用额度后才失败。品牌另拒绝格式字符与行/段分隔符，防止标题伪装。`Render` 在品牌不合法时返回 `sender.ErrUnavailable`，作为最后防线，不输出未经校验的值。

- 标题：`brand + purpose + "验证码"`；
- 正文：首行前加“【brand】”。

accountsvc 读取 `ACCOUNTSVC_SMTP_BRAND`，校验后传入渲染器。

## Risks / Trade-offs

- [本地无法用真实阿里云验证签名与模板] → 用官方示例向量固定签名算法，用本地假服务验证请求参数；真实发送由部署方在接入时验收，并写入验收记录。
- [阿里云频控与库内冷却叠加，用户在限额内仍可能收到 503] → 503 是既有发送失败契约；运维可以通过日志中的服务商错误码定位。
- [新错误原因 `TARGET_NOT_SUPPORTED` 属于新增的 HTTP 契约] → 只在发送器声明不支持时出现，旧宿主不受影响；写入 OpenAPI 与兼容说明。
- [非 +86 用户无法使用手机号登录] → 首版产品取舍，已由用户确认，可以改用邮箱或第三方登录；配置结构已为国际短信预留位置。

## Migration Plan

新增配置均为可选，未设置时行为不变，无数据库迁移。启用步骤：配置阿里云的 AccessKey、签名与四个模板，在 `ACCOUNTKIT_ENABLED_IDENTITY_KINDS` 中显式加入 PHONE，然后重启。回滚时移除 PHONE 与短信配置即可。
