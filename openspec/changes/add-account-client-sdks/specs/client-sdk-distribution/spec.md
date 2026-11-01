## Purpose

定义两个官方客户端 SDK 的独立包身份、版本及可复现分发方式，使接入方能够从 accountkit Git 仓库的固定版本和子目录安装。约束源码边界、TypeScript 消费工具链和真实协议验证，避免包只能在维护者工作区运行或版本兼容性仅依赖文档声明。

## ADDED Requirements

### Requirement: 固定包身份与可独立消费

Flutter 包 SHALL 位于 sdk/accountkit-flutter，名称为 accountkit_flutter，公开入口为 lib/accountkit_flutter.dart；管理 TypeScript 包 SHALL 位于 sdk/accountkit-admin-ts，名称为 @accountkit/admin-client，公开入口为 src/index.ts。两包 SHALL 具备完整清单、许可、文档和直接依赖声明；消费 SHALL 不依赖父仓库工作区、未分发路径别名、本地绝对路径或 Go 构建。源码内部文件 SHALL 不作为受支持公共 API。

#### Scenario: 包独立安装
- **WHEN** 消费项目仅按 SDK 的 Git 路径安装依赖，不复制维护仓库的工作区配置
- **THEN** 能通过包名导入公开入口，解析直接依赖，不访问包目录外的实现源码

### Requirement: Git 固定引用与子目录分发

正式接入 SHALL 支持 Flutter 的 Git URL/path/ref 和 pnpm 的 Git ref/path 子目录依赖。说明与验收 SHALL 使用固定 commit 或不可重写 tag，不以 main/develop 等浮动分支作为发布基线。应用 SHALL 提交依赖锁文件并可使用冻结锁文件重复安装。Git 访问凭据 SHALL 由环境注入，不写入包清单、示例 URL 或提交的锁文件。

#### Scenario: 重复安装
- **WHEN** 两个干净环境使用同一固定 ref 和相同锁文件安装
- **THEN** 解析到同一 SDK 源码提交；冻结安装不悄悄升级到分支最新内容

#### Scenario: 私有 Git 仓库
- **WHEN** 应用使用环境提供的只读 Git 认证访问仓库
- **THEN** 能按相同子目录契约安装，文档和依赖文件不包含 token 或密码

### Requirement: TypeScript 源码分发

管理 SDK SHALL 直接导出 TypeScript 源码和可解析的类型信息，不要求预提交 dist、安装脚本编译或公共 npm 发布。支持的消费环境 SHALL 明确为能够转换该源码的工具链，并给出已验证的 Vite/TypeScript/pnpm 组合；不得宣称任意 JavaScript 运行时均可直接执行该包。SDK SHALL 使用可独立转换的模块语法，不依赖消费方未配置的编译插件或私有别名。

#### Scenario: Vite 消费源码
- **WHEN** 独立消费项目从 Git 子目录安装 SDK 后执行类型检查和生产构建
- **THEN** 构建工具将 SDK TS 源码转换为可执行 JS，不调用 SDK 安装时构建脚本或寻找不存在的 dist

### Requirement: 独立语义化版本与服务兼容矩阵

两个 SDK SHALL 独立维护 SemVer、变更记录和服务兼容矩阵，首个服务兼容基线为 v1.0.0。Flutter tag SHALL 使用 sdk/flutter/v<version>，管理 TS tag SHALL 使用 sdk/admin-ts/v<version>，tag 版本 SHALL 与对应包清单一致。SDK 与 Go 服务版本 SHALL 不要求同步递增；破坏性 SDK API 变化 SHALL 使用新主版本。已经分发的版本和 tag SHALL 不重写。创建/推送发布 tag 是单独发布动作，不由生成文档或普通实现任务隐式执行。

#### Scenario: 只修复管理 SDK
- **WHEN** 管理 SDK 修复不改变公开契约的错误
- **THEN** 仅递增其补丁版本并声明经过验证的服务版本，不要求 Flutter SDK 或 Go module 同步发版

#### Scenario: 错误的发布引用
- **WHEN** SDK tag 的版本与包清单不一致
- **THEN** 发布校验失败，不将该版本记录为可供接入的正式版本

### Requirement: 覆盖登记与真实协议验收

SDK SHALL 建立正式基线的 HTTP 路由/方法/模型覆盖表，并与 OpenAPI 和 handler 保持一致。模拟测试 SHALL 验证状态机及故障时序，真实账号服务测试 SHALL 验证实际编码、DTO、令牌与会话行为；只有单元测试通过不得声明真实集成通过。可选提供方的合成验证 SHALL 明确区别于平台真实授权验证；不能把其他语言服务端测试等同于 SDK 实际调用成功。

#### Scenario: 新增或遗漏端点
- **WHEN** 基线 HTTP 路由集合与 SDK 覆盖登记不一致
- **THEN** 检查指出缺失或额外操作，不以泛型请求入口代替公开能力的验证

#### Scenario: 缺少集成依赖
- **WHEN** 必需集成套件缺少数据库、Redis、服务进程或测试凭据
- **THEN** 必需门禁失败或明确列为未执行，不把跳过记作通过

### Requirement: 脱离工作区的消费验证

发布前 SHALL 在独立临时消费项目中以 Git 固定 commit 和子目录安装两个 SDK，验证依赖锁文件、类型检查、测试及消费构建。预发布可用包含待发布文件的本地 Git 提交对象验证相同解析路径；公共远端 tag 发布后 SHALL 再验证远端获取，不能将本地验证报告为远端验证。验证 SHALL 不使用 path/workspace override 或安装时编译掩盖包边界问题。

#### Scenario: 源码包引用外部文件
- **WHEN** SDK 入口或导入依赖维护仓库外部路径，工作区测试恰好能找到它
- **THEN** 独立消费验证失败，不允许据工作区测试发布该包

#### Scenario: 发布前后验证分层
- **WHEN** 发布前仅完成本地 Git 固定提交安装
- **THEN** 报告只声明本地 Git 子目录消费通过；远端 tag 可用后另行记录实际获取结果

### Requirement: 多语言门禁与证据独立

SDK CI SHALL 固定并记录消费工具链，运行各自格式/类型/静态分析、行为测试和独立消费验证，保留现有 Go 独立构建、数据库、race 与恢复门禁。结果 SHALL 区分 SDK 单元、真实服务契约、平台构建和远端分发验证。文档 SHALL 明确消费者 SDK、管理 SDK、Go 库和可选 accountsvc 的关系，不把下游产品迁移算作 SDK 已完成事项。

#### Scenario: SDK 变更触发验证
- **WHEN** 任一 SDK 或其兼容服务契约改变
- **THEN** 对应 SDK 及契约门禁执行，Go 的既有必需检查不会因新增语言而被删除或允许跳过
