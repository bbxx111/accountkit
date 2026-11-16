# Design

## Context

动机和全仓边界见[提案](proposal.md)。起点为develop的`a11ea46`；此前盘点基线`7f43faa`与之源码相同，仅合并验收文档不同。现有197个非`_test.go`且排除sqlc生成结果的Go文件，以及两个SDK、示例与工具中，共识别78组候选；21组优先、57组维护性收敛。完整值与位置在[inventory.json](inventory.json)，本变更不依赖临时盘点文件。

最近合入的管理权限改动已提供Permission、profile、scheme和服务权限等常量，但消费者认证仍裸比较bearer、根配置仍复制IdP默认值、HTTP reason与审计原因仍分散；Flutter匿名端点发送和凭据排除集合也分别维护。候选中48组偏向常量/既有符号复用，其余30组包含帮助函数、解析和数据目录，不能按字符串出现次数自动替换。

已读取HTTP凭据解析、通用审计、迁移安全、版本化存储和SDK分发等主规格，以及`configure-admin-permissions`的增量规格与设计。旧变更尚未归档，但其19项实现任务已完成；本次不回填旧任务，也不恢复旧内置管理角色授权。

## Goals / Non-Goals

**Goals:**

- 固定协议/业务标识有明确拥有者；多个位置同义时单一定义，已有定义优先复用，关键单处AAD/格式版本具名以便审查。
- 对78组候选逐组闭环，涵盖Go、两个SDK、示例、fixture和工具；保留不适合常量化的具体理由，不以数量指标决定完成。
- 小型构造帮助函数和数据定义只承接现有语义，外部可观察行为逐字节/逐分类保持。
- 各包可独立消费，公开Go签名、字段类型、既有导出符号和SDK公开入口保持兼容。

**Non-Goals:**

- 不改错误命名、状态码、响应头顺序/组合、认证与授权边界、环境变量、公开默认值、Redis故障策略或校验接受范围。
- 不重写filter、JSON Schema、客户端描述或浏览器识别算法，不新增表达式、规则、浏览器类型或受支持数据形状。
- 不修改生产SQL、冻结迁移/哈希、sqlc生成结果、模板正文和锁文件；不升级依赖，不新建代码生成器、全仓constants包或跨语言运行时协议包。
- 不把示例域名、合成数据、JSON tag、单次普通文案或独立测试预期机械改成常量；不创建tag或推进发布。

## Approaches

1. **仅按重复次数提取。** 改动表面较小，但无法区分USER的三种含义，也无法处理匿名端点、缓存头漏设和创建/清理前缀漂移；不采用。
2. **按语义归属收敛，全仓分阶段验证。** 常量、现有符号、小帮助函数和局部字段表各司其职，保持包边界；用户确认全仓纳入，采用此方案。
3. **统一全局字符串枚举或大规模解析器重构。** 容易造成领域→HTTP依赖、SDK父目录依赖和隐式行为调整，与兼容目标冲突；不采用。

## Decisions

### D1 候选清单与处理口径

`inventory.json`保留L001–L078、原值全集、已核对位置、建议拥有者及primaryTask；省去大体积词法匹配上下文，避免将同名误命中当替换指令。每组初始status为planned。实施后记录实际符号/文件与验证；若某些词已是单一映射或落在排除项，逐值说明保留理由。复杂解析器重构只记录本次的数据收敛边界，不能借该排除项搁置本来可完成的常量复用。

所有78组都要核对，不承诺每个候选词都新增常量。P1/P2仅表示整理顺序，不代表已发现漏洞。保留同值不同义：HTTP status与reason、HTTP reason与审计reason、ActorType与ResourceType、Facebook token USER与本地USER、refresh_token字段名与grant值分别拥有定义。

### D2 Go归属和依赖方向

优先在现有包内建`*_constants.go`或具体协议文件，不制造公共API。只有跨包的确是同一协议时新增内部叶子目录，设计选定如下：

| 位置/已有模块 | 拥有内容 | 不承担的职责 |
|---|---|---|
| `internal/httpcontract`（新增） | 真正共享的Header/MIME、OAuth与认证reason标识、challenge和缓存策略帮助函数 | 不验证token、角色、账号状态；不承接全部业务reason |
| `internal/jose`（新增） | Header/Claim/JWK用途与访问令牌类型文字 | 不签发/验签、不选issuer/audience，不合并三类身份 |
| `httpapi/enduser`、`httpapi/admin`、服务HTTP包 | 各面的业务reason、下一动作、内部拒绝分类 | 不作为user领域的依赖 |
| `audit/authentication`及各业务审计能力 | 认证资源类型、认证原因及对应业务action | 不复用ActorType表达资源类型，不扩大认证Actions目录 |
| `internal/idpdefaults`（新增） | 根配置与idp构造共同使用的六个默认endpoint | 不从网络或宿主读取配置，不改变根Validate时机 |
| `httpapi/internal/resourcename`（新增） | 两HTTP面共享的资源名构造 | 不共享DTO、不改变ID格式或资源归属校验 |
| 既有code、session、idp、SMTP、迁移、metricdata模块 | 自己的状态、键、格式、模式、表名和标签 | 不把跨业务同值字符串全部拉到全局 |

以上新增模块是内部实现组织，不新增外部业务能力。旧`authn.ReasonTokenMissing/Invalid`、`tokens.Algorithm/TokenType`等公开定义保留；迁移共同定义时用兼容别名，不删除/改类型。标准方法用`http.Method*`；RS256内部校验优先用当前jwt库的算法标识，公开const不能改成运行时函数值。标准时间布局在输出完全相同的前提下复用time.DateTime/time.RFC3339。

业务reason默认保持所属面私有；只有现有两个以上面确为同义的reason才进入内部公共目录。审计原因仍由审计映射层拥有；领域不得为复用文字导入HTTP包。`UNAVAILABLE`等同值的status、审计原因、指标分类不得串用。

### D3 HTTP帮助函数的等价边界

- `SetNoStore`只复现Cache-Control，`SetOAuthNoStore`按原调用点另加Pragma；不得向所有响应自动新增Pragma或改变公开JWKS的缓存策略。
- challenge构造支持当前realm缺失/error缺失的组合；保留各入口的精确值、多行WWW-Authenticate、401/403/503顺序和认证前业务输入不解析行为。不是把字符串拼接改成统一认证中间件。
- 资源名帮助函数只组装现有users/identities/sessions/profile/auditEvents路径。管理与消费者DTO、权限和SQL归属条件继续独立，动态ID仍按原规则校验。
- Header常量只覆盖具有共享维护需求的值；大小写不敏感比较可对定义值规范化，但输出头、设备输入和可信代理语义不变。RequestID共享验证函数不改变各宿主回调、生成和回退时机。

### D4 存储、审计和错误分类

验证码Go侧状态与Lua协议用独立测试校对，原Lua正文保留；Dimension仍为string。公共维度如确需命名，可按现有支持的Go/code包契约添加最小常量，不更改接口方法集或字段类型。

AAD、subject前缀、nonce scope、策略指纹、Redis键及schema_migrations名称都以旧字节为冻结基线。subject构造在各提供方内保留身份空间；迁移driver与旧引擎锁共用同一表名定义，但不改SQL和锁ID算法。审计原值如ALREADY_BOUND与IDENTITY_ALREADY_BOUND、LAST_ANCHOR与LAST_ANCHOR_IDENTITY不统一改名。USER_加状态的动态原因改明确映射时，必须保留当前所有实际可达分支输出及未知状态的原行为，不能新增或收窄分支。

已有enum名称数组继续是文本映射来源；优先在已有包内复用String映射/私有解析帮助函数，不为清理新增第二套名称或不必要的公开Parse API。新增业务action不加入认证enum.EventType，不改变审计过滤与隐私投影。

SQLSTATE使用已锁定依赖可用的具体码定义；25P04本地具名。08/53类前缀保持HasPrefix覆盖，身份解析额外的55P03/25P04/57014仍只在原调用处生效，不把各路径的可用性分类合并成一个更宽或更窄的集合。

### D5 配置、指标与既有解析器

模式、命令、环境前缀/后缀及完整变量名构造在各配置域集中。管理权限CSV去重与服务客户端CSV拒绝重复继续分开；别名下划线、空值残留字段和真实角色精确匹配不变。IdP默认值保留根配置填充及URL验证语义，不仅删除根默认赋值交给构造器。

metrics标签名/单位在已有metricdata模块共享，不改instrument名、标签集合、维度分类或高基数拒绝。现有Task/AuditField优先以具名值传递，移除不必要的字符串往返。

filter、浏览器、Schema、严格审计JSON与客户端描述只共用名称、允许集合、别名/rank数据和已有读取帮助函数，保留原switch/递归/预算/解析算法；敏感词本身已是规则集合，不逐词造公共常量。需要重写算法才能完成的额外优化不在本次实现内，清单记录原因，后续独立设计。

### D6 SDK与工具边界

Flutter在`lib/src`内部按认证协议、HTTP/设备、wire字段和Dio元数据分组定义；不导出大批新公共符号。内置kind仍允许自定义扩展；匿名路由发送与凭据排除集合必须同源；请求重放、epoch/revision和错误分支不改。动态资源路径与对应校验共用SDK内部函数。

TS在`src`内部收敛资源格式与安全错误；filter字段用const tuple派生现有union和运行时Set，保持公开类型的合法集合完全相同。两个SDK都不得导入父仓库或共享Go定义，不新建安装构建或运行时生成步骤。

脚本使用工具专用SDK descriptor/方法集合；fixture有自有profile、控制路由、origin变量和标签。生产SDK不依赖这些测试元数据，协议测试继续保留独立预期。跨语言优先从既有readiness/工具元数据消费，不增加新的运行配置、生成器或启动I/O。fixture基线可用本地具名值并由测试对照`tool-versions.json.serviceBaseline`，不新增BASELINE环境变量或包外嵌入依赖。

实施细化：仅测试fixture的既有readiness增加`policy_seed_marker`元数据，由Dart策略seed与shell提取器消费同一标记；stdout仍输出原`SDK_POLICY_SEED_USER_ID=`前缀，独立测试保留该原值。shell先精确校验元数据再提取ID；不新增环境变量、启动文件读取或生产SDK依赖。此增量仅影响测试控制元数据，不属于accountsvc生产HTTP契约。

临时目录前缀在创建和清理验证共用；保留真实路径、归属和边界核验，不能为了减少文本取消安全判断。恢复seed/verify/source-unchanged与独立源目标约束不变；shell保留普通环境变量引用，不制造复杂间接变量。Windows/Linux取消/退出、临时Git隔离、版本和路径检查都需要原行为验证。

### D7 覆盖、验证和提交

tasks按模块列出primaryTask和检查证据，inventory作为候选追踪数据，不把基于文本的自动替换器作为实施工具。每个实现步骤先补能揭示漂移的行为断言并确认当前行为；缺陷另按TDD修复，纯提取不能新增镜像实现测试。测试中原始协议值至少有独立断言，不能全部引用生产常量。

每组包括配套注释/文档及范围内测试，不把测试全部延迟到最后。检查、审查通过后按Conventional Commits本地提交，含tasks/verification及清单闭环；依赖未完成的工作不并行写同一模块。apply前另按仓库要求编写详细执行计划，本设计不是未验证的完成记录。

全仓综合验收包含Go独立构建/vet/race、真实PostgreSQL/Redis、迁移历史和恢复、accountsvc进程，两个SDK各自单元/真实服务/固定Git消费与平台门禁，工具测试与协议/生成覆盖检查。精确工具版本以`sdks/tool-versions.json`为准；缺依赖列未验证且相应任务不勾选，不重复使用旧CI作为本次证据。原始日志保留.test-output，维护结果只写本变更[verification.md](verification.md)。

## Risks / Trade-offs

- [常量归属造成依赖倒置或公共API扩张] → 内部叶子目录与私有默认，旧公开别名保留；独立Go/SDK消费验证。
- [同值不同义或安全格式被统一] → 清单记录拥有者，审计/HTTP/提供方/身份面分开，持久化与密码学固定向量及旧值读取回归。
- [帮助函数改变错误优先级、Pragma或challenge] → 保留调用顺序并做精确状态/响应头/消息/业务未执行断言，不能借重构修行为。
- [解析器/SQLSTATE支持集合变窄] → 不重写算法，集合与边界输入独立对照；类前缀及每调用方额外码保持。
- [多语言“共享”破坏独立分发] → SDK内部定义，跨语言只共享测试数据，不引用包外实现或增加安装构建。
- [过度抽象让代码更难读] → 只对稳定语义、复用和关键版本标识具名；单处普通文字/模板保留，候选可有证据地保留。
- [全仓改动难以审查] → 按任务依赖分阶段验证/审查/本地提交，78组逐项闭环；不以替换数量作为完成依据。

## Migration Plan

1. 以本变更清单和当前行为作为基线，先复用已有定义，再按Go协议/领域/配置、数据描述、SDK、示例fixture和工具推进。
2. 不要求宿主、部署或SDK使用方迁移调用；若现有签名、字节格式、合法输入或错误契约必须变化，停下该部分并另行确认行为变更，不能继续用skip_specs覆盖。
3. 全部相关门禁及独立审查通过后再集成；本地阶段提交不授权推送、合并、归档、版本修改、tag或发布。Go与两个SDK版本轴独立，本次不自动升版本。
4. 回退使用兼容的旧代码/SDK引用，不迁移或清理数据库/Redis；恢复验证保持专用空源/目标库。

## Open Questions

无影响方案或任务拆分的待定问题。各包私有常量的具体短名称、文件拆分及普通一次性值的逐项保留判断由实施者按上述规则决定，并在清单记录证据；不能据此改变语义或删减全仓范围。
