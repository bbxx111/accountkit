## ADDED Requirements

### Requirement: 服务资料补填配置
服务 SHALL 通过 `ACCOUNTKIT_PROVIDER_PROFILE_BACKFILL` 接受资料补填配置，未设置时视为开启。非法布尔值 SHALL 使 serve 与 migrate 在启动前以非零退出并指出该配置名，不泄露其他配置值。无秘密配置模板 SHALL 列出该项及其默认值。

#### Scenario: 关闭补填
- **WHEN** serve 设置 `ACCOUNTKIT_PROVIDER_PROFILE_BACKFILL=false`，其他配置有效
- **THEN** 服务正常启动，已有账号的第三方登录不补填空资料，新账号仍按通用规则填充

#### Scenario: 非法值
- **WHEN** `ACCOUNTKIT_PROVIDER_PROFILE_BACKFILL` 不是有效布尔值
- **THEN** 启动失败并指出 `ACCOUNTKIT_PROVIDER_PROFILE_BACKFILL`
