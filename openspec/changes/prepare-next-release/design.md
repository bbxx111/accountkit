# Design

## Context

用户确认保留v1，基线与审计见verification.md。本次采用v1.1.0候选：公开Go API为增量，但accountsvc配置移除、机器主体及scope要求、HTTP凭据边界和管理员启动行为有迁移影响。SDK已独立发布1.0.0，运行代码未变；0001和依赖也未变。

## Goals / Non-Goals

**Goals:** 保留github.com/bbxx111/accountkit及既有导入和路由，交付一致的v1.1.0候选说明、显式服务迁移边界、发布准备快照验证及可执行后续发布清单。

**Non-Goals:** 不新增运行能力、不恢复旧配置模式、不改变Go路径/SQL/依赖/SDK版本；不自行合入main、推送、打tag、发布Release或部署产品。

## Decisions

1. 保留v1是用户决定；新增能力使用v1.1.0候选而非补丁号。本次不建立accountsvc独立版本轴。不得将此版本描述为所有部署无缝兼容，也不据本次决定推导以后可随意破坏v1契约。
2. 发布说明集中列明：四项旧逐接口配置移除、注册表方法/权限迁移、Bearer专用sub与P∩scope、default/rfc9068新增、认证头边界（消费者新增16KiB）、管理员故障与readiness变化、身份解析新增及回退限制。azp模式值改名只属未发布实施过程，不作为v1.0.0升级项。
3. README、功能/兼容/Go集成/OpenAPI及服务手册统一标明v1.1.0候选，v1.0.0历史与SDK1.0.0已发布状态保持。OpenAPI info.version为v1.1.0，描述注明候选；路由与DTO不变，SDK覆盖baseline不因版本元数据机械重写。
4. SDK清单版本不动。兼容矩阵保留原v1.0.0已验证行，新增v1.1.0 pending行与本变更证据链接；基线SDK CI成功不冒充最终候选或正式tag消费通过，后续最终候选检查完成后再更新状态。
5. 现有发布流程不变：本轮准备文档及本地完整验证，提交到准备分支；后续获明确授权再合入develop/main、推送并等待最终main SHA的CI，随后annotated tag/Release及两个独立远端Go消费验证。本轮tasks仅跟踪准备工作，后续发布步骤列普通待办而不伪装已完成。
6. 采用既有Linux镜像和独立PG17/Redis，创建普通测试库及两个不同空恢复库。运行check-generated、check-migrations、OpenAPI、verify.sh（内含恢复三阶段）和verify-accountsvc.sh；非root单独核验权限用例。不得复用已seed恢复库重跑恢复脚本；原始材料留.test-output，清理只针对本任务资源。

## Risks / Trade-offs

- v1次版本含服务接入破坏性变化 → 用户选择和受影响边界在发布说明显著呈现；旧部署须迁移，源码可编译不代表令牌/配置可用。
- 新候选提交不同于已通过CI的62aaa820 → 分别记录本地快照、最终main SHA与正式tag，不互相替代。
- 回退恢复旧更宽授权 → 成套恢复二进制、配置和提供方/调用方凭据，不称安全等价回退，不清库或清Redis。
- SDK候选兼容尚未最终确认 → pending明确保留，不升版本或伪造已发布证据。

## Migration Plan

准备并审查候选说明 → 本地发布门禁 → 提交准备工作。后续明确授权后依次集成develop/main并验证最终CI、创建不可移动tag与Release、远端消费确认、补记已发布状态；产品环境验收独立进行。

## Publication Follow-up

用户后续明确授权合并、推送，并确认继续发布和归档流程。GitHub 登录暂不可用时，先在已通过 CI 的固定提交创建 tag 并独立完成远端消费；登录恢复后创建 Release。自动审批要求公告先去除“候选、尚未发布”，因此先基于已发布 Go tag 和实际验收证据更新正式说明，独立复核后创建 Release，随后合入补记和归档；此调整不修改已发布 tag，也不改变运行契约或产品部署边界。
