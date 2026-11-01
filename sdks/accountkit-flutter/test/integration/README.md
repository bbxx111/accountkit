# 真实 SDK 集成

这些入口刻意不使用 `_test.dart` 后缀；默认 `flutter test` 保持离线。显式执行时缺必需环境或依赖直接失败，无 skip 分支。全部消费者请求使用公开 AccountClient/Controller；dart:io 只访问测试控制接口读取验证码/合成第三方凭证。

从包目录执行默认 accountsvc 与嵌入 IdP 场景：

```powershell
$env:ACCOUNTKIT_FLUTTER_CONTROL_ORIGIN = 'http://127.0.0.1:18081'
$env:ACCOUNTKIT_FLUTTER_SERVICE_ORIGIN = 'http://127.0.0.1:18082'
$env:ACCOUNTKIT_FLUTTER_EMBEDDED_ORIGIN = 'http://127.0.0.1:18081'
flutter test test/integration/service_contract.dart --concurrency=1
```

可选宿主策略场景是独立必需入口，顺序运行同一专用 schema/Redis 前缀：先配置 REQUIRED_KINDS=[]，seed成功后停止宿主，再改REQUIRED_KINDS=EMAIL重启；两个阶段均为profile=custom-extended。测试验证同一第三方subject仍进入种子user_id，不能以两个独立账号伪造NeedsBinding：

```powershell
$env:ACCOUNTKIT_FLUTTER_POLICY_ORIGIN = 'http://127.0.0.1:18084'
$env:ACCOUNTKIT_FLUTTER_POLICY_CONTROL_ORIGIN = 'http://127.0.0.1:18084'
$env:ACCOUNTKIT_FLUTTER_POLICY_SUBJECT = 'unique-policy-subject'
flutter test test/integration/policy_seed.dart --concurrency=1
# 保存输出中的 SDK_POLICY_SEED_USER_ID，停止并按上述策略重启fixture后：
$env:ACCOUNTKIT_FLUTTER_POLICY_USER_ID = 'u_<seed-output-id>'
flutter test test/integration/policy_contract.dart --concurrency=1
```

无平台通道：注入 MemoryTokenStore、FixedDeviceProvider；网络与服务器均是真实实现。各 IdP 是受控 verifier/upstream 合成验证，不是平台真实原生授权。完整证据见本变更 verification.md，不能从默认单元测试推断这些场景已执行。
