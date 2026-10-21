# Design

## Context

参见 [proposal](proposal.md)。现有显示名有独立 Go 方法和必需 HTTP Service 方法，扩展资料通过独立 JSON Schema 配置。用户已确认短设计及仅新增头像，并授权直接修改当前 0001。

## Goals / Non-Goals

**Goals:** 基础资料一致读写、原子更新、生命周期清理、当前迁移保护与历史拒绝。

**Non-Goals:** username、图片上传或代理、第三方头像拉取、资料扩展白名单的其他调整、旧数据迁移。

## Decisions

- user_account 增加 avatar_url TEXT 可空列，紧邻 display_name；使用 NULL 表示缺失，DTO 使用空字符串，与显示名一致。相比 JSON 扩展，内置列能用于无需配置的最小公开资料。
- 新增 user.ProfilePatch{DisplayName *string, AvatarURL *string} 和 Service.UpdateProfile(ctx, userID, patch) (Me,error)。先校验字段，然后在 withDomainTx 内 LockUserByID，复核 ACTIVE，再写入最新行中选定字段。复用显示名校验，不扩大旧 UpdateDisplayName 的行为变更。
- HTTP 通过可选 ProfileUpdater 方法集探测能力。支持能力时 PATCH 使用 UpdateProfile；旧实现仅显示名时回退 UpdateDisplayName，含头像时固定 503。保留必需 Service 源码兼容。null 与省略一致，延续既有显示名指针解码语义。
- 头像规范化为 TrimSpace 后的最长 2048 字节绝对 HTTPS URL；拒绝空主机、用户信息、fragment、反斜线、内部空白/控制字符及非法 UTF-8，校验仅本地执行。地址可带普通查询参数，宿主提供稳定公开资源地址并管理图片本身。
- Me/AdminUser/PublicProfile 与两面 DTO 加 AvatarURL；批量公开资料沿用注销隐去显示名的规则一同隐去头像。PurgeUser 同事务设 NULL。扩展 Schema 顶层保留字新增 avatar_url，避免两套头像来源。
- 只变更当前 0001 up 的头像列，down 删除整表语义已覆盖。同步 sqlc 和 initialFrozen，预检检查头像列结构。旧资料 v1 拒绝测试以原定义创建夹具，不改冻结源；此前 catalog 等价测试需加入本轮单列后再与当前结构对比。

## Risks / Trade-offs

- 直接调整同号初始结构 → 缺头像列的已有库明确拒绝，使用新空资源；不自动转换或承诺旧程序可回退。
- URL 不等于文件所有权或可用性 → 宿主负责对象存储、公开访问策略及默认头像，库不抓取资源。
- HTTP 增加响应字段 → 更新 DTO 精确字段测试和兼容文档；旧必需 Service 仍可编译。

## Migration Plan

新实例使用当前 0001 安装 version=1/dirty=false。保留全部历史标签和原 SQL；旧库先核验并另行制定有数据迁移方案。本轮不改任何实际产品库，不归档、提交或推送。
