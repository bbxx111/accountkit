# 发布准备核验记录

状态：兼容性盘点阶段，版本策略待用户确认；尚无最终候选SHA。此记录不表示已获得打tag、创建Release、合并main或产品部署授权。

## 已确认基线

- 当前已发布Go版本v1.0.0；tag对象 `f497dec1dd487f18cb52e5ee3df85f438bde1831`，解析提交 `e1b93da7d050bce6e94766412a81f5c11d994b1f`，与远端一致。
- 当前准备起点为develop `62aaa82010f6ba69ec32daa60f898a3a2db73a85`；发布准备分支 `chore/prepare-next-release`。
- 该起点的[Go CI](https://github.com/bbxx111/accountkit/actions/runs/43661311566)与[SDK CI](https://github.com/bbxx111/accountkit/actions/runs/43661311567)全部通过。已查询确认check-generated、Verify package and recovery、Verify accountsvc with real dependencies and process signals均成功；SDK单元/真实服务/嵌入策略/Git消费以及Android/iOS检查成功。
- 以上仅为起点证据。发布说明、版本路径或其他文件变更后产生新候选，最终main候选须重新取得精确SHA对应的线上门禁。
- 从v1.0.0到起点，migrations、go.mod、go.sum无差异；不需要生产数据库升级，冻结0001保持。
- 远端只有Go v1.0.0及两个SDK的v1.0.0 tag，无远端v0 tag。本地历史v0引用不作为远端发布状态依据，未进行删除。
- main当前远端 `e6408f6a7909f3de7931f73c8c1b8573a59001cb`；仅计算的merge-tree预检无冲突，没有执行真实合并。

## 兼容性决策输入

- 独立公开源码审计确认：新增user.ResolveIdentityInput、user.ResolvedIdentity与Service.ResolveIdentity；已有公开Go符号未见删除或签名改变。消费者Authorization解析收紧，包含新增16KiB边界，不能将所有被拒绝的旧输入笼统称作语法非法。源码兼容不等于所有运行契约兼容。
- 相对正式v1.0.0，accountsvc移除旧逐接口客户端配置，新增统一注册权限，服务Bearer强制机器sub绑定与scope交集，构成服务接入契约变化。
- azp profile改名default发生在本轮未发布开发过程中，不能列作v1.0.0已发布配置的迁移项。
- 两SDK已有独立v1.0.0版本；相对其发布提交，包内仅README变动，未见运行时代码变化，不自动升SDK版本。

## 不依赖版本决策的准备

- 两份独立只读审计已完成，原始报告为 `.test-output/next-release/public-api-audit.md` 与 `service-contract-audit.md`。
- 兼容指南明确区分正式v1.0.0与下一候选；补齐三项服务能力、Basic=P/Bearer=P∩scope、机器主体、profile、认证头边界、管理员依赖故障与身份解析的升级摘要。
- 服务手册纠正默认三项能力及第三realm说明，明确双方式示例只授两项权限而非完整权限目录；不改变运行代码。
- accountsvc上线清单补齐三项内部入口、主体/scope配置及独立OIDC可用性验证；身份解析补入候选能力对照。独立文案复核通过，按反馈明确16KiB仅对消费者入口属于新增边界（管理员和服务v1.0.0已有上限）。
- 版本号未定前不改README/OpenAPI的正式版本注记，不生成版本号占位的正式Release说明，不把基线CI标为最终候选通过。

当前文档检查：OpenSpec全库strict 46/46通过（含本发布准备草案，不表示设计/任务已完成）；OpenAPI源码检查通过；git diff --check通过。未改Go/SQL/依赖，因此没有为此文档修正重复执行运行测试。

## 仍需确认

统一版本是否升级为v2.0.0并迁移Go module/import到/v2；若保留v1路径，先明确accountsvc的版本与兼容承诺。该决定影响发布版本、工件和实施范围，确认前不改Go模块路径、不签发版本号、不生成正式发布说明。

## 发布边界

真实提供方、产品网关、SMTP实际送达与目标环境恢复属于产品上线验收，不能由本地或CI夹具替代；库发布与产品部署按既有release-checklist分别执行。当前工作只进行发布准备，tag/Release、main集成和推送需按用户后续明确授权执行。
