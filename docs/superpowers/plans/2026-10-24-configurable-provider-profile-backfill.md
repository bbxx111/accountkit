# 提供方资料补填配置 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把第三方资料补填统一为与身份类型无关的规则：新建账号始终填充空资料，已有账号由默认开启的 `ProviderProfileBackfill` 配置决定是否补填。

**Architecture:** 配置沿用 `SensitiveOpVerification *bool` 的写法，经 `Config` → `user.Deps` → `Service` 传递。`user` 包删除按类型的 `refillsProfileOnSignIn`，`createProviderIdentity` 不再自行填资料；各调用点按“账号是否本次新建”调用新的 `backfillProviderProfile`，写入仍由 `seedEmptyProviderProfile` 在原事务内完成。

**Tech Stack:** Go 1.26.5、pgx/v5、PostgreSQL 17、Redis（miniredis / 真实 Redis）。

**Spec:** [OpenSpec 变更 configurable-provider-profile-backfill](../../../openspec/changes/archive/2026-10-24-configurable-provider-profile-backfill/proposal.md)（另见同目录 design.md、specs/、tasks.md）。

## Global Constraints

- 构建与测试在 `GOWORK=off` 下运行；修改 Go 文件后 gofmt。
- 配置名：Go 字段 `ProviderProfileBackfill *bool`（`Config` 与 `user.Deps`），环境变量 `<prefix>PROVIDER_PROFILE_BACKFILL`，`nil` / 未设置视为 `true`。
- 新建账号（直接注册、补全新建账号）始终填充空资料，不受配置影响。
- 任何路径不覆盖非空 `display_name` / `avatar_url`；补填只用本次声明，不用 `provider_meta` 中的历史 profile。
- `provider_meta.profile` 的记录与覆盖规则不受配置影响。
- 不改 HTTP、数据库、迁移、sqlc、指标、审计。
- 数据库测试需要 `SERVER_TEST_DB_DSN`；缺失时只能报告单元测试结果，不得宣称集成通过。
- 新增必需顶层测试追加到 `scripts/required-tests.txt`；本次删除的旧测试只能随规格替换同步移除，并在 verification.md 说明。

## OpenSpec 任务对应

| 计划任务 | OpenSpec tasks.md |
|---|---|
| Task 1 | 1.1、1.3 |
| Task 2 | 1.2、2.1–2.4 |
| Task 3 | 3.1–3.3 |
| Task 4 | 4.1–4.2 |

## 文件结构

| 文件 | 动作 | 职责 |
|---|---|---|
| `config.go` | 修改 | 字段、默认值、环境变量解析 |
| `config_test.go` | 修改 | 默认值、覆盖、非法值 |
| `accountkit.go` | 修改 | 传入 `user.Deps` |
| `user/service.go` | 修改 | `Deps` 字段、`nil` 默认、`Service` 保存布尔值 |
| `user/service_idp.go` | 修改 | 删除 `refillsProfileOnSignIn`，`verifyIdp` 统一净化，新增 `backfillProviderProfile` |
| `user/service_identity.go` | 修改 | `createProviderIdentity` 去掉填充；`BindWithIdp` 两个分支调用补填 |
| `user/service_signin_completion.go` | 修改 | `signInVerified`、`finalizeSignIn` 按账号新旧调用 |
| `user/service_profile_backfill_test.go` | 新建 | 统一规则的数据库测试 |
| `user/service_alipay_test.go`、`user/service_qq_test.go`、`user/service_weibo_test.go` | 修改 | 删除与新规则矛盾的 6 个测试 |
| `accountkit_weibo_http_test.go` | 修改 | 经 `Config` 关闭补填的端到端装配测试 |
| `internal/accountsvc/config_test.go` | 修改 | 服务非法值报告 |
| `scripts/required-tests.txt` | 修改 | 替换必需测试条目 |
| `deploy/accountsvc/.env.example`、`README.md`、`docs/*.md` | 修改 | 文档 |

---

### Task 1: 配置项（库与 accountsvc）

**Files:**
- Modify: `config.go:96-97`（字段）、`config.go:248-250`（默认值）、`config.go:607-614`（环境变量）
- Modify: `user/service.go:85-86`（Deps 字段）、`user/service.go:177-184`（默认值）、`user/service.go` 中 `type Service struct` 与 `NewService` 构造处
- Modify: `accountkit.go:232`
- Test: `config_test.go`、`internal/accountsvc/config_test.go`
- Modify: `deploy/accountsvc/.env.example`

**Interfaces:**
- Produces: `accountkit.Config.ProviderProfileBackfill *bool`；`user.Deps.ProviderProfileBackfill *bool`；`user.Service` 内部字段 `providerProfileBackfill bool`（Task 2 使用）。

- [ ] **Step 1: 写失败测试**

`config_test.go` 的 `TestValidateAppliesDefaults` 断言里在 `got.SensitiveOpVerification == nil || !*got.SensitiveOpVerification ||` 后追加：

```go
		got.ProviderProfileBackfill == nil || !*got.ProviderProfileBackfill ||
```

`TestConfigFromEnvOverridesAndParses` 在 `t.Setenv("T_SENSITIVE_OP_VERIFICATION", "false")` 后追加 `t.Setenv("T_PROVIDER_PROFILE_BACKFILL", "false")`，断言里在 `*got.SensitiveOpVerification ||` 后追加：

```go
		got.ProviderProfileBackfill == nil || *got.ProviderProfileBackfill ||
```

`TestConfigFromEnvReportsBadValues` 末尾追加：

```go
	t.Setenv("T_ACCESS_TOKEN_TTL", "")
	t.Setenv("T_PROVIDER_PROFILE_BACKFILL", "maybe")
	if _, err := accountkit.ConfigFromEnv("T_"); err == nil || !strings.Contains(err.Error(), "T_PROVIDER_PROFILE_BACKFILL") {
		t.Fatalf("bad boolean must name the variable: %v", err)
	}
```

`internal/accountsvc/config_test.go` 新增：

```go
func TestConfigProviderProfileBackfill(t *testing.T) {
	serveEnvironment(t)
	t.Setenv("ACCOUNTKIT_PROVIDER_PROFILE_BACKFILL", "false")
	c, err := LoadConfig("serve")
	if err != nil || c.Library.ProviderProfileBackfill == nil || *c.Library.ProviderProfileBackfill {
		t.Fatalf("backfill switch not loaded: %v", err)
	}
	for _, mode := range []string{"serve", "migrate"} {
		t.Run(mode, func(t *testing.T) {
			serveEnvironment(t)
			t.Setenv("ACCOUNTKIT_PROVIDER_PROFILE_BACKFILL", "sensitive-bool")
			_, err := LoadConfig(mode)
			if err == nil || !strings.Contains(err.Error(), "ACCOUNTKIT_PROVIDER_PROFILE_BACKFILL") || strings.Contains(err.Error(), "sensitive") {
				t.Fatalf("invalid switch: %v", err)
			}
		})
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `GOWORK=off go test -count=1 -run 'TestValidateAppliesDefaults|TestConfigFromEnvOverridesAndParses|TestConfigFromEnvReportsBadValues' . && GOWORK=off go test -count=1 -run TestConfigProviderProfileBackfill ./internal/accountsvc/`
Expected: 编译失败，`ProviderProfileBackfill` 未定义。

- [ ] **Step 3: 实现配置**

`config.go` 字段（紧跟 `SensitiveOpVerification`）：

```go
	// ProviderProfileBackfill 为 nil 时视为 true：已有账号在第三方登录、绑定或补全关联时
	// 用本次资料声明补填空的显示名与头像。新建账号始终填充，不受此项影响。
	ProviderProfileBackfill *bool
```

默认值（紧跟 `SensitiveOpVerification` 默认值）：

```go
	if c.ProviderProfileBackfill == nil {
		c.ProviderProfileBackfill = boolPtr(true)
	}
```

`ConfigFromEnv`（紧跟 `SENSITIVE_OP_VERIFICATION` 解析）：

```go
	if s := str("PROVIDER_PROFILE_BACKFILL"); s != "" {
		b, err := strconv.ParseBool(s)
		if err != nil {
			fail("PROVIDER_PROFILE_BACKFILL", err)
		} else {
			c.ProviderProfileBackfill = boolPtr(b)
		}
	}
```

`user/service.go` 的 `Deps`（紧跟 `SensitiveOpVerification`）：

```go
	// ProviderProfileBackfill 为 nil 时默认启用；false 时已有账号不因第三方登录、绑定或补全关联改写资料。
	ProviderProfileBackfill *bool
```

`NewService` 默认值处理（紧跟 `SensitiveOpVerification` 块）：

```go
	backfill := d.ProviderProfileBackfill == nil || *d.ProviderProfileBackfill
	d.ProviderProfileBackfill = &backfill
```

`type Service struct` 增加 `providerProfileBackfill bool`，在构造 `&Service{...}` 处赋值 `providerProfileBackfill: *d.ProviderProfileBackfill`（按现有字段对齐格式）。

`accountkit.go` 在 `SensitiveOpVerification: cfg.SensitiveOpVerification,` 后增加：

```go
		ProviderProfileBackfill: cfg.ProviderProfileBackfill,
```

`deploy/accountsvc/.env.example` 在第三方提供方配置段前（`# Explicit GOOGLE ...` 之前）增加：

```
# Existing accounts backfill empty display name / avatar from provider claims on sign-in,
# binding and completion linking. New accounts are always filled. Default true.
# ACCOUNTKIT_PROVIDER_PROFILE_BACKFILL=true
```

- [ ] **Step 4: 运行确认通过**

Run: 同 Step 2，另加 `GOWORK=off go vet . ./user/ ./internal/accountsvc/`
Expected: PASS。若 accountsvc 非法值测试未提取出配置名，检查 `safeLibraryConfigError` 的 `libraryErrorName`：错误格式为 `accountkit: ACCOUNTKIT_PROVIDER_PROFILE_BACKFILL: ...`，应已匹配，无需新增映射。

- [ ] **Step 5: 提交**

```bash
gofmt -l config.go config_test.go accountkit.go user/service.go internal/accountsvc/config_test.go
git add config.go config_test.go accountkit.go user/service.go internal/accountsvc/config_test.go deploy/accountsvc/.env.example
git commit -m "feat(user): add ProviderProfileBackfill configuration"
```

---

### Task 2: 统一补填逻辑

**Files:**
- Create: `user/service_profile_backfill_test.go`
- Modify: `user/service_idp.go:44-77`、`user/service_identity.go:236-262,285-316`、`user/service_signin_completion.go:96-104,530-560`
- Modify: `user/service_alipay_test.go`（删除 `TestAlipayExceptionDoesNotBackfillOtherProviders`、`TestAlipayRepeatBindingDoesNotBackfill`）、`user/service_qq_test.go`（删除 `TestQQExceptionDoesNotBackfillOtherProviders`、`TestQQRepeatBindingDoesNotBackfill`）、`user/service_weibo_test.go`（删除 `TestWeiboRefillDoesNotBackfillOtherProviders`、`TestWeiboRepeatBindingDoesNotBackfill`）
- Modify: `accountkit_weibo_http_test.go`
- Modify: `scripts/required-tests.txt`

**Interfaces:**
- Consumes: `user.Deps.ProviderProfileBackfill`、`Service.providerProfileBackfill`（Task 1）。
- Produces: `func (s *Service) backfillProviderProfile(ctx context.Context, q *db.Queries, userID string, ident idp.Identity, newAccount bool) error`；`createProviderIdentity` 签名不变但不再写账号资料。

- [ ] **Step 1: 写失败测试**

新建 `user/service_profile_backfill_test.go`：

```go
package user_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
)

// backfillKinds 覆盖原补填集合内外的提供方；合成提供方返回请求里的 name/picture。
var backfillKinds = []enum.IdentityKind{enum.IdentityAlipay, enum.IdentityQQ, enum.IdentityWeibo, enum.IdentityGoogle, enum.IdentityFacebook, corpSSO}

func boolRef(b bool) *bool { return &b }

var backfillModes = []struct {
	name string
	on   *bool
	want bool
}{{"default", nil, true}, {"on", boolRef(true), true}, {"off", boolRef(false), false}}

func useBackfillProvider(t *testing.T, f *fixture, kind enum.IdentityKind, on *bool, required []enum.IdentityKind) {
	t.Helper()
	newServiceWith(t, f, func(d *user.Deps) {
		*d = withProvider(*d, alipayProvider(kind, nil))
		d.RequiredIdentityKinds = required
		d.ProviderProfileBackfill = on
	})
}

func laterCred(kind enum.IdentityKind) user.IdpCredential {
	return providerCred(kind, map[string]string{"sub": "person", "app": "app-one", "openid": "open-one", "name": "Later", "picture": "https://example.test/later.png"})
}

func emptyCred(kind enum.IdentityKind) user.IdpCredential {
	return providerCred(kind, map[string]string{"sub": "person", "app": "app-one", "openid": "open-one"})
}

func assertProviderMetaHas(t *testing.T, f *fixture, uid string, kind enum.IdentityKind, text string) {
	t.Helper()
	rows, err := f.repo.Q().ListActiveIdentitiesByUser(context.Background(), uid)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Kind == kind && strings.Contains(string(row.ProviderMeta), text) {
			return
		}
	}
	t.Fatalf("provider metadata for %s lacks %q", kind, text)
}

func expectedProfile(want bool) (string, string) {
	if want {
		return "Later", "https://example.test/later.png"
	}
	return "", ""
}

func TestProviderProfileBackfillOnSignIn(t *testing.T) {
	for _, kind := range backfillKinds {
		for _, mode := range backfillModes {
			t.Run(string(kind)+"/"+mode.name, func(t *testing.T) {
				f := newFixture(t)
				useBackfillProvider(t, f, kind, mode.on, []enum.IdentityKind{})
				first := requireSignInTokens(t, mustSignInWithIdp(t, f, emptyCred(kind)))
				again := requireSignInTokens(t, mustSignInWithIdp(t, f, laterCred(kind)))
				if again.UserID != first.UserID || again.IsNewUser {
					t.Fatal("later sign-in changed user")
				}
				name, avatar := expectedProfile(mode.want)
				assertAlipayProfile(t, f, first.UserID, name, avatar)
				assertProviderMetaHas(t, f, first.UserID, kind, "Later")
			})
		}
	}
}

func TestProviderProfileBackfillKeepsNonEmpty(t *testing.T) {
	f := newFixture(t)
	useBackfillProvider(t, f, enum.IdentityGoogle, nil, []enum.IdentityKind{})
	first := requireSignInTokens(t, mustSignInWithIdp(t, f, emptyCred(enum.IdentityGoogle)))
	mine := "Mine"
	if _, err := f.svc.UpdateProfile(context.Background(), first.UserID, user.ProfilePatch{DisplayName: &mine}); err != nil {
		t.Fatal(err)
	}
	mustSignInWithIdp(t, f, laterCred(enum.IdentityGoogle))
	assertAlipayProfile(t, f, first.UserID, "Mine", "https://example.test/later.png")
}

func TestProviderProfileBackfillOnBinding(t *testing.T) {
	for _, kind := range []enum.IdentityKind{enum.IdentityAlipay, enum.IdentityGoogle} {
		for _, mode := range backfillModes {
			t.Run(string(kind)+"/"+mode.name, func(t *testing.T) {
				ctx := context.Background()
				f := newFixture(t)
				useBackfillProvider(t, f, kind, mode.on, nil)
				phone := f.signIn(t, enum.IdentityPhone, phone1, dev1)
				p := principalOf(t, f, phone)
				if _, created, err := f.svc.BindWithIdp(ctx, p, laterCred(kind), meta1); err != nil || !created {
					t.Fatal(err)
				}
				name, avatar := expectedProfile(mode.want)
				assertAlipayProfile(t, f, phone.UserID, name, avatar)
				empty := ""
				if _, err := f.svc.UpdateProfile(ctx, phone.UserID, user.ProfilePatch{DisplayName: &empty, AvatarURL: &empty}); err != nil {
					t.Fatal(err)
				}
				if _, created, err := f.svc.BindWithIdp(ctx, p, laterCred(kind), meta1); err != nil || created {
					t.Fatal(err)
				}
				assertAlipayProfile(t, f, phone.UserID, name, avatar)
				assertProviderMetaHas(t, f, phone.UserID, kind, "Later")
			})
		}
	}
}

func TestProviderProfileBackfillOnCompletionLink(t *testing.T) {
	for _, mode := range backfillModes {
		t.Run("new-identity/"+mode.name, func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t)
			useBackfillProvider(t, f, enum.IdentityGoogle, mode.on, nil)
			owner := f.signIn(t, enum.IdentityEmail, email1, dev2)
			pending := mustSignInWithIdp(t, f, laterCred(enum.IdentityGoogle))
			if pending.Pending == nil {
				t.Fatal("expected pending")
			}
			cred := completionCredential(t, f, pending.Pending, enum.IdentityEmail, email1)
			done, err := f.svc.CompleteSignIn(ctx, pending.Pending.SignInCompletionToken, cred, dev1, meta1)
			if err != nil || requireSignInTokens(t, done).UserID != owner.UserID {
				t.Fatalf("completion did not link owner: %v", err)
			}
			name, avatar := expectedProfile(mode.want)
			assertAlipayProfile(t, f, owner.UserID, name, avatar)
		})
		t.Run("owned-proof/"+mode.name, func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t)
			useBackfillProvider(t, f, enum.IdentityAlipay, mode.on, nil)
			owner := f.signIn(t, enum.IdentityEmail, email1, dev2)
			pending := mustSignInWithIdp(t, f, laterCred(enum.IdentityAlipay))
			if pending.Pending == nil {
				t.Fatal("expected pending")
			}
			bound, _, err := f.svc.BindWithIdp(ctx, principalOf(t, f, owner), emptyCred(enum.IdentityAlipay), meta1)
			if err != nil {
				t.Fatal(err)
			}
			alipayPendingPayload(t, f, func(p map[string]any) {
				proof := p["Proofs"].([]any)[0].(map[string]any)
				proof["UserID"] = owner.UserID
				proof["IdentityID"] = bound.ID
			})
			cred := completionCredential(t, f, pending.Pending, enum.IdentityEmail, email1)
			done, err := f.svc.CompleteSignIn(ctx, pending.Pending.SignInCompletionToken, cred, dev1, meta1)
			if err != nil || requireSignInTokens(t, done).UserID != owner.UserID {
				t.Fatalf("completion did not use owned proof: %v", err)
			}
			name, avatar := expectedProfile(mode.want)
			assertAlipayProfile(t, f, owner.UserID, name, avatar)
		})
	}
}

func TestProviderProfileBackfillDisabledStillFillsNewAccounts(t *testing.T) {
	ctx := context.Background()
	direct := newFixture(t)
	useBackfillProvider(t, direct, enum.IdentityGoogle, boolRef(false), []enum.IdentityKind{})
	res := requireSignInTokens(t, mustSignInWithIdp(t, direct, laterCred(enum.IdentityGoogle)))
	if !res.IsNewUser {
		t.Fatal("expected direct registration")
	}
	assertAlipayProfile(t, direct, res.UserID, "Later", "https://example.test/later.png")

	completed := newFixture(t)
	useBackfillProvider(t, completed, enum.IdentityAlipay, boolRef(false), nil)
	pending := mustSignInWithIdp(t, completed, laterCred(enum.IdentityAlipay))
	if pending.Pending == nil {
		t.Fatal("expected pending")
	}
	cred := completionCredential(t, completed, pending.Pending, enum.IdentityEmail, email1)
	done, err := completed.svc.CompleteSignIn(ctx, pending.Pending.SignInCompletionToken, cred, dev1, meta1)
	if err != nil || !requireSignInTokens(t, done).IsNewUser {
		t.Fatalf("completion did not create an account: %v", err)
	}
	assertAlipayProfile(t, completed, requireSignInTokens(t, done).UserID, "Later", "https://example.test/later.png")
}
```

`accountkit_weibo_http_test.go` 末尾追加（经 `Config` 关闭补填的端到端装配）：

```go
func TestWeiboHTTPProfileBackfillDisabled(t *testing.T) {
	requireAlipayHTTPDependencies(t)
	const app = "1234567890"
	s := newWeiboHTTPServer(t, map[string]weiboHTTPGrant{
		"first": {app: app, uid: "2000000001", profileUnavailable: true},
		"later": {app: app, uid: "2000000001", name: "Weibo name", avatar: "https://tvax1.sinaimg.cn/hd.jpg"},
		"new":   {app: app, uid: "2000000002", name: "New account", avatar: "https://tvax1.sinaimg.cn/new.jpg"},
	})
	cfg := s.config(app)
	off := false
	cfg.ProviderProfileBackfill = &off
	f := newAlipayHTTPFixture(t, cfg)
	token := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", weiboLoginBody(app, "first"), 200))
	token = f.tokenResult(f.call("POST", "/users:signInWithIdp", "", weiboLoginBody(app, "later"), 200))
	f.assertMe(token, "", "")
	fresh := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", weiboLoginBody(app, "new"), 200))
	f.assertMe(fresh, "New account", "https://tvax1.sinaimg.cn/new.jpg")
}
```

- [ ] **Step 2: 运行确认失败**

Run: `GOWORK=off go test -count=1 -run 'TestProviderProfileBackfill' ./user/`（需 `SERVER_TEST_DB_DSN`）
Expected: FAIL——GOOGLE/FACEBOOK/CORP_SSO 的 default/on 子测试未补填；ALIPAY/QQ/WEIBO 的 off 子测试仍补填；绑定 off 子测试仍填充；同账号重复绑定 on 子测试未补填。确认失败原因是行为而非编译或夹具错误。

- [ ] **Step 3: 删除与新规则矛盾的旧测试**

删除 6 个测试函数：`TestAlipayExceptionDoesNotBackfillOtherProviders`、`TestAlipayRepeatBindingDoesNotBackfill`、`TestQQExceptionDoesNotBackfillOtherProviders`、`TestQQRepeatBindingDoesNotBackfill`、`TestWeiboRefillDoesNotBackfillOtherProviders`、`TestWeiboRepeatBindingDoesNotBackfill`。它们断言的“集合外不补填 / 重复绑定不补填”已被 `TestProviderProfileBackfillOnSignIn`、`TestProviderProfileBackfillOnBinding` 的 off 与 on 子测试取代。删除后若有仅被它们使用的 import，一并清理。

`scripts/required-tests.txt`：删除上述 6 行，并在 `user/TestProviderPolicy...` 附近按字母序（文件现有顺序为按包分组）追加：

```
github.com/bbxx111/accountkit/user/TestProviderProfileBackfillDisabledStillFillsNewAccounts
github.com/bbxx111/accountkit/user/TestProviderProfileBackfillKeepsNonEmpty
github.com/bbxx111/accountkit/user/TestProviderProfileBackfillOnBinding
github.com/bbxx111/accountkit/user/TestProviderProfileBackfillOnCompletionLink
github.com/bbxx111/accountkit/user/TestProviderProfileBackfillOnSignIn
github.com/bbxx111/accountkit/TestWeiboHTTPProfileBackfillDisabled
```

（插入位置跟随文件中相邻条目的既有排序，不重排其他行。）

- [ ] **Step 4: 实现**

`user/service_idp.go`：`verifyIdp` 中把

```go
	// Pending proofs retain only the current, sanitized claims of sign-in refill
	// providers. Other providers keep their existing proof and first-identity profile semantics.
	if refillsProfileOnSignIn(cred.Kind) {
		profile := providerProfileFrom(ident)
		ident.Name, ident.Picture = "", ""
		if profile != nil {
			ident.Name, ident.Picture = profile.Name, profile.Picture
		}
	}
	return ident, nil
```

改为

```go
	// Pending proofs and later backfill retain only the current, sanitized claims.
	profile := providerProfileFrom(ident)
	ident.Name, ident.Picture = "", ""
	if profile != nil {
		ident.Name, ident.Picture = profile.Name, profile.Picture
	}
	return ident, nil
```

删除 `refillsProfileOnSignIn` 及其注释，在原位置新增：

```go
// backfillProviderProfile 用本次声明填充空的显示名与头像：新建账号始终填充，
// 已有账号仅在 ProviderProfileBackfill 开启时补填。
func (s *Service) backfillProviderProfile(ctx context.Context, q *db.Queries, userID string, ident idp.Identity, newAccount bool) error {
	if !newAccount && !s.providerProfileBackfill {
		return nil
	}
	return s.seedEmptyProviderProfile(ctx, q, userID, ident)
}
```

`user/service_identity.go` 的 `createProviderIdentity`：删除

```go
	if err := s.seedEmptyProviderProfile(ctx, q, userID, ident); err != nil {
		return db.Identity{}, err
	}
```

并把函数注释改为 `// createProviderIdentity 创建第三方身份行。空 openid 与空 profile 不落库；账号资料由调用方按账号新旧补填。`

`BindWithIdp` 同账号分支：

```go
		case err == nil && existing.UserID == p.UserID:
			if err := s.refreshProviderMeta(ctx, q, existing, ident, now); err != nil {
				return err
			}
			if err := s.backfillProviderProfile(ctx, q, p.UserID, ident, false); err != nil {
				return err
			}
```

新建身份分支在 `row, err := s.createProviderIdentity(...)` 的错误处理之后、`info, created = identityInfoFrom(row), true` 之前：

```go
		if err := s.backfillProviderProfile(ctx, q, p.UserID, ident, false); err != nil {
			return err
		}
```

`user/service_signin_completion.go`：

`createProofIdentity` 增加参数 `newAccount bool`，第三方分支在创建身份后补填：

```go
func (s *Service) createProofIdentity(ctx context.Context, q *db.Queries, uid string, p completionProof, newAccount bool) (db.Identity, error) {
	// 锚点分支保持原样
	...
	row, err := s.createProviderIdentity(ctx, q, uid, *p.Provider)
	if err != nil {
		return db.Identity{}, err
	}
	if err := s.backfillProviderProfile(ctx, q, uid, *p.Provider, newAccount); err != nil {
		return db.Identity{}, err
	}
	return row, nil
}
```

（先读 `createProofIdentity` 现有锚点分支，原样保留；只改第三方分支的 `return s.createProviderIdentity(...)`。）

`signInVerified` 已有身份分支：

```go
				if err = s.backfillProviderProfile(ctx, q, u.ID, *proof.Provider, false); err != nil {
					return err
				}
```

替换原 `if refillsProfileOnSignIn(proof.Kind) { ... }` 整块。新账号分支调用改为 `s.createProofIdentity(ctx, q, uid, proof, true)`。

`finalizeSignIn` 已归属分支：

```go
					if e = s.backfillProviderProfile(ctx, q, u.ID, *proof.Provider, false); e != nil {
						return e
					}
```

替换原 `if refillsProfileOnSignIn(proof.Kind) { ... }` 整块；新建身份调用改为 `s.createProofIdentity(ctx, q, u.ID, proof, isNew)`。

最后 `rg -n "createProofIdentity\(|refillsProfileOnSignIn" user/` 确认所有调用都已传 `newAccount`，且旧函数无残留。

- [ ] **Step 5: 运行确认通过**

Run（需 `SERVER_TEST_DB_DSN`）：

```bash
GOWORK=off go test -count=1 -run 'TestProviderProfileBackfill' -v ./user/ 2>&1 | rg -- '--- (FAIL|SKIP)|^ok|^FAIL'
GOWORK=off go test -count=1 ./user/
GOWORK=off go test -count=1 -run 'TestWeiboHTTP|TestQQ|TestAlipay|Google|OIDC|Facebook' .
```

Expected: 新测试全部 PASS 且无 SKIP；`./user/` 全包通过（原支付宝/QQ/微博后续补填、并发 PATCH、冻结/注销竞争、pending 并发、最后阶段失败回滚、Google 资料测试均不改动并通过）；根包 HTTP 用例需要真实 PostgreSQL 与 Redis，若 `requireAlipayHTTPDependencies` 跳过，记录为未验证。

- [ ] **Step 6: 静态检查**

Run: `gofmt -l user/ accountkit_weibo_http_test.go && GOWORK=off go vet ./...`
Expected: 无输出。

- [ ] **Step 7: 提交**

```bash
git add user/ accountkit_weibo_http_test.go scripts/required-tests.txt
git commit -m "feat(user): unify provider profile backfill across identity kinds"
```

---

### Task 3: 文档

**Files:**
- Modify: `README.md`（配置表约第 429 行附近；资料补填说明，用 `rg -n "补填|首次填充|display_name" README.md` 定位）
- Modify: `docs/go-integration.md`（约第 45 行“登录补填提供方（ALIPAY、QQ、WEIBO）”）
- Modify: `docs/accountsvc.md`（第三方配置段）
- Modify: `docs/compatibility.md`（第 48、58 行现行描述；新增本次段落）
- Modify: `docs/alipay-sign-in.md`（约第 88 行）、`docs/qq-sign-in.md`、`docs/weibo-sign-in.md` 中“后续登录补填”段

- [ ] **Step 1: README**

配置表在 `REAUTH_MAX_AGE` / `SENSITIVE_OP_VERIFICATION` 行后加：

```markdown
| `PROVIDER_PROFILE_BACKFILL` | `true` | 已有账号在第三方登录、绑定或补全关联时，用本次资料声明补填空的显示名与头像；新建账号始终填充，不受此项影响 |
```

资料补填说明统一为：“第三方提供方返回昵称或头像时，新建账号始终填充空的显示名与头像；已有账号在 `PROVIDER_PROFILE_BACKFILL=true`（默认）时，每次第三方登录、绑定或补全关联都补填仍为空的字段，不覆盖非空值，只使用本次声明。用户主动清空也会在下一次第三方登录时被补填；不希望如此时设为 `false`。”删除按 ALIPAY/QQ/WEIBO 区分的旧描述。

- [ ] **Step 2: Go 集成与 accountsvc 文档**

`docs/go-integration.md`：把“登录补填提供方（ALIPAY、QQ、WEIBO）的后续成功登录会……”改为引用统一规则与 `Config.ProviderProfileBackfill`。`docs/accountsvc.md`：在库配置说明中加入 `ACCOUNTKIT_PROVIDER_PROFILE_BACKFILL`（默认 `true`，非法值启动失败并指出该名称）。

- [ ] **Step 3: 兼容指南**

`docs/compatibility.md` 第 48、58 行的“登录补填提供方集合现为……”保留为历史小节内容但在其后注明“已被下述统一规则取代”；在文件末尾或当前未发布版本段新增：

```markdown
### 提供方资料补填统一（ProviderProfileBackfill）

- 补填不再区分提供方。默认（`PROVIDER_PROFILE_BACKFILL=true`）下，GOOGLE、OIDC、FACEBOOK 等提供方的已有账号再次登录、绑定或补全关联，也会补填空的显示名与头像；用户主动清空的值会在下一次第三方登录时被重新填充。
- 设为 `false` 后，已有账号的第三方登录、绑定（含同账号重复绑定）与补全关联都不写账号资料；ALIPAY、QQ、WEIBO 也不再后续补填。此前所有提供方在绑定到已有账号时都会填充空资料，关闭后不再填充。
- 新建账号（第三方直接注册、补全新建账号）始终填充，不受配置影响。
- `provider_meta.profile` 记录规则不变；HTTP、数据库无变化。
```

- [ ] **Step 4: 提供方接入指南**

`docs/alipay-sign-in.md`、`docs/qq-sign-in.md`、`docs/weibo-sign-in.md` 的后续补填描述改为：“资料填充遵循统一规则：新建账号始终填充；已有账号的后续登录在 `PROVIDER_PROFILE_BACKFILL` 开启（默认）时补填空字段。”

- [ ] **Step 5: 核对**

Run: `rg -n "登录补填提供方|refillsProfileOnSignIn" README.md docs/ --glob '!docs/superpowers/**' --glob '!docs/releases/**'`
Expected: 仅剩 `docs/compatibility.md` 中已标注“已被取代”的历史描述。检查新增段落中的相对链接可打开。

- [ ] **Step 6: 提交**

```bash
git add README.md docs/
git commit -m "docs: document provider profile backfill configuration"
```

---

### Task 4: 集成验证与记录

**Files:**
- Create: `openspec/changes/configurable-provider-profile-backfill/verification.md`
- Modify: `openspec/changes/configurable-provider-profile-backfill/tasks.md`（勾选）

- [ ] **Step 1: 全量快速验证**

```bash
export GOWORK=off
go build ./... && go vet ./... && go test -count=1 ./...
openspec validate --all --strict
```

Expected: 全部通过；记录 `SERVER_TEST_DB_DSN` 是否设置、哪些包实际运行了数据库用例。

- [ ] **Step 2: 完整验证**

在 Linux 验证镜像（`tests/Dockerfile`）中运行 `bash scripts/verify.sh`，使用三个一次性数据库（集成、恢复源、恢复目标）和空 Redis DB；结束后只清理本任务创建的资源。

- [ ] **Step 3: 写 verification.md**

记录：实际运行的命令与结果、被删除的 6 个旧测试及其替代测试、required-tests 变更、未验证项（真实第三方平台联调不在本变更范围）。勾选 tasks.md 中已完成并验证的任务。

- [ ] **Step 4: 提交**

```bash
git add openspec/changes/configurable-provider-profile-backfill/
git commit -m "docs(openspec): record provider profile backfill verification"
```
