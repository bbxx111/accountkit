# Design

## Context

见 proposal.md。当前统一 Provider 注册支持 WECHAT；微信验证器维护 AppID/Secret 映射并仅调用 OAuth。subject 通过现有精确查询和唯一索引查找。provider_meta 有 openids/profile，登录补全加密保存 idp.Identity。

## Goals / Non-Goals

**Goals:** 使用同一个 WECHAT 提供方承接两类应用；全局固定模式；所有已验证身份标识在创建和后续登录中保存；库和 accountsvc 配置一致。

**Non-Goals:** 自动升级、降级、关联查找登录、账号合并、额外用户信息抓取、动态模式切换、数据库 DDL、数据迁移、发布或归档。

## Approaches

已讨论应用类型+JSON、多组环境变量、每应用 SubjectMode 和自动 UnionID 升级。用户选择两组简单凭据环境变量和全局模式；自动升级需要额外别名唯一性与冲突合并设计，当前明确不做。保留 OpenID 只用于元数据与人工排查。

## Decisions

- Config 新增 WeChatMiniProgramApps []WeChatApp、WeChatSubjectMode；根包导出模式类型与常量（可 alias idp 类型），默认 UNIONID。沿用 ParseWeChatApps，Secret 中冒号不受影响；显式空模式与未知模式拒绝。
- idp 新增 WeChatSubjectMode string（UNIONID/OPENID）、WeChatConfig{Apps, MiniProgramApps []App, SubjectMode WeChatSubjectMode, BaseURL string} 和 NewWeChatWithConfig(config WeChatConfig, hc *http.Client, logger *slog.Logger) (*WeChat, error)。保留旧 NewWeChat 签名，默认 OAuth/UNIONID；生产 Config 走新构造器。新配置错误不回显字段值或秘密。
- idp.Identity 新增 UnionID string；微信验证器仅提取两个 ID，不解码/持久化秘密，subject 加类型前缀。约定同实例配置的 UnionID 应用来自同一开放平台，跨平台命名空间不在本轮范围。OpenID 编码要拒绝包含分隔符的 AppID，确保无二义性。
- providerMeta 新增 UnionID 字段；复用单一 merge helper 用于创建/刷新，非空 ID 冲突返回现有身份冲突错误，不能覆盖。保留已有 profile 净化逻辑；不把微信原始 JSON 放入存储。
- PolicyFingerprint 使用排序后的带组名 AppID 与模式、地址；不纳入 Secret。pending 保持既有指纹检查与加密序列化，模式变化自然使旧流程失效。
- accountsvc 的启用检查、管理员 aud 隔离和配置错误脱敏覆盖小程序组；文档展示当前 kind/credential 请求。

## Risks / Trade-offs

- [subject 前缀改变] → 明示破坏性，旧裸 UnionID 不自动匹配，不写迁移工具；不修改历史 SQL。
- [模式切换产生新身份] → 文档要求固定模式，pending 指纹阻断混用，禁止隐式升级。
- [OpenID 不跨应用] → 明确按 AppID 隔离，保留同类身份上限和显式绑定契约。
- [第三方返回异常] → 沿用限时 HTTP、禁止重定向和错误分类；错误日志不记第三方 errmsg 原文。
- [环境验证不可用] → 单元与真实 PostgreSQL 验证分别报告，不以 skip 声称通过。

## Migration Plan

无 DDL，无旧数据转换。新部署设置两组凭据和固定模式；现有 Go 位置式 Config/Identity 初始化可能需要改为字段名初始化。旧 NewWeChat 保留签名但返回新前缀 subject，升级者必须审查身份数据兼容性。回退不会自动转换新格式。
