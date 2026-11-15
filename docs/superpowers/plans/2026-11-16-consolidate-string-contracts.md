# 全仓字符串契约收敛 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 完成78组候选的语义收敛及30项OpenSpec任务，保持公开API、协议和持久化字节兼容。

**Architecture:** Go按语义拥有者复用或定义常量，跨包公共协议使用内部叶子目录；小型helper只保持现有输出。两个SDK独立整理内部协议，工具/fixture不成为SDK运行依赖。

**Tech Stack:** Go1.26.5，PostgreSQL17/Redis，现有jwt/Chi/pgx，Flutter3.47.4/Dart3.13.3，Node24.21.0/pnpm11.24.0；其他精确版本以sdks/tool-versions.json为准，不新增依赖。

**Spec:** [design.md](../../../openspec/changes/consolidate-string-contracts/design.md)、同目录proposal/tasks及inventory.json。OpenSpec清单是值、位置和primaryTask的唯一需求来源。

## Global Constraints

- 保持所有既有字符串原值、HTTP状态/响应头/错误顺序、审计归属与隐私语义、实例隔离、持久化及密码学字节格式、环境变量及公开Go/SDK API兼容。
- 不修改生产SQL、冻结迁移/哈希、sqlc生成结果、模板正文和锁文件；不升级依赖，不新建代码生成器、全仓constants包或跨语言运行时协议包。
- 不重写filter、JSON Schema、客户端描述或浏览器识别算法，不新增表达式、规则、浏览器类型或受支持数据形状。
- 测试保留必要的独立预期值，不能让实现常量与断言同错同过。已确认行为的纯重构用基线/回归，不编造先红；新增helper及缺陷修复先写行为测试观察失败。
- root统一维护tasks、inventory和verification、分阶段本地提交；工作代理只改自己范围，不暂存、不提交、不派生代理。不同模块无共享写入时按AGENTS并行，依赖者顺序接入。

## Review Focus

- HTTP status/reason/审计reason同值不同义；用原响应、原审计查询/投影验证。
- challenge多头、realm/error缺失、no-store/Pragma组合；Task1独立文字断言。
- AAD/Redis/subject/指纹/迁移锁固定字节；Task4及Task5固定向量和旧值往返。
- SDK匿名排除表与实际请求路由、Dio epoch/revision；Task2状态/并发测试。
- SQLSTATE08/53未知码及各路径额外码；Task4测试不缩窄/扩张分类。

### Task 1: Go HTTP/JOSE与HTTP描述数据

**Files:** httpapi/**（含filter/clientinfo的数据文字）、tokens/**、internal/httpcredentials/**；internal/accountsvc/{adminauth,basicclients,oidcjwt,serviceauth,introspection,purgefeed,identityresolve}/**。新建internal/httpcontract、internal/jose、httpapi/internal/resourcename。不写root accountkit/config、accountsvc顶层、user、audit、SDK、scripts。

**Interfaces:** httpcontract提供HeaderAuthorization/HeaderWWWAuthenticate/HeaderContentType/HeaderRequestID/HeaderDeviceID/HeaderDeviceName、MediaTypeJSON/ContentTypeJSON/MediaTypeForm、TokenTypeBearer、OAuthError*、共享Reason*；SetNoStore(http.ResponseWriter)、SetOAuthNoStore(http.ResponseWriter)、BearerChallenge(realm,errorCode string) string、BasicChallenge(realm string) string、ValidRequestID(string) bool。jose提供HeaderType/HeaderKeyID/HeaderJWKURL/HeaderX509URL及Claim*、AccessTokenType/AccessTokenMediaType、JWK*。resourcename提供User(id string)、Identity(userID,id string)、Session(userID,id string)、Profile(userID string)、AuditEvent(userID,id string)。如实际名称不同，报告准确接口供Task5接入，不改输出。

**Coverage:** OpenSpec2.1–2.4、5.1；L001/L005–L012/L035–L038/L042/L043及L046的RequestID。L002审计事件复用可留Task4避免audit目录冲突。

- [ ] 先写challenge/no-store/资源名/RequestID行为测试并运行确认缺少helper导致失败；HTTP reason复用现有独立端点断言。
- [ ] 按清单归属提取；旧authn.Reason*及tokens公开const保留兼容别名，JOSE不依赖消费者签发实现。filter/browser只共享名字/数据，不重写算法。
- [ ] 跑GOWORK=off go test -count=1 ./httpapi/... ./tokens ./internal/httpcredentials ./internal/accountsvc/{对应子包}与vet，保存日志；更新所属包注释，不写镜像实现测试。
- [ ] 报告实际覆盖IDs/保留值、公开接口变化（应兼容）、文件列表及测试证据；root审查后提交。

### Task 2: Flutter内部协议（独立并行）

**Files:** 仅sdks/accountkit-flutter/lib、test及README；不改pubspec/lock、Go、工具、fixture或其他文档。

**Interfaces:** 内部协议定义置于lib/src，不从accountkit_flutter.dart新增公开exports。保持自定义kind、原token持久化格式、同一AccountClient刷新协调和平台注入接口。

**Coverage:** OpenSpec6.1–6.2，L057–L063。精确工具/真实服务/原生构建是root综合验收与6.3，不假报。

- [ ] 先确认原独立协议/匿名请求/并发重放测试，并补能捕获匿名排除表漂移和extra键冲突的行为断言。
- [ ] scope/kind/reason、匿名路径、Dio extra、Header/wire keys与同义regex各归属内部；实际请求与匿名集合共用路由，动态资源函数保持校验。
- [ ] 用可用工具跑format/analyze/单元；若非精确版本仅记补充证据，root会提供精确工具路径。缺依赖不修改锁文件迁就；范围README说明兼容行为。
- [ ] 报告逐ID覆盖和测试/工具版本、文件列表；root审查后提交。

### Task 3: TS SDK与JS/shell工具（独立并行）

**Files:** sdks/accountkit-admin-ts/src、test、README；scripts的JS/CJS/MJS及仅相关shell说明。不要修改Go工具、recovery_test.go或sdks/testsupport Go，留Task5；不改lock/package版本。

**Interfaces:** TS内部资源协议模块统一现有alphabet/资源名，filter const tuple派生原union。工具新建scripts/sdk-metadata.mjs与可被CJS/ESM共用的内部方法集合（只工具使用）。fixture控制协议仍独立预期，不成为SDK运行依赖。

**Coverage:** OpenSpec7.1、9.1、9.2的脚本部分；L064–L066/L070–L072/L078脚本协议。跨Go恢复/fixture由Task5顺序接入。

- [ ] 运行现有TS和node工具基线；补临时资源前缀、拒绝漂移/未知surface、取消清理安全边界的必要行为测试。
- [ ] 收敛TS资源/错误/HTTP/filter tuple及工具SDK描述、方法、创建/清理前缀和恢复阶段；原CLI/env、清理路径证明和测试独立期望保持。
- [ ] 精确Node/pnpm路径为.test-output/admin-permissions/tools/node_modules/{node/bin,.bin}，跑TS typecheck/format/test与相关node --test；不盲改coverage检查器的拒绝规则。
- [ ] 报告工具接口供Task5消费、逐ID覆盖/保留及证据；不暂存、提交。

### Task 4: Go领域/审计/存储/指标（Task1之后）

**Files:** user（idp/sender仅storage subject/nonce/policy相关修改）、audit、enum、session、migrations/internal/migrationlock、maintenance、observability、internal/auditconfig、internal/profileattributes。不要改root accountkit/config、accountsvc顶层、提供方请求/响应字段、SDK或scripts。

**Interfaces:** 内部审计原因/资源目录避免领域导入HTTP；若导出最小协议常量须报告。internal/auditconfig增加ForSchema(schema string) authentication.Options供根配置使用，Local(pool)复用，默认值不变。既有obs Task/AuditField直接传递；SQLSTATE每路径集合不变。

**Coverage:** OpenSpec3.1–3.5、5.2，L002/L013–L025/L033/L034/L040/L044/L045/L054–L056。Task5接root及IdP其余协议。

- [ ] 先跑原相关测试并补旧格式/关键边界断言；新helper先红，不能改Lua/SQL/模板正文。
- [ ] 收敛code状态/审计原因资源/存储格式/SQLSTATE/指标标签/JSON关键字数据。same-value不同语义分开；不额外公开解析API、不重写递归/计数算法。
- [ ] 运行领域/audit/session/observability/profileattributes/迁移相关测试和vet；需要DB证据由root专用环境补跑，不把skip当通过。
- [ ] 逐ID报告具体闭环及root/Task5仍需接入的接口，更新局部注释；root审查后提交。

### Task 5: 配置/提供方/示例/fixture与Go工具接入（Task1、3、4之后）

**Files:** root accountkit.go/config.go，accountsvc顶层，user/idp与sender，examples，sdks/testsupport，internal/{testgate,migrationcheck}、recovery_test.go。新internal/idpdefaults。仅必要关联scripts协调Task3接口，避免重写解析器。

**Interfaces:** 消费Task1 HTTP/JOSE、Task4审计默认函数及具名Task/AuditField；IdP六个默认endpoint在internal/idpdefaults，根默认填充/Validate仍保持。fixture metadata只用于测试；不新增环境键、启动I/O或包外依赖。

**Coverage:** OpenSpec4.1–4.3、8.1–8.3、9.2 Go部分及所有前序跨模块引用。L003/L004/L018/L026–L032/L039/L041/L046–L053/L067–L069/L073–L078。

- [ ] 先取得相关配置/提供方/fixture/工具行为基线和签名/旧字节断言。
- [ ] 按目录命名配置/默认/Provider状态与错误/发送器模式，接共享Header/资源/JOSE，不改值、接受范围、顺序或公共字段类型；示例保留宿主选择。
- [ ] 跑root/accountsvc/idp/sender/examples/fixture/Go工具范围测试、vet；验证模板/清理和固定输入签名；更新相关局部文档。
- [ ] 逐ID/值报告实现或保留及完整文件列表；root独立审查后提交。

### Task 6: 全仓验证、清单闭环与提交记录

**Files:** 本变更tasks/inventory/verification及适用必需测试登记；测试脚本仅按真实缺口修复，不能放宽。

- [ ] root固定全仓Go/SDK基线，取得旧/新无replace独立module消费；每批根据原值/实例隔离/协议断言核查并记录。
- [ ] 最终在容器本地文件系统执行Go build/vet/test/race、生成/迁移历史检查、verify.sh恢复和verify-accountsvc进程；只用任务资源，结束核对ID清理。
- [ ] 精确SDK工具完成Flutter和TS单元、真实服务/嵌入策略、固定Git消费及Android/iOS；Windows不能提供的macOS/Xcode检查保留未完成，不伪造。
- [ ] 全分支审查修复、78组闭环、OpenSpec/链接/LF/diff校验；按实际证据勾选任务并分阶段本地提交。推送、合并、归档、发布另按授权。
