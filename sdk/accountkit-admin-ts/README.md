# @accountkit/admin-client

accountkit 管理 HTTP API 的 TypeScript 源码客户端。管理员登录、续期、角色与页面状态由应用管理。SDK 每次调用 `getAccessToken`，不保存令牌，不登录、退出、重试或跳转。

```ts
import {createAccountAdminClient, userFilter} from '@accountkit/admin-client';

const client = createAccountAdminClient({
  baseUrl: 'https://accounts.example.com',
  // apiPrefix: '/admin/v1',
  getAccessToken: async () => obtainApplicationAccessToken(),
});
const page = await client.listUsers({
  filter: userFilter.eq('state', 'ACTIVE'),
  page_size: 20,
});
```

公开方法为 `listUsers`、`getUser`、`deleteUser`、`freezeUser`、`unfreezeUser`、`undeleteUser`、`revealIdentity`、`listSessions`、`revokeSession`、`revokeAllSessions` 和 `listAuditEvents`。用户参数接受 `u_…` 或 `users/u_…`；身份/会话接受 ID 或匹配父用户的完整资源名。DTO 保持 wire snake_case；时间为 RFC3339 字符串，未知合法枚举保留原值。

每个方法最后接受 `{signal, requestId}`；`listUsers` 第一个参数为过滤分页，`listAuditEvents(user, pageOptions, requestOptions)`。冻结为 `freezeUser(user, {reason}, options)`；解冻为 `unfreezeUser(user, {reason?}, options)`。`revokeSession` 成功返回 void，其他写操作返回服务资源或计数。取消不保证服务端事务回滚。所有操作只发送一次；写操作结果丢失后应先查询最终状态。

`AccountAdminArgumentError`、`AccountAdminTokenProviderError`、`AccountAdminCancelledError`、`AccountAdminTransportError`、`AccountAdminApiError` 和 `AccountAdminProtocolError` 可区分失败来源。API 错误提供 `httpStatus/status/reason/serverMessage/retryAfterSeconds/requestId`；默认 Error.message、stack 和对象诊断使用固定安全消息；服务端 message 仅通过显式只读且不可枚举的 serverMessage 读取。应用按 reason 展示信息，避免记录 serverMessage、filter、reveal 明文和令牌。非 JSON 失败使用固定消息，保留 HTTP 状态。响应 request ID 优先于请求 ID。可注入标准 Fetch；传输必须返回原始 Response（包括非 2xx），不能预读正文或隐式重试。

filter 工具支持 state、identity.phone/phone_prefix/phone_suffix/email/email_prefix/email_domain 的 `eq`，create_time 的 `compare('create_time', '>=' | '<', timestamp)`，以及 `and(...)`。也可显式传入原始 filter。正式服务 quoted-value 没有转义语法，工具拒绝含双引号/控制字符的不可表达值；其余特殊字符逐字保留，由 URLSearchParams 编码。分页 token 不透明；SDK 不累计页面或自动恢复失效游标。

仅访问配置的 origin/基础路径及前缀。HTTPS 用于远程服务，HTTP 仅支持 localhost/loopback 测试；拒绝点段、编码路径逃逸、凭据、查询和片段。所有请求 `cache: no-store`、拒绝重定向，不依赖 window、React 或 OIDC 库。

源码包首版 1.0.0，目标服务契约 v1.0.0。固定 Git 引用形式（tag 需正式发布后才存在）：

```json
{"dependencies":{"@accountkit/admin-client":"git+https://github.com/bbxx111/accountkit.git#sdk/admin-ts/v1.0.0&path:/sdk/accountkit-admin-ts"}}
```

也可使用完整 commit SHA；提交消费者锁文件并执行 `pnpm install --frozen-lockfile`。Git 凭据通过环境注入。包只公开根入口，exports/types 指向 src/index.ts，无 dist 或安装时编译；消费方须转换 TS 源码。开发固定 Node 24.21.0、pnpm 11.24.0、TS 5.8.3、Vite 6.4.1、Vitest 3.2.4，包内检查 `pnpm typecheck && pnpm test`；Vite 独立 Git 消费、真实服务与远程分发验收由仓库独立记录，不从包内测试推断通过。

本 SDK 只提供管理协议；消费者 Flutter SDK、可嵌入 Go 库和可选 accountsvc 是独立分发/运行单元。许可边界见 LICENSE。

真实服务集成使用仓库 `sdk/testsupport/README.md` 的 accountsvc 模式。设置 `ACCOUNTKIT_ADMIN_INTEGRATION_ORIGIN` 为真实服务 origin、`ACCOUNTKIT_ADMIN_INTEGRATION_CONTROL_ORIGIN` 为测试控制接口 origin，然后运行 `pnpm test:integration`。缺少配置或真实依赖时直接失败；默认 `pnpm test` 仅运行独立单元套件。集成通过公开管理 SDK 调用全部 11 操作；消费者种子仅通过测试专用 HTTP helper 登录创建，不将 helper 算作管理 SDK 覆盖。
