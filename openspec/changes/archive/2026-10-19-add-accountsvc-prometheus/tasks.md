## 1. 服务装配

- [x] 1.1 形成引用本变更的实施计划，复核规格与测试覆盖；验证计划文件及 CLI apply 状态。
- [x] 1.2 先运行失败的配置与行为测试，再实现开关、标签校验、独立 Registry、Observer 注入和 GET /metrics；通过配置、隔离、采集错误脱敏、并发限制及无业务 I/O 测试并完成步骤审查。

## 2. 部署与集成

- [x] 2.1 更新 Compose、无秘密示例、服务/指标/网关/发布文档；核验开关默认关闭、变量一致及链接正确。
- [x] 2.2 增加真实服务操作到指标抓取和公网代理隔离测试，同步必需测试清单；使用一次性 PostgreSQL/Redis 执行服务完整门禁并检查真实进程采集结果。

## 3. 验收

- [x] 3.1 完成独立审查，在 GOWORK=off 下运行 build、vet、相关及全包测试、服务 Linux race 门禁；执行 openspec validate --all --strict、git diff --check，将实际结果及未验证项写入 verification.md。
