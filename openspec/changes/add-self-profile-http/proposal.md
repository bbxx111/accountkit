## Why

用户明确要求 profile_attributes 中的所有内容允许本人完整读取和修改。当前只有宿主 Go 接口，接入者必须重复实现认证、路由和错误映射；accountsvc 也缺少 Schema 装配入口。需要把本人自助资料管理作为内置消费者能力。

## What Changes

- 新增相对路由 GET/PATCH `/users/me/profile`，以认证 Principal 的 UserID 为唯一目标，所有资料属性对本人可读写，继续执行 Schema 和账号生命周期约束。
- 定义独立资料资源及 JSON Merge Patch 请求；返回完整资料，禁止隐式公开到既有用户响应、JWT、内省和管理面。
- 通过可选 `ProfileAttributesService` 接入现有领域方法，不扩大 enduser.Service 必需方法集。
- accountsvc serve 支持可选 `ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE`，启动时有界读取并校验；未配置保持禁用，migrate 不读取该设置。
- **BREAKING（访问契约）**：启用 Schema 且挂载内置消费者 handler，即允许本人访问全部资料；此前依赖“只有宿主 Go 可访问”保存本人不可见/不可改字段的接入者必须在升级前迁出这类数据。Go 方法签名、存储和既有路由响应保持。

## Capabilities

### New Capabilities

- `self-profile-http`: 本人完整资料 GET/PATCH、认证、协议限制、隐私与可选适配器兼容。

### Modified Capabilities

- `accountsvc-runtime`: 新增 serve 的可选资料 Schema 文件装配及 migrate 隔离要求。

## Impact

依赖当前已实施但未归档的 [add-schema-validated-user-extensions](../add-schema-validated-user-extensions/proposal.md)。本变更明确取代其“仅 Go、不新增内置路由”的范围限制；原 Go 调用仍由宿主授权，数据与事务规则继续生效。归档顺序为先基础能力、后本变更；归档时同步调整 user-profile-extensions 主规格的访问说明，不能丢失基础规格。

影响 httpapi/enduser、internal/accountsvc、公开配置注释、README、docs/openapi.md、docs/accountsvc.md、兼容说明和无秘密部署模板。无 SQL、迁移、sqlc、令牌格式或第三方依赖变化。用户随后明确要求“提交，apply”，实施进度与实际验证以 tasks.md、verification.md 为准。
