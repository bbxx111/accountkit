# 验证记录

## 2026-11-16：创建与ff准备

分支：`refactor/consolidate-string-contracts`，基于本地develop的`a11ea46`。用户已确认全仓范围，并明确授权创建变更及ff。原`configure-admin-permissions`未归档，不由本次改写或归档。

本轮仅准备[提案](proposal.md)、[设计](design.md)、[任务](tasks.md)及[78组候选清单](inventory.json)。`.openspec.yaml`设置`skip_specs: true`；本次保持既有行为，因此不新增或修改行为规格，CLI确认specs为skipped，不创建specs目录。

候选清单保留原值、位置、语义拥有者和任务映射，省去原临时报告的词法匹配上下文。盘点源基线为`7f43faa`，与a11ea46源码相同，仅合并验收文档不同；实施不依赖.test-output中的原报告。所有候选均为planned，实施任务保持未勾选。

### 已执行

- OpenSpec CLI为1.14.3；已读取当前项目root、创建/状态和各工件指令，proposal/design/tasks依赖齐备，specs显式跳过。
- `openspec validate --all --strict`：47项通过、0失败（45份主规格、原管理权限变更及本变更）。skip_specs的INFO为明确的纯重构声明，不是验证缺失豁免。

### 清单与工件核验

- 78个候选ID唯一，全部为planned；每项primaryTask均对应真实任务，共21个主要任务映射。30项实施任务均未勾选，apply状态为ready、0/30，不把工件齐备视为实现完成。
- 367处源文件行号引用有效，10个本地文档链接可解析；6个工件/支撑文件均位于本变更目录，无符号链接或路径越界，未创建与skip_specs冲突的specs目录。
- JSON解析、Markdown末尾换行/行尾空白及Git差异检查通过；盘点基线与当前起点的Git差异仅为旧变更验收记录，源码相同。
- 独立builder-check审查结论为spec PASS、quality PASS、`nothing important left`；已核对全仓覆盖、跨包依赖、78组任务映射、SDK独立消费、存储字节及平台门禁边界，未发现需补充决定的实质问题。原始审查报告保留于`.test-output/string-contracts-ff/review.md`。
- CLI最终状态为proposal/design/tasks完成，specs显式skipped；全量严格校验47/47通过。按AGENTS.md在本轮工件检查与审查通过后做一次本地阶段提交；未开始实施、未推送、未合并或归档。

### 实施后的证据边界

本轮未修改Go/Dart/TS、脚本、部署或主规格，未执行构建、运行测试、数据库/race/恢复、SDK真实服务/平台/远端消费检查。前一变更的日志和本轮规格校验不等于本次重构通过运行门禁。

apply后需按任务记录每阶段实际命令、独立审查、提交和清单闭环；精确SDK工具、macOS/Xcode等必需依赖缺失时相关任务保持未完成，不把skip或近似版本当作通过。临时日志和原始材料放.test-output，长期证据只维护本记录。

## 2026-11-16：实施基线与详细计划

使用已创建任务分支和当前工作区，沿用此前工作树创建受沙箱限制后的原地执行方式；不重复创建工作树。详细执行计划为[全仓实施计划](../../../docs/superpowers/plans/2026-11-16-consolidate-string-contracts.md)，执行依赖及写入范围明确；无共享写入的Go HTTP、Flutter、TS/工具按项目规则并行，Go领域及配置接入顺序实施。

- 起点`a94a070`，78组候选全部planned；30项任务映射及原值清单已核对。已有协议、审计、存储和拒绝边界断言作为行为基线，不为保持行为的纯提取编造先红；新增帮助函数先写测试观察缺失失败。
- Go1.26.5 windows/amd64、GOWORK=off，`go test -count=1 ./...`基线通过，日志`.test-output/string-contracts/baseline-go.log`；未配置数据库，skip不作为真实集成证据。
- 固定临时Git快照`2c0a898b9981ade4c6d43bd47fe53c892e8b5680`，本地代理版本`v1.1.1-0.20261115185502-2c0a898b9981`；旧角色宿主和新权限宿主两个独立module在GOWORK=off、无replace下tidy/build/run通过，原始记录`baseline-consumers/verification.json`。只用于兼容基线，不创建源分支tag或发布。
- 当前Flutter3.47.9/Dart3.13.7不等于要求3.47.4/3.13.3，精确工具另行准备；本机无macOS/Xcode，不把其他平台或近似版本当作原生门禁通过。现有产品容器均不操作，真实验证只创建本任务资源。

## 阶段：TS管理SDK与JS工具定义

- 完成L064–L066/L070–L072：SDK内部资源名/HTTP与安全消息同源，filter tuple派生原类型和Set；工具拥有SDK descriptor、冻结HTTP方法集合及创建/清理同源前缀。未扩大SDK入口或加入父目录运行依赖，未知SDK与覆盖拒绝规则保持。
- L066的create_time及已单份query tuple保留局部表达；L078的shell阶段/env目前保持原显式调用，Go恢复驱动与跨语言闭环交后续阶段，不提前记该组完成。
- 精确Node24.21.0/pnpm11.24.0：TS typecheck/format/test通过（56项）；node工具测试31通过、1项POSIX专属在Windows跳过；OpenAPI 24/11及SDK覆盖36操作通过。原始日志task3-*.log与实施报告位于.test-output/string-contracts。
- 独立审查task-3-review.md：spec/quality均通过，无实质finding。真实服务/Git完整消费、Linux取消补验和全仓门禁尚未执行；7.1已完成，7.2及9.1/9.3等综合要求保持未勾选。

## 阶段：Go HTTP/JOSE内部契约

- 新增internal/httpcontract、internal/jose及httpapi/internal/resourcename；Header/MIME、OAuth与同义reason、challenge、缓存策略、资源名和RequestID校验集中。保留authn.Reason*与tokens公开常量兼容，JWK/JWT算法复用现有定义，默认/rfc9068和三类身份验证不合并。
- 消费者/管理/三个服务HTTP面的reason按拥有者定义；status/reason/审计reason分别维护。filter/browser只共用字段名、kind映射和品牌/rank描述数据，输入解析和预算不改。
- 新helper测试先因缺少符号失败，再实现并通过；分区go test -count=1及最终重点回归通过。独立审查规格/质量通过、无实质finding；审查指出缺少vet空日志，root已重跑完整分区vet并明确保存task-1-vet.log，exit0。
- 根accountkit/config、accountsvc顶层和user/idp引用留后续阶段接入；L009/L011/L012/L036/L038/L046仅记录阶段进展，不提前关闭整个组，2.2–2.4仍待跨模块闭环。2.1与5.1已完成。

## 阶段：Flutter内部协议

- L057–L063完成：按认证/HTTP/wire/路由/Dio metadata及校验规则建立内部定义，实际匿名请求与排除集合共用7条路由；未知kind仍可扩展，token存储字节和epoch/revision/刷新重放逻辑保持。资源helper先以缺失方法测试失败，再实现并回归。
- 独立审查确认代码规格保持，唯一README中文被写成问号的问题已通过apply_patch修复并复核关闭；spec/quality均通过。77项单元包含独立匿名路由、application extra隔离、父资源和历史TokenRecord字节断言。
- 补充工具Flutter3.47.9/Dart3.13.7的77项测试和analyze通过；随后在任务目录准备精确Flutter3.47.4 revision9584c6713b / Dart3.13.3，冻结pub安装、format、analyze及77项单元均通过，日志flutter-3.47.4-*.log。首次任务SDK克隆因Windows长路径失败，仅在该临时克隆启用core.longpaths并完成检出，未修改全局或现有D:/flutter。
- 未改SDK入口、版本、依赖或锁；6.1/6.2完成，6.3的真实服务、Git和原生平台门禁继续待验证，不以单元测试替代。
