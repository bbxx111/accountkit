# accountkit_flutter

accountkit 消费者 HTTP SDK，版本 1.0.0，首个服务兼容基线为 v1.0.0。独立 `AccountClient` 可连接 accountsvc 或同契约嵌入式宿主；不依赖 Go、数据库或父工作区。授权边界见 LICENSE；未授予外部复用许可。

正式发布后的 Git 引用形式如下，示例不表示 tag 已创建：

```yaml
dependencies:
  accountkit_flutter:
    git:
      url: https://github.com/bbxx111/accountkit.git
      ref: sdk/flutter/v1.0.0 # 或已核验完整 commit SHA
      path: sdk/accountkit-flutter
```

应用提交自己的 pubspec.lock，使用 `flutter pub get --enforce-lockfile` 冻结重装。Git 凭据由环境注入，不写入 URL。包内锁文件固定本次开发依赖；不替代消费方固定 ref。

```dart
final accounts = AccountClient(
  baseUrl: 'https://accounts.example.com', // 可带反向代理基础路径
  apiPrefix: '/v1',
);
await accounts.initialize();
final controller = AccountController(api: accounts);
await controller.initialize();
await controller.sendCode(CodePurpose.signIn, 'EMAIL', emailInput);
await controller.submitCode(codeInput);
```

`baseUrl` 不允许查询、fragment、userinfo、点段或编码路径逃逸。HTTP 仅在明确设置 `allowInsecureHttp: true` 后可用。默认平台存储/设备适配面向 Android/iOS；其他平台必须注入 TokenStore 与 DeviceProvider 并单独验证。本机测试 Flutter 3.47.4 / Dart 3.13.3；本包纯逻辑测试不代表原生 Android/iOS 构建已通过。Android 最低 API 23，设备插件要求 Java 17、Kotlin 2.2.0、AGP 8.12.1、Gradle 8.13；iOS 最低 13，遵循各插件的平台配置要求。

`AccountApi` 是完整 24 操作与只读会话契约；`AccountClient` 独占 TokenStore、刷新和串行持久化。控制器只订阅该会话，初始化未完成不可当作登录成功。四态由服务 scope 决定：SignedOut、NeedsBinding、PendingDeletion、SignedIn；不解析 JWT。登录持久化失败抛 AccountStorageError，退出存储失败也可见。

第三方授权由应用执行；SDK 只接收 `IdentityCredential.apple/google/oidc/wechat/alipay/qq/weibo/facebookIdToken/facebookAccessToken` 的凭据，`generateAccountNonce()` 提供随机原始 nonce。PHONE/EMAIL 的构造器要求 target/code_id/code。扩展提供方使用 `IdentityCredential.custom('CORP_SSO', {...})` 或 `IdentityCredential.oidc(kind:'CORP_SSO', idToken:..., nonce:...)`。不把提示邮箱作为已绑定邮箱。

pending 补全仅存在控制器内存。检查 `controller.pending` 后调用 `sendCode(CodePurpose.completion,...)` 与 `submitCode`；网络/5xx 结果未知时仅由用户显式调用 `retryCompletion()` 重放原请求。更换输入、取消、外部会话变化或过期使原流程失效；票据和验证码不得进入 URL、日志或分析。页面离开调用 `cancelFlow()` 或 `dispose()`。

资料使用 `updateMe(BasicProfilePatch(displayName:...))`；`avatarUrl:''` 明确清空，null 表示省略。`patchMyProfile({'key':null})` 保留 merge patch 删除语义。身份和会话资源参数接受原始类型 ID 或属于当前账号的完整资源名。当前会话是平坦 DTO，`authenticationIdentities` 是具体身份摘要。

绑定/恢复/解绑/换绑返回 `AccountMutationResult<T>`：`value` 已成功，`sessionSync` 分别为 confirmed、sessionEnded、unconfirmed，`syncError` 单独描述确认故障。不得因确认 503/超时重新发送成功写操作。当前会话确认默认预算 5 秒。`revokeSession` 在成功后有界确认当前会话，确认故障不重复撤销。`signOut` 先清本地，远端失败通过 `SignOutResult.remoteError` 返回。

```dart
final result = await accounts.replaceIdentity(identityName, replacement);
renderIdentity(result.value);
if (result.sessionSync == SessionSync.sessionEnded) showSignIn();
if (result.sessionSync == SessionSync.unconfirmed) showSyncPending();
```

`AccountApiError` 保留 httpStatus、reason、OAuth error、retryAfterSeconds、requestId；应用按 reason 本地化。`requiresReauthentication` 标记需近期认证的业务错误。403、400 不自动刷新，刷新 invalid_grant 清理原会话，网络/429/503 保留。异常默认字符串不包含响应正文与凭据。

```dart
final business = accounts.createAuthenticatedDio(trustedScopes: [
  TrustedRequestScope(origin:'https://business.example.com', pathPrefix:'/v1'),
]);
await business.get<Object?>('https://business.example.com/v1/orders');
// 写操作仅在业务确认可重放时显式提供新 body。
await business.post<Object?>(url, data:body, options:Options(extra:{
  AuthenticatedReplay.key: AuthenticatedReplay(rebuildBody:()=>freshBody()),
}));
```

上例 `Options` 来自 `package:dio/dio.dart`。允许范围按 origin 与完整路径段匹配；禁止重定向，不向匿名账号端点附凭据。GET/HEAD 在明确401后最多一次刷新和重放；其他方法需上述显式声明，消耗过的流/FormData 不可复用。网络和5xx不自动重试。所有账号方法接受 `AccountRequestOptions(cancelToken: CancelToken())`；取消不是服务端回滚。调用方注入 Dio 不由 SDK 关闭；SDK 创建的业务 Dio 随 AccountClient.dispose 关闭。Controller 默认不拥有注入 AccountApi，`ownsApi:true` 时释放它。

注入 `dio` 时，账号传输只借用其 `httpClientAdapter`，不继承 BaseOptions、默认请求头、拦截器、transformer 或自动重试逻辑；这避免业务凭据进入匿名账号端点。适配器的连接配置由调用方负责，SDK 不关闭借用的适配器。需调整协议地址、请求超时、设备和请求 ID 时使用 AccountClient 的配置及 AccountRequestOptions。应用不得在适配器内擅自添加认证或重放账号操作。

控制器补全通过 `AccountRequestOptions.isValid` 把原期限与流程代次传到客户端，客户端在发出请求、接收结果、持久化前后检查它。自定义 AccountApi 实现同样需要遵守该有效性守卫；守卫必须同步、无副作用。Timer 用于及时取消，不替代提交时的期限检查。临时存储故障后的 initialize 可以显式重试。

测试导入 `package:accountkit_flutter/testing.dart`，提供 MemoryTokenStore、MutableAccountClock、FixedDeviceProvider、FakeAccountApi，可脚本化延迟、补全、刷新、失效；无需平台通道。`example/main.dart` 为使用 FakeAccountApi 的独立 UI 示例。

开发检查：`flutter pub get --enforce-lockfile`、`dart format --output=none --set-exit-if-changed lib test example`、`flutter analyze`、`flutter test`。真实服务、Git 隔离消费和原生平台构建由仓库 SDK 验证入口分层执行，不将单元测试视为这些层级通过。
