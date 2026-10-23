# 微信小程序与固定身份识别 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 两组微信应用共享全局固定 UnionID/OpenID 模式，接入小程序并保存白名单元数据。

**Architecture:** 单一 WECHAT Provider 根据服务端 AppID 配置分派接口。subject 固定编码，现有领域查询及唯一约束继续生效；元数据不用于身份查找。

**Tech Stack:** Go、pgx、PostgreSQL、httptest、现有 OpenSpec。

**Spec:** [设计](../../../openspec/changes/add-wechat-mini-program-sign-in/design.md)、[规格](../../../openspec/changes/add-wechat-mini-program-sign-in/specs/wechat-sign-in/spec.md)。

## Global Constraints
- 无 DDL/迁移/生成代码修改；无自动升级、降级、账号合并或历史数据转换。
- 默认 UNIONID；subject 为 unionid:<unionid> 或 openid:<appid>:<openid>。
- 凭据仅在服务端验证；元数据只保存验证后的标识与已有资料，不存秘密。
- 由 develop 创建的任务分支原地实施；不提交、推送或归档。

## Review Focus
- 小程序请求参数 js_code 和 OAuth code 不能互换：Task 1 覆盖路径及参数。
- 模式/分组变化必须使 pending 失效，顺序与 Secret 变化不改变指纹：Task 1/3 覆盖。
- 仅小程序服务不能错误要求 OAuth 配置；管理员 aud 同样隔离：Task 2 覆盖。
- 已有 provider_meta.profile 不能因增加 UnionID 而丢失：Task 3 覆盖创建与合并。
- 元数据冲突不得创建新会话或修改已有身份：Task 3 数据库回滚断言。

### Task 1: 微信验证器与指纹

**Files:** user/idp/wechat.go、policy.go、idp.go 及关联测试。
**Interfaces:** 新增 WeChatSubjectMode string、WeChatSubjectModeUnionID、WeChatSubjectModeOpenID；WeChatConfig{Apps, MiniProgramApps []App, SubjectMode WeChatSubjectMode, BaseURL string}；NewWeChatWithConfig(WeChatConfig,*http.Client,*slog.Logger)(*WeChat,error)；Identity.UnionID string。保留旧 NewWeChat 签名并以默认模式装配。
- [x] 先写行为测试并运行，确认缺少接口/前缀/指纹功能失败；测试组合 OAuth/小程序 × UNIONID/OPENID、缺 ID、unknown app、无重定向、错误日志无秘密、重复 AppID、非法模式、colon AppID 拒绝。
- [x] 实现上述接口；新构造器配置错误不回显敏感值；固定模式选 subject，返回 UnionID 与 OpenIDs。小程序无 session_key 持久化或额外用户资料调用。
- [x] 指纹包括模式、应用分组、base URL，不包括 Secret，排序稳定；完善公共注释。
- [x] GOWORK=off go test ./user/idp -count=1；审查通过后报告改动与证据，不提交。

### Task 2: 配置及 accountsvc 装配

**Files:** config.go、accountkit.go、internal/accountsvc/config.go、关联测试、README.md、docs/accountsvc.md、deploy/accountsvc/.env.example。
**Interfaces:** 消费 Task 1 新构造器；根包 alias 导出同名 WeChatSubjectMode 类型和两个常量。Config 新增 WeChatMiniProgramApps []WeChatApp 与 WeChatSubjectMode。
- [x] 先写配置测试：自定义 prefix、默认 UNIONID、仅小程序、非法模式含显式空、跨组重复、Secret 冒号、秘密不回显、管理员 aud 小程序隔离；运行失败证据。
- [x] 更新 applyDefaults、Validate、ConfigFromEnv、providerKinds、identityProviders；两组只注册一个 WECHAT；根包模式零值按 UNIONID。ConfigFromEnv 显式空拒绝。
- [x] 更新 accountsvc 仅小程序启用检查、管理员 aud 检查和错误字段映射。补真实 httptest 微信验证器装配测试，避免仅检查字段。
- [x] 更新环境配置和请求示例（kind/credential）；GOWORK=off go test . ./internal/accountsvc -count=1，审查差异，不提交。

### Task 3: 元数据和数据库验证

**Files:** user/service_idp.go、service_identity.go、关联测试、docs/compatibility.md、scripts/required-tests.txt。
**Interfaces:** 消费 Identity.UnionID；providerMeta 增加 UnionID string json:unionid,omitempty，统一合并 helper 在 create/refresh 使用。
- [x] 先写测试：首次 UnionID 保存、OPENID 后补 UnionID subject 不变、missing 保留旧值、profile 保留、UnionID/OpenID 冲突返回 ErrIdentityConflict、事务回滚、并发相同 subject 单身份、绑定和 pending 持久化。
- [x] 实现元数据合并，保留同类上限/事务/审计/来源规则；不通过 JSON 查找登录；fake provider 使用明确带前缀 subject 的新用例。
- [x] 运行真实 PostgreSQL 相关测试；如新增必需顶层集成测试，维护 required-tests；补裸 UnionID 与模式切换兼容说明。
- [x] 运行 GOWORK=off go build ./...、go vet ./...、go test -count=1 ./...；OpenSpec validate --all --strict；独立审查并修复。
- [x] verification.md 记录实际命令、结果、环境限制，完成后才更新 tasks。
