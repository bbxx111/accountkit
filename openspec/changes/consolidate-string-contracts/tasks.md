# Tasks

实施依据[design.md](design.md)及[inventory.json](inventory.json)，结果记录于[verification.md](verification.md)。全部为待实施任务；ff不勾选。apply前按仓库要求编写详细计划，沿依赖顺序分派、审查和验证；每个可独立验证的步骤通过后本地提交，包含相关测试/文档和清单记录，不并行修改共享定义。清单primaryTask表示主要负责项，跨模块引用在相应后续步骤同步，不能把共享定义引入后留下无法编译的中间提交。

## 1. 行为与兼容基线

- [x] 1.1 以a11ea46及78组清单固定当前原值、位置和语义拥有者，补/复用精确协议、既有持久化值、危险同值不同义的行为断言；确认当前行为通过而非编造先红，记录基线SHA及各候选处理边界，验证所有ID唯一、任务映射存在、引用文件有效。
- [x] 1.2 为现有Go公开符号、字段类型和构造入口准备独立module兼容消费检查，复用既有固定Git快照机制且GOWORK=off/无replace；先取得基线编译运行结果，记录操作方法到verification，为后续内部共享定义/兼容别名提供可重复对照，不新增运行依赖。

## 2. Go HTTP与JOSE协议

- [ ] 2.1 处理L001/L005–L007/L010：复用scheme、方法与身份定义，按语义归属命名60种HTTP reason、OAuth错误、token type/grant/下一动作及固定安全消息；保留旧公开reason别名和各面的状态/消息/错误优先级，补独立字面值断言与相关注释，运行httpapi和三个服务HTTP面范围测试/vet并审查。
- [ ] 2.2 处理L008/L009/L035：在内部HTTP帮助层收敛challenge、SetNoStore/SetOAuthNoStore和资源名构造；先覆盖单/多认证头、realm/error缺失、403/503、Pragma差异、reveal所有响应及父资源路径，保持各面DTO/授权独立，更新内部说明，运行HTTP/服务认证协议测试验证精确输出。
- [ ] 2.3 处理L011/L012及L026共享部分：集中无业务依赖的JOSE header/claim/JWK文字，优先复用当前jwt算法定义，tokens公开const保持；补有效/未知/畸形声明、default/rfc9068不回退和消费者/管理员/服务/IdP隔离测试，更新归属注释，运行tokens、oidcjwt、adminauth、serviceauth及IdP回归/vet。
- [ ] 2.4 处理L036–L038及L046的RequestID部分：共用Header/MIME和同义RequestID验证，保持自定义宿主回调、生成/回退时机、设备头及Client Hints大小写语义；补解析边界与生成/回退行为测试、同步协议注释，运行clientinfo、reqid、accountsvc metadata及根门面测试，确认未给普通一次性标准头制造新公共API。

## 3. 领域、审计与持久化文字

- [ ] 3.1 处理L013：集中Go验证码脚本状态和限流Dimension，原Lua不拆碎、公开字段仍为string；以独立文字断言检查Lua/Go协议、尝试次数/轮次/发送和失败预算，更新状态注释，运行code及领域/HTTP验证码回归并核对失败路径不回滚计数。
- [ ] 3.2 处理L002/L014–L016/L054：命名审计静态原因、认证资源类型及各能力业务action，复用已有事件枚举，动态USER_状态原因保留所有可达/未知分支输出；补审计写入/投影/查询/隐私清理与Actions注册范围断言，更新所属目录说明，运行audit、authentication及对应user管理/身份解析/资料回归，不合并同值的主体/资源/原因。
- [ ] 3.3 处理L017/L019–L021/L034/L040：提取AAD、subject/nonce/指纹/Redis命名域及迁移表名的具名原值/构造函数；保持不同提供方身份空间和实例前缀，补旧格式读取/解密、键与锁ID固定向量、宽限重试和多实例迁移测试，更新格式注释，运行pii/code/idp/session/迁移真实数据库与并发回归，核对冻结SQL/哈希未变。
- [ ] 3.4 处理L022/L025：复用pgerrcode具体码和既有enum文本映射，仅在现有语义内提供局部解析/分类帮助函数；用未知08/53类码及每路径额外错误码、结果/主体合法与非法值验证接受集合不变，更新分类注释，运行user严格认证/会话来源/身份解析及审计JSON回归。
- [ ] 3.5 处理L023/L024/L033/L044：复用维护Task/AuditField，集中审计默认构造与metricdata标签/单位，保留默认schema/namespace及指标目录/维度；补独立标签/任务/省略字段/默认配置断言和高基数拒绝测试，更新模块注释，运行observability两适配器、maintenance、audit与根配置测试。

## 4. 配置、发送器与第三方协议

- [ ] 4.1 处理L003/L018/L029–L032：复用envPrefix/emailTemplatesSetting，集中模式、命令、客户端字段/后缀及变量构造，提供SMTP TLS模式具名定义；补孤立空字段、别名下划线、不同CSV去重、配置报错脱敏、production/development和TLS组合回归，更新配置说明，运行accountsvc config及SMTP测试，环境名/默认值/校验顺序不变。
- [ ] 4.2 处理L026–L028/L046–L053：按提供方整理默认endpoint、claim/请求/响应复用字段、鉴别值、错误码、算法/MIME/PEM与相同用途的校验规则，根默认与idp构造使用单一内部定义；补原始签名输入/验签、成功与拒绝分类、URL/应用ID边界及配置规范化回归，更新提供方协议注释，运行所有IdP/短信/邮件与根Config测试，保留上游拼写及原时间格式。
- [ ] 4.3 处理L039/L041：命名健康响应、服务Endpoint标签、预览文件名与管理审计本地拒绝分类；验证ready/health无依赖泄露、无权限不解析业务、预览覆盖允许表与输出文件一致、审计失败分类和no-store保持，更新运维/工具说明，运行routes/http/preview/admin audit范围测试。

## 5. 既有解析器的数据收敛

- [ ] 5.1 处理L042/L043：只集中filter字段元数据与浏览器规范名/别名/rank，保留既有语法和识别算法；以合法/非法filter、转义、未知品牌、别名优先级及原输出独立断言验证，更新局部字段说明，运行admin filter/clientinfo用例，复杂解析器重写不纳入本步。
- [ ] 5.2 处理L045/L055/L056：共用Schema Draft URI/关键字、审计JSON和客户端描述反复索引/白名单数据，保留单一敏感词策略集合，避免逐词公共常量；覆盖重复键、未知字段、嵌套/循环ref/容量预算、精确数字和隐私脱敏的既有边界并同步注释，运行profileattributes/audit/client info单元、fuzz种子及数据库往返回归，不改递归算法或接受集合。

## 6. Flutter SDK

- [ ] 6.1 处理L057–L059：在lib/src内整理scope/kind/下一动作/控制流程错误/Dio extra键，让匿名端点发送与凭据排除集合同源；先覆盖未知kind扩展、pending/绑定/恢复、匿名请求不带Bearer、取消/重放和epoch/revision隔离，更新内部说明并运行对应状态机、controller、协议和Dio测试，不扩大公开入口。
- [ ] 6.2 处理L060–L063：集中重复资源路径构造、wire keys、HTTP/设备字段和同义校验器，保持token持久化及请求编码原值；补旧存储往返、父资源/编码逃逸、nonce/设备快照、未知字段/枚举和错误分类测试，更新存储/平台适配注释，运行models/session/platform/transport范围测试，保留独立协议字面值预期。
- [ ] 6.3 按tool-versions精确版本完成Flutter冻结安装、format/analyze/全部单元、真实accountsvc与嵌入策略协议、固定Git子目录独立消费及Android/iOS原生构建；按证据分别报告各层结果并更新SDK文档中的实际说明，缺macOS/Xcode/精确Flutter工具链时此任务保持未完成，不能用其他语言或近似版本替代。

## 7. TypeScript管理SDK

- [x] 7.1 处理L064–L066：统一内部资源名格式/构造/解码、HTTP标识和安全错误消息，filter字段const tuple派生原union与Set；覆盖父资源、未知响应、非法过滤、错误消息脱敏和取消不发迟到请求，更新内部注释，按精确Node/pnpm执行typecheck、format及全部单元，公开类型和入口保持。
- [ ] 7.2 对TS SDK运行真实accountsvc全部管理操作与权限拒绝、固定Git子目录独立安装/消费构建；复核SDK不解析角色、不新增登录/续期或重试职责，记录精确服务/SDK工具版本和证据，关联文档只更新实际结果，不创建版本或tag。

## 8. 示例与测试装配

- [ ] 8.1 处理L004/L067/L068：remoteauth复用既有token定义并整理安全错误/helper，codeemail与audit示例模式按自身归属具名；同步示例说明，运行remoteauth契约对照、renderer、audit及embedded可编译检查，示例宿主前缀/品牌与业务选择不强制共享。
- [ ] 8.2 处理L073–L077：fixture内复用Go权限/身份常量，收敛profile/控制路由/地址/origin/测试标签和测试配置声明；保留显式外部角色与启动环境隔离，基线用测试对照既有版本元数据，更新testsupport说明并执行带integration标签的真实依赖入口和两SDK控制协议回归，不新增生产SDK依赖或运行配置项。
- [ ] 8.3 核对测试装配跨Go/Dart/TS/shell的命名生产者与消费者一致，独立断言仍保留原路由/seed标记；用fixture readiness、pending/NeedsBinding重启、邮件控制码和清理/停止测试证明行为一致，更新操作手册中的命令及适用平台，未验证的平台分别记录。

## 9. 验证与发布工具

- [ ] 9.1 处理L070–L072：建立仅工具使用的SDK descriptor/方法集合，集中临时目录前缀与取消消息；保留路径/版本/归属/清理安全检查，更新脚本注释和工具说明，运行node工具测试及固定Git快照创建/取消/清理用例，覆盖Windows和Linux，不改公共SDK运行代码以消费工具元数据。
- [ ] 9.2 处理L069/L078：命名testgate action、迁移扫描目录/扩展名与恢复阶段/seed标记等测试协议，保留冻结哈希映射和shell原生环境引用；更新验证说明，用既有pass/fail/skip/missing流、浅克隆/历史迁移及恢复三阶段测试核对拒绝边界，禁止通过共享实现常量弱化独立预期。
- [ ] 9.3 运行全部相关node --test、check-openapi/check-sdk-coverage及SDK工具链/发布元数据检查；如常量化影响源码提取器，只增加正确解析支持并保留缺失/漂移/未知surface拒绝，核对coverage登记与示例命令，不关闭或放宽检查器，记录各工具输出。

## 10. 全仓闭环与综合验收

- [ ] 10.1 在GOWORK=off下完成Go build/vet/test、生成一致性和完整迁移历史检查，使用任务专用PostgreSQL17/Redis执行verify.sh的race/必需测试/真实恢复及verify-accountsvc.sh的实际进程门禁；root/平台/Docker条件skip逐项补验或列未完成，新增必需顶层测试同步两份相应登记，不修改已发布SQL和哈希。
- [ ] 10.2 综合运行两个SDK及fixture/工具的协议、固定Git消费、平台和跨语言装配门禁，核对它们独立于Go门禁的结果；确认源码包不依赖父目录/生成器/本地replace或安装构建，不改版本、锁文件和tag；缺依赖的必需检查保持未完成，文档不得报告为通过。
- [ ] 10.3 对78组逐项检查实际符号、拥有者、原值与保留理由，确认无遗留planned和未验证的应完成项；完成独立全分支规格/质量审查及修复，执行OpenSpec严格校验、链接/格式/LF/diff检查，verification记录实际命令、审查结论、阶段提交和未验证项，确认全仓兼容后再勾选完成。

## Workflow follow-up

- 实施与验证完成后，推送、合并、归档、版本修改、tag和发布分别按用户授权执行；本地提交和任务勾选不授权这些动作。
