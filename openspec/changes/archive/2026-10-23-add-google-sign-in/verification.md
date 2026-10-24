# 验收记录：add-google-sign-in

日期：2026-10-23。实现已由 `d3e9762` 合入 `develop`。

## 已核对

- 显示名上限为 128 个 Unicode 码位。本人更新与 `UpdateProfile` 接受 128、拒绝 129 和禁止字符。
- `GOOGLE = 5`。Required 不能包含它。Google ID token 使用原始 nonce、两个 iss、aud/azp 允许列表。无缓存的 JWKS 失败为不可用；缓存密钥在刷新失败后仍可验签，并单独计入 `google_cached_key_fallback`。
- 首次写入 Google 身份时，只填充仍为空的 `display_name` 和 `avatar_url`。直接注册、补全创建、补全关联和绑定都覆盖到。再次登录只更新非空 `provider_meta.profile`。微信 openid 合并保留已有 `profile`。已验证邮箱只作为 hint，不合并账号。
- pending 响应带 `hint_email`；省略 target 在 HTTP 返回 400 `CREDENTIAL_INCOMPLETE`。登录凭证增加互斥的 `google` 成员。身份列表和 reveal 的 DTO 没有 `provider_meta`。
- 当前 `0001` 只改了 `identity.kind` 与 `provider_meta` 注释。当前 up SHA-256 为 `c21e317a4007e6178553fe48ccd494b45475b14944788a4d74cc300257406872`。down、已发布 tag 和冻结源未改。`sqlc generate` 后 `user/db` 与 `audit/db` 无差异。

## 命令

在 `GOWORK=off` 下执行：

- `go test -count=1 ./enum ./user/idp ./user ./httpapi/enduser ./httpapi/admin ./observability ./observability/internal/metricdata ./internal/accountsvc ./internal/migrationcheck .`：通过。这一轮没有 `SERVER_TEST_DB_DSN`，`user` 里的数据库用例被跳过。
- 使用一次性 Postgres 17 容器后，`go test -count=1 ./user`：通过，包含 Google 资料填充、hint 和显示名用例。容器已删除。
- `go test -count=1 -run 'TestCompletionOmitsTargetInsteadOfUsingHint|TestSignInWithIdp|TestCredentialIdp' ./httpapi/enduser`：通过。
- `go vet` 覆盖上述改动包：通过。
- `openspec validate add-google-sign-in --strict --type change`：通过。
- `sqlc generate`：生成目录无差异。

## 未验证

- 未跑 `scripts/verify.sh`，没有恢复演练，没有 race。
- 未对 Google 的真实 JWKS 端点做在线请求；校验器用本地 JWKS 测试。

## 归档

2026-10-23 将 delta spec 同步到主规格后，变更目录移至 `openspec/changes/archive/2026-10-23-add-google-sign-in`。本记录没有需要按新深度改写的相对链接。
