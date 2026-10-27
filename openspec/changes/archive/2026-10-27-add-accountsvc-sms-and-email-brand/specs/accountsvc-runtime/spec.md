## ADDED Requirements

### Requirement: 服务短信服务商配置

serve SHALL 读取可选的短信服务商配置。`ACCOUNTSVC_SMS_PROVIDER` 首版只接受 `aliyun`，未设置或为空表示未配置。选择 `aliyun` 时 SHALL 要求以下配置：

- AccessKey ID 与 AccessKey Secret；
- 签名名称；
- SIGN_IN、BIND、REAUTH、SIGN_IN_COMPLETE 四个模板 code。

endpoint 可选覆盖，仅供测试或代理部署使用。任一必填项缺失或非法时，serve SHALL 启动失败并指出变量名，不回显值。未知服务商名称 SHALL 拒绝启动。AccessKey Secret SHALL 只通过环境或密钥注入提供，不写入镜像、Compose 文件或日志。配置结构 SHALL 为将来的国际签名与模板预留独立变量，不复用大陆模板变量。

#### Scenario: 完整的阿里云配置
- **WHEN** serve 设置了 provider=aliyun 及全部必填变量
- **THEN** 构造阿里云发送器并注入为短信发送器，不进行网络 I/O

#### Scenario: 配置不完整
- **WHEN** provider=aliyun 但缺少 REAUTH 模板
- **THEN** 启动失败并指出该模板变量名，输出不含 Secret

### Requirement: 服务邮件品牌前缀

serve SHALL 读取可选的 `ACCOUNTSVC_SMTP_BRAND`，并传给内置中文渲染器。未设置或为空时邮件文案 SHALL 不变。值不合法时（控制字符、换行、超过 32 个码位或首尾空白），serve SHALL 启动失败并指出该变量。EMAIL 被显式禁用时 SHALL 不解析该变量。

#### Scenario: 设置品牌
- **WHEN** serve 设置 `ACCOUNTSVC_SMTP_BRAND=食方` 并发送登录验证码邮件
- **THEN** 邮件标题为“食方登录验证码”

## MODIFIED Requirements

### Requirement: 服务身份策略与发送能力

serve SHALL 读取 ACCOUNTKIT_ENABLED_IDENTITY_KINDS 和 ACCOUNTKIT_REQUIRED_IDENTITY_KINDS，对显式策略执行子集与依赖校验；未设置时保留原默认装配。EMAIL 显式禁用时 SHALL 不要求或解析 SMTP 设置。显式启用 PHONE SHALL 要求已配置短信服务商；未配置时 SHALL 拒绝启动，不使用日志或伪发送器替代。省略 ENABLED 时，即使配置了短信服务商，短信渠道也 SHALL 保持显式禁用，不自动启用 PHONE。显式启用 GOOGLE 时 SHALL 要求至少一个 client ID，缺失时指出 `ACCOUNTKIT_GOOGLE_CLIENT_IDS`。

#### Scenario: 身份集合与服务发送能力一致
- **WHEN** serve 读取显式身份策略
- **THEN** 校验集合及运行依赖；EMAIL 禁用时不解析 SMTP；PHONE 启用时须已配置短信服务商，否则拒绝启动；直接嵌入库仍可注入真实发送器

#### Scenario: 配置短信服务商但未显式启用 PHONE
- **WHEN** serve 配置了阿里云短信，但省略 ACCOUNTKIT_ENABLED_IDENTITY_KINDS
- **THEN** 身份策略与短信渠道均保持原默认装配，发送短信验证码返回 `CHANNEL_NOT_ENABLED`

#### Scenario: 启用 Google 但未配置客户端
- **WHEN** serve 的显式 Enabled 包含 GOOGLE，且 Google client ID 为空
- **THEN** 启动失败并指出 `ACCOUNTKIT_GOOGLE_CLIENT_IDS`

### Requirement: 生产发送与内省凭据要求

production SHALL 在 EMAIL 启用时要求真实 SMTP 与 SMTP TLS，在 PHONE 启用时要求真实短信服务商，并始终要求内省调用凭据。production SHALL 不使用日志验证码、固定默认秘密或明文邮件降级；HTTP TLS 开关不得使生产 SMTP 降级。短信 endpoint 在 production SHALL 只接受 HTTPS。

#### Scenario: 生产发送和内省依赖不可降级
- **WHEN** production 缺少必需的真实 SMTP/TLS、短信服务商或内省调用凭据
- **THEN** 拒绝启动，不用日志验证码、默认秘密或明文 SMTP 替代；显式关闭 HTTP TLS 不豁免 SMTP TLS

### Requirement: migrate 静态校验与依赖边界

migrate SHALL 只执行静态身份策略语法/集合校验及迁移必要检查，不监听、不构造认证处理器，不要求发送器或第三方运行时依赖。migrate SHALL 不要求或解析服务监听地址、HTTP TLS 开关、证书、SMTP、短信服务商、邮件品牌或内省客户端配置；迁移锁与密钥版本检查仍执行。

#### Scenario: 迁移独立于服务运行配置
- **WHEN** migrate 的身份集合合法且迁移必要依赖有效，未提供服务运行配置
- **THEN** 迁移可执行，不读取无关监听/TLS/发送/短信/内省设置，仍核对迁移锁和存储引用密钥
