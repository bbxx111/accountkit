## 1. 实施准备

- [x] 1.1 复核基础 change 与本变更全部工件，以 writing-plans 形成引用本 change 的详细实施计划，核对访问契约和依赖顺序；计划文件及执行方式可评审后再开始编码。

## 2. 消费者 HTTP

- [x] 2.1 先写本人 GET、完整字段、空资料、旧适配器和未配置测试，再新增可选能力接口、DTO 与 GET 路由；验证旧 Service 方法集无需修改且新路由正确返回 200/503。
- [x] 2.2 先写身份与状态矩阵测试，再挂载两路由的 user scope 和 no-store；验证 401/403、禁止他人目标、FROZEN 只读、注销状态拒绝及无需近期重新认证。
- [x] 2.3 先写 PATCH 外层解析测试，再实现有界原始 JSON 读取；覆盖缺失/null/未知或重复外层字段、非法编码、尾随内容、自定义总大小上限和隐私日志，确认错误固定且内层字节保持。
- [x] 2.4 接入 PatchProfileAttributes 并完成安全错误映射；用测试验证嵌套合并、null 删除、数组替换、空补丁、未知键删除、readOnly/writeOnly 注解、大整数、16 KiB/深度/节点边界及完整结果 Schema 校验。
- [x] 2.5 通过根包公开 EndUserHandler 与真实 PostgreSQL 验证读写往返、双账号隔离、并发不重叠保留、状态竞争、更新时间和审计；复核错误与既有用户/JWT/内省响应不泄露资料，修复审查发现后进入下步。

## 3. accountsvc 装配

- [x] 3.1 先写可选 Schema 文件配置测试，再实现 serve 启动前有界读取及校验；覆盖空设置、合法/空/不存在/不可读/非普通/超限/非法文件、失败不泄露内容或路径，以及实例不热更新。
- [x] 3.2 补服务 HTTP 集成及 migrate 隔离测试，验证服务挂载同一协议、禁用返回 503、坏 Schema 阻止监听且不影响 migrate；复核 ConfigFromEnv/New 未增加文件 I/O。

## 4. 接入文档与验收

- [x] 4.1 更新 README、docs/openapi.md、docs/accountsvc.md、兼容说明、配置注释与无秘密模板，明确本人全部访问、Schema 注解、普通 user scope、Merge Patch 示例及服务配置；检查示例 JSON、链接和文档契约测试。
- [x] 4.2 更新相关规格的当前访问说明，明确基础 change 的原范围被本变更取代；基础 change 未归档时保留依赖及顺序，归档必须另行授权，历史验收不改写；检查无冲突的契约引用。
- [x] 4.3 按新增必需顶层测试维护 scripts/required-tests.txt，执行 GOWORK=off 构建、相关测试与 vet、真实数据库 HTTP/并发测试及可用环境下 race；将实际结果和未验证项写入本 change 的 verification.md，不以 skip 代替集成通过。
- [x] 4.4 按 requesting-code-review 复核规格、实现和兼容性，处理实质问题；执行 openspec validate --all --strict、差异及链接检查，以 verification-before-completion 核验交付证据，仅在完成并验证后勾选任务。
