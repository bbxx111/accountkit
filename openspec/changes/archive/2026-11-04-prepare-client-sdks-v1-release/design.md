## Context

PR #20 的提交 7fd8d50200b66f7511158a8f0c125c8ce4c6bf88 已通过 Go 完整门禁、SDK 契约、Android 与 iOS CI，以 792bd6b4c1adc7a08cc14028d68f0bb1f51846d8 合入 develop。远端目前仅有 Go v1.0.0 tag/Release，两个 SDK 清单均为 1.0.0，兼容服务 v1.0.0。

## Goals / Non-Goals

发布两个可独立固定 Git 子目录消费的 SDK 首版，保留可追溯证据。不发布到 pub.dev/npm，不扩大许可，不升级 Go 服务版本，不改变运行时代码或产品配置，不将原生编译等同于真机/签名发布。

## Decisions

1. 两个 annotated tag 为 sdk/flutter/v1.0.0、sdk/admin-ts/v1.0.0，均固定同一个通过最终 CI 的 main 提交；对应包清单必须一致。既有 v1.0.0 不变。
2. 发布准备分支通过 PR merge commit 合入 develop，再由 develop 的 PR merge commit 合入 main；不使用 squash/rebase。最终 main 的 Go 与 SDK 全部检查通过后才能创建 tag。
3. 沿用现有完整 CI：Go workflow 在 tests/Dockerfile 镜像中执行库 race、数据库、迁移历史、恢复及服务信号验证；SDK workflow 执行真实服务/嵌入式契约、隔离消费与两个原生平台构建。归档/发布文档另做 strict、路径及候选元数据核验；不为纯文档重复创建本地数据库。最终候选在线证据替代不了发布后的真实远端消费。
4. docs/releases/client-sdks-v1.0.0.md 为两个 GitHub Release 的共同正文，候选提交保留候选措辞；两个 Release 均不设置为仓库 Latest，以保留 Go v1.0.0 的 Latest 含义。
5. tag 发布后分别以真实 HTTPS Git URL 和对应固定 tag 在仓库外安装，运行现有 verify-sdk-consumers 入口；Flutter platform none 仅证明远端安装、分析与行为，原生编译证据来自同一候选的 CI。
6. 发布完成后在补记分支更新状态和证据，再合入 develop/main；tag 不移动。发布执行记录保持活动状态，后续归档由单独请求决定。发布授权已由本轮用户明确提供，覆盖本设计的提交、推送、合并及 tag/Release 动作。

## Risks / Trade-offs

远端依赖下载可能失败，须区分网络与包边界问题；已发布 tag 不回写，真实缺陷用新版本修复。SDK 仍未授予外部复用许可；微信/Apple 真实平台授权、iOS 真机与签名发布及下游产品上线保留未验证。

## Migration Plan

归档功能 → 候选说明/严格检查/独立审查 → develop/main merge commit → 最终候选 CI → 两个 annotated tag/Release → 真实远端消费 → 已发布状态补记。初次接入使用新 tag 与 sdks 子目录；回退恢复应用原 ref 和锁文件，不迁移数据库。
