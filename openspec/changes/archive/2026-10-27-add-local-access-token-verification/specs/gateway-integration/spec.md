## ADDED Requirements

### Requirement: JWKS 入口策略

接入说明 SHALL 将 `GET /.well-known/jwks.json` 列为独立入口。它只包含公钥，部署方 SHALL 能够按业务服务所在网络选择对公网开放或只供内网访问；开放 JWKS SHALL 不得连带开放内省、管理、探针或 `/v1/*` 通配路由。网关 SHALL 只放行 GET 和 HEAD 方法。网关缓存 SHALL 不长于服务端设置的公共缓存期限，以免轮换顺序失效。

#### Scenario: 对公网开放 JWKS
- **WHEN** 部署方在网关允许清单中加入 JWKS 路径
- **THEN** 外部业务服务可以获取公钥；内省、管理和探针仍按既有边界被拦截

#### Scenario: 仅内网访问 JWKS
- **WHEN** 业务服务全部在内网，部署方不对公网开放 JWKS
- **THEN** 内网业务服务可以直连获取公钥，公网请求被网关拒绝，不影响本地验签
