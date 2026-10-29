## MODIFIED Requirements

### Requirement: 服务令牌配置校验

serve SHALL 读取 `ACCOUNTSVC_SERVICE_ISSUER`、`ACCOUNTSVC_SERVICE_AUDIENCE`、`ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS`、`ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS`。两个客户端列表为 JSON 字符串数组，元素 SHALL 唯一、非空、不含空白或控制字符且不超过 256 字节；空数组视同未配置。任一列表非空时 SHALL 要求签发方和 audience；签发方或 audience 已设置而两个列表都为空 SHALL 视为配置错误。签发方 SHALL 为不含凭据、查询和片段的绝对 URL：`ACCOUNTSVC_MODE=production` 时 SHALL 只接受 HTTPS；`ACCOUNTSVC_MODE=development` 时 SHALL 另接受 `http://`，此时 discovery 地址与 `jwks_uri` 同样允许 HTTP，serve SHALL 在监听前记录一条指出 `ACCOUNTSVC_SERVICE_ISSUER` 的 WARN。签发方 SHALL 不等于消费者 JWT issuer，可以等于管理员 issuer。audience SHALL 非空且无首尾空白，SHALL 不等于消费者 JWT audience、已启用的管理员 audience、任一已配置的第三方登录应用或客户端 ID，以及任一允许的 `azp`。两个列表 SHALL 不相交。违反任一规则 SHALL 在监听前非零退出并指出变量名，不回显值。migrate SHALL 不解析这些变量。

#### Scenario: 列表相交
- **WHEN** 同一 `azp` 同时出现在两个客户端列表中
- **THEN** serve 启动失败并指出 `ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS`

#### Scenario: audience 冲突
- **WHEN** 服务 audience 等于消费者 audience、管理员 audience、某个第三方应用 ID 或某个允许的 `azp`
- **THEN** serve 启动失败并指出 `ACCOUNTSVC_SERVICE_AUDIENCE`

#### Scenario: 半配置
- **WHEN** 设置了客户端列表但缺少签发方或 audience，或设置了签发方/audience 但两个列表都为空
- **THEN** serve 启动失败并指出缺失或多余的变量名

#### Scenario: 生产模式拒绝 HTTP 签发方
- **WHEN** `ACCOUNTSVC_MODE` 为 production（含未设置）且 `ACCOUNTSVC_SERVICE_ISSUER` 为 `http://` 地址
- **THEN** serve 启动失败并指出 `ACCOUNTSVC_SERVICE_ISSUER`，不回显值

#### Scenario: 开发模式接受 HTTP 签发方
- **WHEN** `ACCOUNTSVC_MODE=development` 且 `ACCOUNTSVC_SERVICE_ISSUER` 为不含凭据、查询和片段的 `http://` 地址
- **THEN** serve 通过配置校验并记录一条指出 `ACCOUNTSVC_SERVICE_ISSUER` 的 WARN；Bearer 令牌按该 HTTP issuer 完成 discovery、JWKS 加载与 `iss` 精确匹配
