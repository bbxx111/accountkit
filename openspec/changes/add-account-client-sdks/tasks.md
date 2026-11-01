## 1. Package Foundations and Contract Coverage

- [x] 1.1 建立 `sdk/accountkit-flutter` 的独立 pubspec、许可、公开入口及分析/测试配置，包名为 accountkit_flutter；验证从包目录独立解析依赖且没有父工作区或绝对路径引用。
- [x] 1.2 建立 `sdk/accountkit-admin-ts` 的独立 package.json、许可、TS/Vitest 配置与 src/index.ts，包名为 @accountkit/admin-client；验证 exports/types 指向源码，独立安装与类型检查成功且无 dist/安装构建要求。
- [x] 1.3 锁定并记录 Flutter/Dart、Node、pnpm、TypeScript、Vite 与直接开发依赖的实测版本，生成各自锁文件；验证两包在声明工具链上冻结安装，Node 满足 pnpm engines。
- [x] 1.4 添加 sdk/http-coverage.json 和 scripts/check-sdk-coverage.mjs，登记消费者 24 个与管理 11 个当前操作及模型/测试关联；验证对照 OpenAPI 全覆盖，并用缺失/额外条目的检查用例证明会失败。

## 2. Flutter Protocol and Public API

- [x] 2.1 定义 AccountApi、公开 DTO、开放 kind 和内置/扩展 credential 构造器；验证 PHONE/EMAIL 轮次、各第三方正式凭据形状、未知 kind、未知枚举和畸形必填字段的序列化/解码测试。
- [x] 2.2 实现 URL/前缀、资源名校验、设备头、请求 ID、JSON 编解码和类型化错误；验证自定义挂载、父资源不匹配、路径逃逸、非 JSON 错误、AIP/OAuth 及无敏感信息打印。
- [x] 2.3 实现登录/发码/补全/token/revoke 的协议调用，并为每个操作登记覆盖测试；验证匿名路径不带 Bearer、无认证自动重试且正确处理 token/pending 互斥与无内容 revoke 成功响应。
- [x] 2.4 实现基础资料、扩展资料、重新认证、注销/恢复方法；验证 avatar_url 空字符串清除、字段省略、merge patch null 删除和重新认证只返回 access 的协议形状。
- [x] 2.5 实现身份发码/列表/绑定/解绑/换绑和会话列表/当前/撤销/撤销其他方法；验证当前会话平坦 DTO、认证身份、分页及可选能力 503 reason，覆盖所有剩余消费者操作。

## 3. Flutter Session Runtime

- [x] 3.1 实现单一会话运行时与完整记录 TokenStore、安全存储适配及初始化恢复；验证损坏记录不形成登录态，登录只有存储成功才发布，access/refresh 到期信息完整保留。
- [x] 3.2 实现 epoch/revision 和串行存储提交；验证延迟登录/刷新/清除/存储失败与换账号交错时不覆盖或删除新会话，旧凭据不从迟到写入复活。
- [x] 3.3 实现基于正式 scope 的四态与只读观察接口，将 AccountController 接到 AccountApi；验证初始化不冒充已登录、未知 scope 报错、无第二份可写凭据或 JWT 解码。
- [x] 3.4 实现主动刷新、并发合并和一次认证重试；验证多请求只有一次刷新、旧 revision 的 401 复用新 token、重放仍 401 只退出原会话，invalid_grant 与网络/429/503 分别处理。
- [x] 3.5 实现重新认证后的 access 更新、注销/当前会话撤销与本地 signOut；验证 refresh 不被丢弃、重认证与轮换交错不回写旧数据、远程 revoke 失败不恢复已退出凭据。
- [x] 3.6 实现 AccountMutationResult 和绑定/恢复后的 scope 同步、解绑/换绑后的有界会话确认；验证成功与 confirmed/sessionEnded/unconfirmed 分开，503/超时不伪造业务失败，迟到确认不影响新会话，普通 403/400 不触发刷新。

## 4. Flutter Flows and Application Adapters

- [x] 4.1 实现内存登录补全流程、原期限/设备快照和原请求手动重试；验证取消、输入修改、外部会话变化、过期与迟到成功时不持久化，未知 action/channel 可恢复且 pending 不成为第五态。
- [x] 4.2 实现验证码轮次、用途/目标/generation 关联及服务端等待秒数；验证旧发码响应不污染新输入、CODE_INVALID 不猜次数、CODE_EXPIRED 结束轮次、429 不自动重发或退出。
- [x] 4.3 实现持久设备身份、可注入且有界的 client-info 采集和 nonce 工具；验证采集失败/超时降级、补全设备快照稳定、默认打印不包含凭据或完整身份信息。
- [x] 4.4 实现复用同一会话的 createAuthenticatedDio、显式受信 origin/路径、禁重定向和可重放 body 契约；验证外域/路径边界不泄露凭据，GET 一次401重放、显式写重放及不可重放流的结果，网络/5xx不自动重做写操作。
- [x] 4.5 实现网络取消、Controller/Client dispose 及资源所有权规则；验证等待刷新期间取消不继续发送、释放后无状态事件、调用方网络组件不被关闭。
- [x] 4.6 提供 testing.dart 的 FakeAccountApi、内存存储及可控时钟/设备替身，补充独立应用使用示例；验证无需平台通道即可测试四态、补全、刷新、失效及延迟响应。

## 5. TypeScript Admin SDK

- [x] 5.1 实现 createAccountAdminClient 配置、逐请求 token provider、标准 Fetch 注入、请求 ID、URL/前缀与资源层级校验；验证 provider 失败/空值不发请求、无导入副作用、无 React/OIDC/window 依赖。
- [x] 5.2 实现仅解析一次响应的传输层和局部/provider/取消/网络/API/协议错误；验证 204、AIP reason、响应 request id、429等待值、非 JSON 状态保留与畸形成功响应。
- [x] 5.3 实现用户列表/详情/删除/冻结/解冻/恢复、身份 reveal；逐方法验证 HTTP 路径/动词/body、DTO nullable/未知值和角色403原样返回。
- [x] 5.4 实现 session 列表/单个撤销/全部撤销与认证 audit 查询；验证嵌套资源父归属、opaque cursor、flow_id省略、其他null保留及无自动分页恢复。
- [x] 5.5 实现用户 filter 类型化组合、原始 filter 入口和分页/show_deleted 参数；验证合法字段/AND/特殊字符转义、游标不修改、INVALID_PAGE_TOKEN 不触发隐藏请求。
- [x] 5.6 实现所有方法 AbortSignal、no-store、禁重定向和单次执行；验证 provider等待中取消后无请求、发送后取消可辨识、401/429/5xx/网络失败均不自动重试、不更改应用登录态或记录敏感正文。

## 6. Real Service Contract Tests

- [x] 6.1 添加 sdk/testsupport 测试服务装配，重用现有 accountsvc 启动/迁移方式，提供独立 PostgreSQL/Redis、测试发码与受控管理员 JWKS；验证可启动真实服务，缺少必要依赖时必需套件失败而非跳过。
- [x] 6.2 用 Flutter SDK 实际调用真实服务，覆盖验证码登录、补全、刷新、四态、重新认证和删除/恢复；验证真实凭据、scope、数据库会话与 SDK 结果一致，网络请求不是另写 HTTP 客户端替代。
- [x] 6.3 用 Flutter SDK 验证基础/扩展资料、身份绑定/换绑/解绑、当前/其他会话及撤销；验证换绑撤销源会话时业务成功仍保留，并用嵌入式 fixture 覆盖自定义前缀和可选能力未配置。
- [x] 6.4 用受控提供方 fixture 验证第三方 credential 形状和自定义 kind 流转；验证与真实第三方原生授权测试分别标注，不将合成验证报告为平台授权成功。
- [x] 6.5 用 TypeScript SDK 实际调用真实服务，覆盖管理用户生命周期、分页/filter、reveal、会话和认证审计；验证 operator/super-admin 权限差异、401/403与审计 nullable/flow_id 语义。

## 7. Isolated Git Consumers and CI

- [x] 7.1 添加仓库外临时消费者生成与固定 Git 提交快照入口，不修改工作分支或创建正式 tag；验证两消费者只使用 Git URL＋SHA＋子目录，无 path/workspace override，临时 Git 服务仅监听本机并可清理。
- [x] 7.2 实现 Flutter 独立消费验证：冻结依赖安装、包入口导入、分析/行为测试以及 Android debug 和 iOS 无签名构建；验证平台构建分别在适用 runner 执行并与纯逻辑测试分开报告。
- [x] 7.3 实现 TS/Vite 独立消费验证：pnpm Git ref/path 安装、锁文件冻结、类型检查/测试/生产构建；验证 SDK 原始 TS 被消费者转换且不需要 dist、prepare/postinstall 或根目录配置。
- [x] 7.4 添加 scripts/verify-client-sdks.sh 与 SDK workflow，固定工具版本并触发相关协议变更检查；验证完整运行包含覆盖检查、两包检查、真实服务和隔离消费，必需依赖缺失不会成功退出。
- [x] 7.5 验证既有 Go CI、构建、数据库、race 与恢复门禁继续独立运行；执行仓库规定检查并记录命令和结果，确认 Go 使用方不被要求安装 SDK 工具链。

## 8. Documentation and Readiness

- [x] 8.1 完成两包 README/CHANGELOG 和 docs/client-sdks.md，更新根 README、兼容性说明及 AGENTS；检查示例准确覆盖公开入口、默认/自定义前缀、平台注入、错误/成功后同步处理，以及 OIDC/UserInfo/角色/页面状态由应用负责。
- [x] 8.2 增加 SDK 独立版本与发布校验入口，检查 manifest/tag 一致、固定 ref 示例和服务兼容矩阵；验证合法/不匹配版本用例，文档明确正式 tag 不由本实现自动创建或推送。
- [x] 8.3 添加发布后远程 Git 固定 tag 消费的可执行验证入口及运行说明；通过命令参数/fixture验证入口可用，当前未发布时明确记为待发布后执行，不将本地 Git 验证当远端成功。
- [x] 8.4 在本变更 verification.md 汇总各层真实执行证据、工具版本、服务基线和限制，原始日志放 .test-output；执行 openspec validate --all --strict 并复核所有任务与规格场景对应，确认未实施下游迁移或改变服务端契约。
