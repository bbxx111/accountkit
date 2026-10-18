## Context

动机和变更范围见 [proposal.md](proposal.md)，验收契约见 [验证码投递增量规格](specs/verification-delivery/spec.md)。

当前 user/sender/sender.go 已定义窄消息模型，但类型名泛化；user/service_signin.go 的 sendCodeUntil 统一处理占额、签发轮次、选择渠道及投递失败清理。user/sender/smtp/smtp.go 同时执行用途到中文的映射、正文拼接、MIME 编码和网络投递。internal/accountsvc/run.go 使用 smtp.New(cfg.SMTP)。旧宿主存在仅实现 SendSMS/SendEmail 的集成，公开 Deps 字段和原 SMTP 构造入口均需继续工作。

本变更涉及公共类型、可选传输实现与服务装配，具有兼容和隐私边界，因此需要本设计。核心 accountkit 仍不依赖 SMTP，也不管理宿主语言策略。

## Goals / Non-Goals

**Goals:**

- 用精确类型名表达验证码边界，同时保持原接口的方法集、参数类型兼容及 Deps 字段。
- 允许宿主复用 SMTP 编码和传输，独立控制本地化、模板和品牌；允许服务商托管模板继续直接实现发送器。
- 为渲染输入、输出、取消、并发和错误建立可验证的最小契约。

**Non-Goals:**

- 不合并两个渠道为 CodeDeliverer，不增加通知、审计订阅、消息队列或重试机制。
- 不增加 accountsvc 的语言环境变量、HTTP 语言协商、模板上传或管理能力。
- 不修改认证用途、轮次、限流、错误码、数据库或 Redis 格式；不移动 user/sender 包路径。
- 不提供通用模板引擎、任意邮件发送 API、附件、抄送或供应商参数容器。

## Decisions

### 1. 精确命名与类型别名

在 sender 包声明以下正式类型：

```go
type CodeMessage struct {
    Purpose enum.CodePurpose
    Code    string
    TTL     time.Duration
}

type SMSCodeSender interface {
    SendSMS(context.Context, string, CodeMessage) error
}

type EmailCodeSender interface {
    SendEmail(context.Context, string, CodeMessage) error
}

// Deprecated: use SMSCodeSender.
type SMSSender = SMSCodeSender

// Deprecated: use EmailCodeSender.
type EmailSender = EmailCodeSender

// Deprecated: use CodeMessage.
type Message = CodeMessage
```

保留 accountkit.Deps.SMSSender / EmailSender、user.Deps.SMS / Email 的字段名，只更新其类型声明和文档。仓库当前实现、示例和普通测试采用新名称，兼容测试保留旧名称。历史归档、源基线和历史文档不批量替换。

方法名继续为 SendSMS / SendEmail，避免两个签名相同的 SendCode 接口在 Go 结构化类型系统中变为等价。旧名称为别名而不是重新定义的类型，以维持方法签名和赋值兼容。反射或 %T 展示出的声明类型名称不是稳定协议，本次不承诺保持其字符串拼写。

替代方案：统一 CodeDeliverer 会将渠道分派移动到宿主并引入额外适配，不能直接解决 SMTP 文案问题，因此不采用；删除旧类型或更改 Deps 字段会造成无必要的源码迁移，也不采用。

### 2. 渲染扩展属于可选 SMTP 包

在 user/sender/smtp 定义：

```go
type EmailContent struct {
    Subject string
    Text    string
    HTML    string
}

type Renderer interface {
    Render(context.Context, sender.CodeMessage) (EmailContent, error)
}

func NewWithRenderer(cfg Config, renderer Renderer) (*Sender, error)

type LegacyChineseRenderer struct{}
```

NewWithRenderer 要求有效渲染器，包括拒绝接口包装的 nil 指针或 nil 函数。构造阶段只校验依赖，不调用 Render、不做 I/O。保留现有 Config 结构和 New(Config) 签名，避免新增字段破坏旧非键控字面量或修改函数类型；New 内部委托 NewWithRenderer(cfg, LegacyChineseRenderer{})。

兼容文案移至独立 legacy_renderer.go，传输路径只接收渲染结果，不包含中文用途标签、默认正文或模板选择。LegacyChineseRenderer 继续使用原四种用途文案、纯文本格式及正 TTL 分钟数向上取整。accountsvc 显式使用 NewWithRenderer 和该兼容渲染器。新接入文档优先展示自定义渲染方式，旧构造入口仍受支持且无删除时间表。

固定文案仅保留于明确的兼容实现及示例。若供应商使用托管模板，宿主直接实现 EmailCodeSender 或 SMSCodeSender，将 Purpose 映射为模板和参数，不经过 SMTP Renderer，也不要求先生成正文。

### 3. 宿主拥有语言选择

Core CodeMessage 保留现有三个字段，不增加 Locale、模板 ID、品牌或通用 map。宿主可以在构造渲染器时固定语言，也可以在自己的 HTTP 中间件或 Go 调用入口使用私有类型化 context 键注入语言；发送器派生的超时 context 必须保留这些值。

在 examples 下新增一个窄范围、无外部网络依赖的验证码邮件渲染示例，演示中文/英文、缺失或不支持语言的明确默认回退，并覆盖四种用途。宿主模板负责正确表达用途、Code 和 TTL；库不以字符串匹配推断任意语言正文是否语义正确。

渲染器配置在构造后视为不可变，Render 必须支持并发。禁止通过修改共享字段设置“当前语言”。示例只说明宿主选择，不新增核心 context helper，不让库解析 Accept-Language 或读取业务资料。以后若有异步自定义发送器，宿主需在入队时显式快照所需信息；本变更不增加异步实现。

### 4. 渲染、编码与投递顺序

```text
user.Service --> SMSCodeSender --> host SMS provider
             --> EmailCodeSender
                     |
                     +--> SMTP Sender --> Renderer --> MIME --> SMTP
```

SMTP SendEmail 的流程为：

1. 建立一次总超时 context，先检查取消并校验目标、Purpose、非空数字 Code 和正 TTL。
2. 将派生 context 与 CodeMessage 传给 Renderer，同一发送仅渲染一次。
3. 再次检查 context，验证 Subject/Text 非空白，Subject 不含 CR/LF。To 来自规范化目标，From 来自配置，渲染器不能提供任意头部。
4. HTML 为空时使用 UTF-8 text/plain；HTML 非空时编码为 multipart/alternative，先 text/plain 后 text/html，各部分使用合适的 MIME 与 quoted-printable 编码。采用 Go 标准库处理边界和编码。
5. 沿用 TLS、认证、连接取消及 SMTP DATA 接受边界，不重置超时预算；DATA 已接受后 QUIT 失败仍视为成功。

HTML 输出由宿主负责正确转义动态值，示例使用 html/template；库负责邮件传输格式，不作为通用 HTML 清洗器。Text 必需，确保 HTML 邮件有完整验证码的纯文本替代。

Render 是可信宿主回调，必须及时响应 context、避免无界 I/O。库不会通过遗留 goroutine 强制终止不合作的 Go 回调，也不声称能够保证这种回调的硬超时；取消测试使用遵守 context 的实现。

### 5. 失败和敏感信息

渲染错误、非法渲染结果与取消均由 SMTP 适配器转换成固定 sender.ErrUnavailable。不得包装、记录或回显宿主原始渲染错误，不自动回退默认文案，不自动重试。输入和渲染结果校验都在网络连接前完成。

user.Service 继续沿用原有失败路径：只清理匹配当前 code_id 的轮次，保留冷却和额度，记录脱敏 SEND_FAILED，HTTP 返回原 503 映射。普通自定义发送器错误的旧映射保持，不借此统一改写。

宿主渲染器必须避免日志输出验证码、完整地址或正文；库无法控制宿主自行写出的日志，文档明确这一责任。默认兼容渲染器和示例不记录消息内容。

### 6. 验证安排

实现阶段先补行为测试再实现，按以下边界验证：

- 外部测试包编译新旧名称、方法签名及 Deps 字段，旧发送器无需新增方法；兼容接口不要求 Enabled 方法。
- 原四用途中文解码内容、旧构造调用行为、渠道禁用和普通错误映射回归。
- 自定义 Subject/Text/HTML、本地化请求 context 值、默认语言回退、并发语言隔离。
- MIME 解码后内容一致，拒绝头注入、空内容及非法原输入，不因自定义 Renderer 绕过 Code/TTL 检查。
- 渲染失败/取消时零 SMTP 连接，固定安全错误、额度不退、失败清理不影响新轮次。
- 保留 TLS 证书、STARTTLS、AUTH、总预算以及 DATA 后 QUIT 失败测试。
- accountsvc 显式渲染器装配及 EMAIL 禁用/migrate 不引入依赖。

按实际修改范围执行 GOWORK=off 下 build、vet、相关测试与全套单元测试；涉及发送失败清理的路径以现有 Redis/数据库集成和并发验证为依据，按开发文档准备一次性资源。发布前执行完整验证入口；缺资源时如实记未验证，不把 skip 当作通过。结果与审查结论统一记录到 [verification.md](verification.md)。

## Risks / Trade-offs

- 两组名称暂时并存 → 文档优先新名称，旧符号以 Deprecated 注释兼容，避免引入重复 Deps 字段。
- 兼容中文渲染器仍随可选 SMTP 包分发 → 保留旧调用行为；固定文案集中在兼容文件，新构造入口不隐式选用它，核心无文案依赖。
- 宿主 context 约定没有跨项目统一语言字段 → 由宿主管理类型化键及回退，示例验证并发隔离；跨进程元数据标准化留待明确需求。
- 自定义内容可能误述用途或有效期 → 明确宿主契约，示例和默认实现覆盖四用途；不伪称传输层能验证任意语言语义。
- HTML 增加编码分支 → 限定纯文本与 text/plain + text/html 两种形式，用解析后的行为验证避免依赖随机 MIME 边界。
- 宿主回调不响应取消 → 明确合作式取消责任；不增加无限后台 goroutine 或伪造总超时保证。

## Migration Plan

1. 增加正式类型及旧别名，更新当前代码引用，保留兼容测试；现有宿主无需同步升级名称。
2. 增加 NewWithRenderer 和独立兼容渲染器，保留旧 New 与 Config；将 accountsvc 改为显式装配，保持原部署邮件行为。
3. 新宿主或希望定制内容的旧宿主提供 Renderer 并选择新入口；仅需更精确命名的宿主可独立替换类型引用。
4. README、兼容指南和 accountsvc 文档说明命名迁移、语言责任、渲染错误及新旧构造入口。后续需删除兼容入口时另行提出变更，本提案不预设删除。
5. 本变更无数据迁移。回退到旧版本时，新类型和 NewWithRenderer 的调用者需同步回退源码；只使用原接口的宿主保持原构建方式。自定义本地化内容不能在旧二进制中自动保留，回退部署前需确认这一展示差异。
