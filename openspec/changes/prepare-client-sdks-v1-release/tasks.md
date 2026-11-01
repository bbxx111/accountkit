## 1. 发布准备

- [x] 1.1 同步三个主规格并归档功能变更，修复引用，完成候选说明、严格校验与独立审查。
- [x] 1.2 核对 develop 基线完整 CI 与发布差异，发布准备以 merge commit 合入 develop。

## 2. 候选与发布

- [x] 2.1 develop 以 merge commit 合入 main，确认最终候选 Go/SDK 四项检查全部通过且 head SHA 精确一致。
- [x] 2.2 确认同名远端 tag 不存在，创建两个 annotated tag 和 GitHub Release，核对 tag 对象及解析 SHA。
- [x] 2.3 两个独立消费者使用真实远端 tag 完成冻结安装、静态/类型、行为和 TS 生产构建验证。

## 3. 发布补记

- [ ] 3.1 更新当前文档为已发布，记录候选、CI、tag、Release、消费证据与未验证边界；补记以 merge commit 集成 develop/main，tag 不移动。
