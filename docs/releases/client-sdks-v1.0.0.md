# 客户端 SDK 1.0.0

状态：已发布（2026-11-01）。两个 tag 均固定到通过最终 Go/SDK CI 的提交 `1a6c60c41a330d568e8795c11c640197ce25c0e6`，两个 SDK 的真实远端 Git 消费均已通过。tag 内文档保留发布时的候选措辞，tag 不移动；发布对象、CI 与消费结果见[发布验收记录](https://github.com/bbxx111/accountkit/blob/develop/openspec/changes/prepare-client-sdks-v1-release/verification.md)。

| 包 | 版本 / 固定 tag | Git 子目录 | Release |
| --- | --- | --- | --- |
| accountkit_flutter | 1.0.0 / sdk/flutter/v1.0.0 | sdks/accountkit-flutter | [Flutter Release](https://github.com/bbxx111/accountkit/releases/tag/sdk%2Fflutter%2Fv1.0.0) |
| @accountkit/admin-client | 1.0.0 / sdk/admin-ts/v1.0.0 | sdks/accountkit-admin-ts | [TypeScript Release](https://github.com/bbxx111/accountkit/releases/tag/sdk%2Fadmin-ts%2Fv1.0.0) |

## 发布内容

- Flutter 消费者 SDK 覆盖 24 个正式 HTTP 操作，提供登录/补全、验证码、唯一会话与刷新协调、凭据存储、账号与身份管理，以及显式受信范围内的业务 Dio 认证。
- TypeScript 管理 SDK 覆盖 11 个正式管理操作，提供类型化 DTO/错误、分页过滤、取消及外部 token provider；不接管管理员登录或刷新，不自动重试写操作。
- 两包从 Git 固定 tag 的子目录独立消费，SDK 版本各自递增。兼容服务基线为 accountkit/accountsvc v1.0.0；本次不发布新的 Go module 版本，不修改服务端 HTTP、存储结构或冻结迁移。

## 接入与工具链

安装配置及调用方式见[SDK 指南](https://github.com/bbxx111/accountkit/blob/sdk%2Fflutter%2Fv1.0.0/docs/client-sdks.md)。应用须提交自己的锁文件，并用冻结安装确认依赖；不要使用 main/develop 浮动分支。曾试用旧目录 sdk/ 的调用方须改为上述 sdks/ 路径。

验收工具为 Flutter 3.47.4 / Dart 3.13.3、Node 24.21.0 / pnpm 11.24.0、TypeScript 5.8.3 / Vite 6.4.1。Flutter 清单最低 Flutter 3.47.1、Dart 3.13.0；iOS 编译要求 macOS 与 Xcode >=26.1.1。精确版本以[工具清单](https://github.com/bbxx111/accountkit/blob/sdk%2Fflutter%2Fv1.0.0/sdks/tool-versions.json)为准。

TypeScript 分发源码，由消费方 TypeScript/Vite 转换，Git 子目录语法使用 pnpm；不承诺 npm/yarn 或任意 JavaScript 运行时直接执行。Flutter 默认平台适配面向 Android/iOS，其他平台需显式替换适配器并验证。Go 使用方无需安装这些工具。

## 验证与边界

发布门禁涵盖 SDK 单元/静态、真实 accountsvc 与嵌入式策略契约、独立 Git 消费、Android debug APK、iOS 无签名构建，以及原有 Go race/数据库/恢复与 accountsvc 信号验证。具体候选 SHA、实际结果与发布后远端消费统一见发布验收记录。

原生编译不等于 iOS 真机/签名发布；微信/Apple 真实平台授权、实际服务商、产品网关与下游接入仍由宿主验收。本仓库及 SDK 未授予外部复用许可，沿用各包 LICENSE；本次是 Git/Release 分发，不是 pub.dev/npm 发布。

## 回退

首次接入可暂缓应用切换；已接入应用恢复其先前验证过的 ref 和锁文件。SDK 发布不要求数据库迁移，不回写、移动或覆盖已发布 tag；缺陷通过新的 SDK 版本交付。
