# 本人资料 HTTP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 内置本人完整资料 GET/PATCH，并让 accountsvc 通过文件启用 Schema。

**Architecture:** HTTP 通过可选能力接口复用 user.Service，只从 Principal 取账号。外层严格、有界解析，内层原始 JSON 保留给领域校验；服务在 LoadConfig 的 serve 分支装配 Schema。

**Tech Stack:** Go 1.26.5、Chi、pgx/v5、PostgreSQL 17，既有 boundedjson/profileattributes。

**Spec:** [设计与契约](../../../openspec/changes/add-self-profile-http/design.md)，同目录 specs 为验收依据。

## Global Constraints

- 路由 GET/PATCH `/users/me/profile`，user scope，no-store 覆盖认证错误，不要求近期重新认证。
- 独立 DTO：name + profile_attributes；PATCH 外层只接受必需的 profile_attributes 对象。内层 16 KiB、深度 8、512 值节点；外层沿用 MaxBodyBytes。
- 所有字段对本人可读写；保留 Schema、ACTIVE/FROZEN 读和 ACTIVE 写约束、账号锁、审计语义。
- 不改 SQL/生成代码、JWT、既有 DTO、必需 Service 方法集；无新依赖。
- GOWORK=off；真实测试资源为本任务创建的一次性容器。提交按本轮授权进行，推送/合并/归档不执行。
- 用户已要求“提交，apply”，沿用项目默认子代理执行，计划形成后继续，不重复请求实施授权。

## Review Focus

- 外层非法 Unicode/重复转义键不能被 encoding/json 宽容解码洗白；Task 1 测试。
- 内层空白不能在原始补丁预算校验前被重编码丢失；Task 1 原始字节测试及 Task 3 实际领域上限测试。
- 认证错误也必须 no-store；Task 1 缺失/无效 token 和 scope 矩阵。
- 文件读取不能无界或让 FIFO 阻塞启动；Task 2 非普通文件与有界读取测试。
- 服务默认启用资料后不能误向 JWT、旧 HTTP 或日志暴露内容；Task 3 集成及隐私回归。

### Task 1: 消费者 HTTP 适配（OpenSpec 2.1—2.4）

**Files:** 新建 httpapi/enduser/profile_attributes.go、profile_attributes_test.go；修改 handler.go 注册路由。需要解析辅助时只新增本域文件，优先复用 internal/boundedjson 的现有严格语义。

**Interfaces:** 消费 user.Service 的 GetProfileAttributes(ctx,userID) 与 PatchProfileAttributes(ctx,userID,user.ProfileAttributesPatch,user.Meta)；产出可选 enduser.ProfileAttributesService，保持原 Service 不变。HTTP DTO 字段为 Name string、ProfileAttributes json.RawMessage。

- [ ] 先写 TestProfileAttributesEndpoint、TestProfileAttributesRequest、TestProfileAttributesErrors 行为矩阵：200 全量/空值、401/403/no-store、503 缺能力/配置、只取本人、无近期认证限制、所有状态错误、400 格式/Schema、404/500；运行 `go test ./httpapi/enduser -run ProfileAttributes -count=1` 确认新路由 404 失败。
- [ ] 实现 GET/PATCH 与专用安全错误映射。读取 MaxBodyBytes 上限，保留内层 json.RawMessage；错误信息固定，不记录原始解析错误。外层语法/字段错误归 MALFORMED_BODY；可解析内层语义错误归 INVALID_PROFILE_ATTRIBUTES。name/profile_attributes DTO 显式构造。
- [ ] 增加重复键、转义键、Unicode、尾随内容、原始空白、16 KiB、自定义外层上限及 request_id 测试，先红再绿；不要用假服务测试领域合并，应在 Task 3 用真实服务覆盖。
- [ ] `go test ./httpapi/enduser -count=1` 与 `go vet ./httpapi/enduser` 通过；记录红绿证据与修改文件，请独立审查。

### Task 2: 服务 Schema 文件配置（OpenSpec 3.1）

**Files:** 修改 internal/accountsvc/config.go；新建 profile_attributes.go、profile_attributes_test.go（该包内）；不修改文档或 HTTP 文件。

**Interfaces:** LoadConfig("serve") 返回 Config.Library.ProfileAttributes；migrate 忽略新设置。辅助 `loadProfileAttributes(path string) (*user.ProfileAttributesConfig, error)`，只返回安全配置错误。

- [ ] 写 TestLoadConfigProfileAttributes：未设置 nil、合法文件载入、空/不存在/目录/超限/非法 Schema 拒绝、migrate 忽略坏路径、修改文件不改变已载入字节。运行新测试确认原代码未加载而失败。
- [ ] 在 migrate 提前返回之后加入文件加载；本地普通文件检查、有界读取 64 KiB+1、既有 ValidateProfileAttributesConfig；错误只含配置项与安全分类，不含路径或原文。保持库 New/ConfigFromEnv 无 I/O。
- [ ] 补读失败和非普通文件测试（OS 支持时 Unix FIFO），超限包含有效前缀测试；验证文件被关闭和只读快照。
- [ ] `go test ./internal/accountsvc -run 'TestLoadConfig|TestProfileAttributes' -count=1` 与该包 vet，通过后独立审查。

### Task 3: 真实 HTTP/服务集成（OpenSpec 2.5、3.2）

**Files:** 新建 accountkit_profile_http_test.go、internal/accountsvc/profile_http_test.go；复用已有 integrationInstance、真实数据库 fixture 与 HTTPRouter。必要时扩展同域测试辅助，不改生产事务语义。

**Interfaces:** 依赖 Task 1 路由、Task 2 LoadConfig；使用现有 Migrate/Users/EndUserHandler 与服务装配。

- [ ] 写 TestProfileAttributesHTTPIntegration：真实登录两账号，GET 空对象、PATCH 完整往返、大整数/注解、嵌套合并、数组/null/空补丁、未知删除拒绝且时间保持、双账号隔离、冻结只读及注销拒绝，缺实现先确认失败。
- [ ] 写 TestProfileAttributesHTTPConcurrency：通过公开 handler 并发修改不重叠属性，验证两者保留；使用账号行锁同步生命周期竞争并验证锁后状态，不靠 sleep 判断竞争。核对审计只发生于实际成功修改，原 HTTP/JWT/内省隐私用例继续通过。
- [ ] 写 TestServiceProfileAttributesHTTP：通过文件 LoadConfig 和服务 HTTPRouter 验证启用/禁用、消费者前缀和实际数据库往返；启动前坏文件失败及 migrate 忽略由 Task 2 测试交叉验证。
- [ ] 提供本任务 PostgreSQL DSN 执行根包、user 与服务定向测试，全部真实通过后独立审查。

### Task 4: 文档与最终验收（OpenSpec 4.1—4.4）

**Files:** README.md、docs/openapi.md、docs/openapi/enduser.json、scripts/check-openapi.cjs、docs/accountsvc.md、docs/compatibility.md、config.go/user/profile_attributes.go 注释、deploy/accountsvc 配置模板、基础 change 当前契约说明、本 change tasks/verification、scripts/required-tests.txt。

**Interfaces:** 文档按 Task 1/2 已验证 API；基础 change 历史记录只追加当前契约引用，不改验收历史。

- [ ] 更新接口示例和所有“仅 Go/宿主自行路由”当前表述，明确所有资料对本人开放及 readOnly/writeOnly 无权限语义；资料 Schema 文件的只读挂载和未配置 503。
- [ ] 新必需顶层测试加入清单，跑 `go build ./...`、`go vet ./...`、`go test -count=1 ./...`，在 Linux 验证镜像执行相关真实数据库 race；不把 skip 当通过。
- [ ] 执行 `openspec validate --all --strict`、git diff --check、文档链接检查，记录全部实际检查及未验证范围。
- [ ] 独立最终审查并修复，更新 tasks 与本计划勾选状态；只提交本任务文件，保留独立提交，不推送/合并/归档。
