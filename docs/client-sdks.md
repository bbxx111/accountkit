# 客户端 SDK

Go 库可直接嵌入宿主，accountsvc 是可选独立服务；两个客户端包通过 HTTP 接入任一遵守正式协议的宿主，互不成为运行时依赖。Go 使用方无需安装 Flutter、Node 或 pnpm。

| 包 | 公开入口 | 默认前缀 | 目标服务基线 |
| --- | --- | --- | --- |
| `accountkit_flutter` | `lib/accountkit_flutter.dart` | `/v1` | `v1.0.0` |
| `@accountkit/admin-client` | `src/index.ts` | `/admin/v1` | `v1.0.0` |

两包首版均为 `1.0.0`（已发布），发布版本独立递增。发布范围见[发布说明](releases/client-sdks-v1.0.0.md)，候选 CI 和远端消费结果见[发布验收记录](../openspec/changes/prepare-client-sdks-v1-release/verification.md)。实际 SDK 单元、真实服务、Git 消费和平台实现结果另见[功能验收记录](../openspec/changes/archive/2026-11-01-add-account-client-sdks/verification.md)。

## 固定 Git 安装

以下为已发布并验证过的固定 tag，也可使用经过验证的完整 commit SHA；不要使用 main/develop 等浮动分支。Git 认证通过环境注入，URL 不含凭据。

```yaml
dependencies:
  accountkit_flutter:
    git:
      url: https://github.com/bbxx111/accountkit.git
      ref: sdk/flutter/v1.0.0
      path: sdks/accountkit-flutter
```

```json
{
  "dependencies": {
    "@accountkit/admin-client": "git+https://github.com/bbxx111/accountkit.git#sdk/admin-ts/v1.0.0&path:/sdks/accountkit-admin-ts"
  }
}
```

提交应用自己的锁文件，分别使用 `flutter pub get --enforce-lockfile` 和 `pnpm install --frozen-lockfile` 重装。TS 子目录语法属于 pnpm，未承诺 npm/yarn 等价支持。TS 源码由消费方 TypeScript/Vite 转换；包不包含 dist、prepare/postinstall，不承诺任意 JavaScript 运行时可直接加载。精确工具补丁见 [tool-versions.json](../sdks/tool-versions.json)。

## Flutter 的职责

完整接口和例子见[包 README](../sdks/accountkit-flutter/README.md)。配置 `baseUrl` 为 origin 或反向代理基础路径，`apiPrefix` 可改为宿主实际前缀；远端使用 HTTPS，本机测试显式允许 HTTP。不得把资源名当成任意 URL。

AccountClient 独占 TokenStore、会话 epoch/revision 与刷新协调。AccountController 依赖 AccountApi，通过只读快照观察 SignedOut、NeedsBinding、PendingDeletion、SignedIn；初始化状态单独表示，pending 登录补全仅在内存。测试入口 `package:accountkit_flutter/testing.dart` 提供无平台通道替身。

平台原生授权由应用完成，将正式 kind/credential 交给 SDK。Android/iOS 的安全存储与设备信息可替换，其他平台需显式注入适配器并单独验证。业务 Dio 必须声明受信 origin/路径；流和写操作的重放需显式契约，网络未知结果不能自动重复写入。

绑定/恢复后的 scope 同步、解绑/换绑后的会话确认通过 `AccountMutationResult` 与业务结果分开表达。`sessionSync=unconfirmed` 表示已成功的业务操作尚未确认会话状态；应用不应据此重新提交换绑。普通 400/403 不触发自动刷新或退出。

## TypeScript 的职责

```ts
import {createAccountAdminClient, userFilter} from '@accountkit/admin-client';

const client = createAccountAdminClient({
  baseUrl: 'https://accounts.example.com/proxy',
  apiPrefix: '/admin/v1',
  getAccessToken: async () => applicationSession.getAccessToken(),
});
const page = await client.listUsers({filter: userFilter.eq('state', 'ACTIVE')});
```

`applicationSession` 为应用已有认证组件。登录、续期、OIDC UserInfo、角色映射、路由准入和页面状态由应用负责；UserInfo 成功不能代替各 API 服务的 issuer/audience/有效期和逐路由角色校验。SDK 不解析 JWT、刷新管理员凭据或跳转页面。

每次请求调用 token provider，失败或取消不发送匿名/迟到请求。可注入标准 Fetch，必须返回原始 Response（含非成功状态）供 SDK 单次解析。所有方法支持 AbortSignal；取消不保证事务回滚。401/429/5xx/网络错误均不自动重试，写操作响应丢失后先查询最终状态。

错误分为参数、provider、取消、传输、API、协议六类。API 错误的默认 message/stack 固定安全，原服务端消息通过显式 `serverMessage` 读取；reason、HTTP status、等待秒数和请求 ID 保留。不要记录 token、reveal 明文或完整 filter。分页游标保持不透明，INVALID_PAGE_TOKEN 由页面决定是否重置列表。

## 验证与发布

覆盖检查：`node scripts/check-sdk-coverage.mjs`。包内分别运行 Flutter analyze/test 和 pnpm typecheck/test。真实服务装配见 [sdks/testsupport](../sdks/testsupport/README.md)；Go 烟测只证明宿主就绪，SDK 契约必须由 SDK 实际发请求。

发布前 Git 隔离入口在仓库外创建一次性应用、临时本机 Git 服务和固定快照 SHA，不改变源分支或创建 tag：

```sh
node scripts/verify-sdk-consumers.mjs --sdk admin-ts
node scripts/verify-sdk-consumers.mjs --sdk flutter --platform none
# 适用 runner 分别执行
node scripts/verify-sdk-consumers.mjs --sdk flutter --platform android
node scripts/verify-sdk-consumers.mjs --sdk flutter --platform ios
```

`none` 只验证安装/静态/行为，不等于 Android 或 iOS 构建通过。Android 使用 debug APK，iOS 无签名构建须 macOS 和 Xcode >=26.1.1；当前设备信息插件要求该工具链，CI 按 `sdks/tool-versions.json` 显式选择 Xcode，不能依赖 runner 默认版本。临时 Git 只监听 loopback，完成后清理本次创建的目录和进程。

发布检查核对清单、tag 及 [服务兼容矩阵](../sdks/compatibility.json) 的验收状态，未验证服务组合会失败；它不创建 tag，也不代替原生平台与其他发布门禁。仅检查候选元数据可显式追加 `--metadata-only`，不能把该模式当作发布就绪：

```sh
node scripts/check-sdk-release.mjs flutter sdk/flutter/v1.0.0
node scripts/check-sdk-release.mjs admin-ts sdk/admin-ts/v1.0.0
```

正式 tag 发布后执行真实远端消费；首版两包已通过，重验命令如下：

```sh
node scripts/verify-sdk-consumers.mjs --sdk admin-ts --git-url https://github.com/bbxx111/accountkit.git --ref sdk/admin-ts/v1.0.0
node scripts/verify-sdk-consumers.mjs --sdk flutter --git-url https://github.com/bbxx111/accountkit.git --ref sdk/flutter/v1.0.0 --platform none
```

两包 tag 不重写；修复另发版本，下游通过恢复旧 ref 和锁文件回退。下游包替换、旧封装删除和产品发布由各自变更验收。本仓库及 SDK 尚未授予外部复用许可，见包内 LICENSE。
