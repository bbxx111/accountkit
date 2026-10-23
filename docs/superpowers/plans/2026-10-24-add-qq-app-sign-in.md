# QQ App SDK 登录 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增内置 `QQ` 第三方提供方：服务端用 QQ 互联 `/oauth2.0/me` 校验 App SDK 的 access_token，按固定 UNIONID/OPENID 模式建立身份，经 `get_user_info` 读取昵称/头像并与支付宝一样在登录时补填空资料。

**Architecture:** 新提供方 `idp.QQ` 实现既有 `Provider` 接口，网络调用全部发生在领域事务之前；补填由 `user` 包的提供方集合判定 `refillsProfileOnSignIn` 统一控制（ALIPAY、QQ）。配置走根包 `Config` / `ConfigFromEnv(prefix)`，accountsvc 只做错误名映射与显式启用校验。

**Tech Stack:** Go 1.26（标准库 `net/http`、`net/url`、`encoding/json`）、pgx/v5、PostgreSQL 17、Redis（miniredis 单元测试 / 真实 Redis HTTP 契约测试）。

**Spec:** OpenSpec 变更 [`openspec/changes/add-qq-app-sign-in/`](../../../openspec/changes/add-qq-app-sign-in/)：`proposal.md`、`design.md`、`specs/*/spec.md`、`tasks.md`。实施者必须先读 proposal、design 与五份 delta spec；本计划不复述需求，只给落地步骤。每完成一个 OpenSpec 任务，在 `tasks.md` 中勾选对应编号。

## Global Constraints

- 构建与测试在 `GOWORK=off` 下进行；不新增第三方依赖。
- 无 SQL/schema/迁移变更；不修改 `migrations/`、`user/db/`、`audit/db/`、`tests/testdata/source-baseline/`。
- 不改动支付宝既有行为与其测试文件（`user/idp/alipay*.go` 只被调用、不被修改；`user/service_alipay_test.go` 不改）。
- QQ access_token、上游原始响应、昵称/头像原文不得出现在日志、错误文本、审计或 HTTP 响应中；所有失败错误只用 `qqFailure(哨兵, "阶段")` 构造，绝不包装 `*url.Error`。
- 错误必须使用 `user/idp` 哨兵：`ErrMalformedCredential`、`ErrAppNotAllowed`、`ErrInvalidCredential`、`ErrUnavailable`、`ErrMisconfigured`。
- 预算：`/me` 10 秒，`get_user_info` 3 秒，均受调用方更早 deadline 约束；每份响应最多 64 KiB；禁止重定向；不重试。
- AppID：1–20 位 ASCII 数字，不重复；模式默认 `UNIONID`，只接受 `UNIONID` / `OPENID`；默认接口地址 `https://graph.qq.com`，HTTP 只允许数值回环地址。
- 头像名单初始只有 `thirdqq.qlogo.cn`。
- 观测取值：方法 `qq`、依赖 `verify_qq`、降级 `qq_profile_unavailable`。
- 修改 Go 后运行 `gofmt -w`；Go/SQL/shell 文件保持 LF。
- 文档中文；示例不得含真实凭据，AppID 示例用 `1100000001` / `1100000002`。
- 提交信息使用 Conventional Commits，末尾带 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`。

## File Structure

| 文件 | 动作 | 职责 |
|---|---|---|
| `enum/enum.go`、`enum/enum_test.go` | 修改 | 内置 `IdentityQQ` |
| `user/identity_policy.go` | 修改 | `builtinKindOrder` 追加 QQ |
| `user/idp/qq_config.go` | 新建 | `QQConfig`、`QQSubjectMode`、`ValidateQQConfig`、地址规范化 |
| `user/idp/qq.go` | 新建 | `QQ` 提供方：凭证、`/me`、subject、资料、头像改写 |
| `user/idp/qq_api.go` | 新建 | 有界 GET 请求与 `/me` 错误码分类 |
| `user/idp/policy.go` | 修改 | `(*QQ).PolicyFingerprint` |
| `user/idp/idp.go` | 修改 | 包注释提及 QQ |
| `user/idp/qq_config_test.go`、`user/idp/qq_test.go` | 新建 | 提供方单元测试 |
| `observability/classification.go` | 修改 | `MethodQQ`、`DependencyVerifyQQ`、`DegradationQQProfileUnavailable` |
| `observability/internal/metricdata/project.go`、`project_test.go` | 修改 | 有界取值允许列表 |
| `user/observability.go` | 修改 | kind→方法/依赖映射 |
| `user/service_idp.go`、`user/service_signin_completion.go` | 修改 | `refillsProfileOnSignIn` 替换三处支付宝判断 |
| `user/service_qq_test.go` | 新建 | QQ 领域补填/pending/并发/回滚测试 |
| `config.go`、`config_qq_test.go` | 修改/新建 | 库配置字段、默认值、校验、环境变量 |
| `accountkit.go`、`accountkit_qq_test.go` | 修改/新建 | 提供方装配 |
| `internal/accountsvc/config.go`、`config_test.go` | 修改 | 显式启用校验与错误名映射 |
| `accountkit_qq_http_test.go` | 新建 | 正式 HTTP 契约测试 |
| `scripts/required-tests.txt` | 修改 | 追加必需测试 |
| `README.md`、`docs/qq-sign-in.md`、`docs/go-integration.md`、`docs/accountsvc.md`、`docs/compatibility.md`、`docs/metrics.md`、`deploy/accountsvc/.env.example` | 修改/新建 | 文档 |
| `openspec/changes/add-qq-app-sign-in/verification.md` | 新建 | 验收记录 |

---

### Task 1: 内置类型与 QQ 配置（OpenSpec 1.1–1.3）

**Files:**
- Modify: `enum/enum.go`（常量块与 `IsBuiltin`）、`enum/enum_test.go`
- Modify: `user/identity_policy.go:65`
- Create: `user/idp/qq_config.go`、`user/idp/qq_config_test.go`
- Modify: `config.go`（类型别名区、`Config` 字段区、`providerKinds`、新增 `qqConfig`、`applyDefaults`、`validateBase`、`ConfigFromEnv`）
- Create: `config_qq_test.go`
- Modify: `README.md` 配置表（支付宝行之后）

**Interfaces:**
- Produces: `enum.IdentityQQ enum.IdentityKind = "QQ"`
- Produces: `idp.QQSubjectMode`、`idp.QQSubjectModeUnionID`、`idp.QQSubjectModeOpenID`
- Produces: `idp.QQConfig{AppIDs []string; SubjectMode QQSubjectMode; APIBaseURL string}`
- Produces: `func idp.ValidateQQConfig(QQConfig) error`；内部 `parseQQConfig(QQConfig) (parsedQQConfig, error)`，`parsedQQConfig{apps map[string]bool; appIDs []string; subjectMode QQSubjectMode; baseURL string}`
- Produces: 根包 `QQSubjectMode` 别名与常量，`Config.QQAppIDs []string`、`Config.QQSubjectMode`、`Config.QQAPIBaseURL string`，`func (c Config) qqConfig() idp.QQConfig`
- 错误文本前缀（accountsvc 依赖）：`idp: QQAppIDs[<i>] ...`、`idp: QQSubjectMode ...`、`idp: QQAPIBaseURL ...`

- [ ] **Step 1: 写 enum 失败测试**

在 `enum/enum_test.go` 末尾追加：

```go
func TestIdentityQQIsBuiltinThirdParty(t *testing.T) {
	if enum.IdentityQQ != "QQ" || !enum.IdentityQQ.IsBuiltin() || !enum.IdentityQQ.Valid() || enum.IdentityQQ.IsAnchor() || enum.IdentityQQ.IsChannel() {
		t.Fatal("QQ must be a valid builtin third-party kind")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `GOWORK=off go test ./enum/ -run TestIdentityQQIsBuiltinThirdParty`
Expected: 编译失败 `undefined: enum.IdentityQQ`

- [ ] **Step 3: 实现 enum 与策略顺序**

`enum/enum.go` 常量块追加 `IdentityQQ IdentityKind = "QQ"`（对齐 gofmt），`IsBuiltin` 的 case 列表末尾追加 `IdentityQQ`。`user/identity_policy.go:65` 的 `builtinKindOrder` 末尾追加 `enum.IdentityQQ`。

- [ ] **Step 4: 运行通过**

Run: `GOWORK=off go test ./enum/ ./user/ -run 'TestIdentity|TestAlipayBuiltInPolicyOrder'`
Expected: PASS

- [ ] **Step 5: 写 QQ 配置失败测试**

创建 `user/idp/qq_config_test.go`：

```go
package idp

import (
	"strings"
	"testing"
)

func TestQQConfigValidation(t *testing.T) {
	ok := []QQConfig{
		{},
		{AppIDs: []string{"1100000001", "1100000002"}},
		{AppIDs: []string{"1"}, SubjectMode: QQSubjectModeOpenID},
		{AppIDs: []string{"1100000001"}, APIBaseURL: "http://127.0.0.1:8080/"},
		{AppIDs: []string{"1100000001"}, APIBaseURL: "https://proxy.example.test/qq"},
	}
	for _, c := range ok {
		if err := ValidateQQConfig(c); err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
	}
	bad := map[string]QQConfig{
		"QQAppIDs[0]":  {AppIDs: []string{""}},
		"QQAppIDs[1]":  {AppIDs: []string{"1100000001", "1100000001"}},
		"QQAppIDs[0] ": {AppIDs: []string{"app-one"}},
		"QQAppIDs[0]  ": {AppIDs: []string{strings.Repeat("1", 21)}},
		"QQSubjectMode": {SubjectMode: "unionid"},
		"QQSubjectMode ": {SubjectMode: "AUTO"},
		"QQAPIBaseURL":  {APIBaseURL: "http://graph.qq.com"},
		"QQAPIBaseURL ": {APIBaseURL: "https://u:p@graph.qq.com"},
		"QQAPIBaseURL  ": {APIBaseURL: "https://graph.qq.com?x=1"},
		"QQAPIBaseURL   ": {APIBaseURL: "https://graph.qq.com#f"},
	}
	for want, c := range bad {
		err := ValidateQQConfig(c)
		if err == nil || !strings.HasPrefix(err.Error(), "idp: "+strings.TrimSpace(want)) {
			t.Fatalf("%+v: %v", c, err)
		}
		for _, secret := range c.AppIDs {
			if secret != "" && strings.Contains(err.Error(), secret) {
				t.Fatal("error echoed configuration value")
			}
		}
	}
	parsed, err := parseQQConfig(QQConfig{AppIDs: []string{"1100000001"}, APIBaseURL: "https://proxy.example.test/qq/"})
	if err != nil || parsed.baseURL != "https://proxy.example.test/qq" || parsed.subjectMode != QQSubjectModeUnionID {
		t.Fatalf("normalized config: %+v %v", parsed, err)
	}
	if parsed, _ := parseQQConfig(QQConfig{}); parsed.baseURL != "https://graph.qq.com" {
		t.Fatal("default base URL")
	}
	apps := []string{"1100000001"}
	parsed, _ = parseQQConfig(QQConfig{AppIDs: apps})
	apps[0] = "changed"
	if !parsed.apps["1100000001"] || parsed.appIDs[0] != "1100000001" {
		t.Fatal("parsed config aliases caller slice")
	}
}
```

- [ ] **Step 6: 运行确认失败**

Run: `GOWORK=off go test ./user/idp/ -run TestQQConfigValidation`
Expected: 编译失败 `undefined: QQConfig`

- [ ] **Step 7: 实现 `user/idp/qq_config.go`**

```go
package idp

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// QQSubjectMode 决定实例内所有 QQ 应用的固定身份识别方式。
type QQSubjectMode string

const (
	QQSubjectModeUnionID QQSubjectMode = "UNIONID"
	QQSubjectModeOpenID  QQSubjectMode = "OPENID"
)

// QQConfig 配置 QQ 互联移动应用；AppIDs 为空表示未启用。App SDK 校验不需要 AppKey。
type QQConfig struct {
	AppIDs      []string
	SubjectMode QQSubjectMode // 空值默认 UNIONID。
	APIBaseURL  string        // 空值默认 https://graph.qq.com；仅回环测试地址允许 HTTP。
}

const qqDefaultAPIBaseURL = "https://graph.qq.com"

var qqAppIDPattern = regexp.MustCompile(`^[0-9]{1,20}$`)

type parsedQQConfig struct {
	apps        map[string]bool
	appIDs      []string
	subjectMode QQSubjectMode
	baseURL     string
}

// ValidateQQConfig 不做网络或文件 I/O；错误只指出配置项，不包含配置值。
func ValidateQQConfig(config QQConfig) error {
	_, err := parseQQConfig(config)
	return err
}

func parseQQConfig(config QQConfig) (parsedQQConfig, error) {
	result := parsedQQConfig{apps: make(map[string]bool, len(config.AppIDs)), subjectMode: config.SubjectMode}
	if result.subjectMode == "" {
		result.subjectMode = QQSubjectModeUnionID
	}
	if result.subjectMode != QQSubjectModeUnionID && result.subjectMode != QQSubjectModeOpenID {
		return parsedQQConfig{}, errors.New("idp: QQSubjectMode must be UNIONID or OPENID")
	}
	base, err := normalizeQQBaseURL(config.APIBaseURL)
	if err != nil {
		return parsedQQConfig{}, err
	}
	result.baseURL = base
	for i, appID := range config.AppIDs {
		if !qqAppIDPattern.MatchString(appID) {
			return parsedQQConfig{}, fmt.Errorf("idp: QQAppIDs[%d] must contain 1-20 ASCII digits", i)
		}
		if result.apps[appID] {
			return parsedQQConfig{}, fmt.Errorf("idp: QQAppIDs[%d] is duplicated", i)
		}
		result.apps[appID] = true
		result.appIDs = append(result.appIDs, appID)
	}
	return result, nil
}

// normalizeQQBaseURL 与支付宝网关规则一致，并去掉尾随斜杠以便拼接接口路径。
func normalizeQQBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return qqDefaultAPIBaseURL, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || u.Opaque != "" {
		return "", errors.New("idp: QQAPIBaseURL must be an absolute HTTPS URL without userinfo, query or fragment")
	}
	loopback := false
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", errors.New("idp: QQAPIBaseURL requires HTTPS except for loopback test addresses")
	}
	return strings.TrimRight(u.String(), "/"), nil
}
```

- [ ] **Step 8: 运行通过**

Run: `GOWORK=off go test ./user/idp/ -run TestQQConfigValidation`
Expected: PASS

- [ ] **Step 9: 写根包配置失败测试**

创建 `config_qq_test.go`：

```go
package accountkit

import (
	"encoding/base64"
	"slices"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/enum"
)

func TestQQConfigFromEnv(t *testing.T) {
	const prefix = "CUSTOM_QQ_TEST_"
	for _, name := range []string{"JWT_KEYS", "SUBJECT_HMAC_KEYS", "SUBJECT_CIPHER_KEYS"} {
		t.Setenv(prefix+name, "1:"+base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	}
	for _, name := range []string{"JWT_ACTIVE_KEY", "SUBJECT_HMAC_ACTIVE_KEY", "SUBJECT_CIPHER_ACTIVE_KEY"} {
		t.Setenv(prefix+name, "1")
	}
	t.Setenv(prefix+"JWT_ISSUER", "issuer")
	t.Setenv(prefix+"JWT_AUDIENCE", "audience")
	t.Setenv("ACCOUNTKIT_QQ_APP_IDS", "invalid-ignored")
	c, err := ConfigFromEnv(prefix)
	if err != nil || len(c.QQAppIDs) != 0 || slices.Contains(c.providerKinds(), enum.IdentityQQ) {
		t.Fatalf("unset QQ must stay disabled: %v", err)
	}
	t.Setenv(prefix+"QQ_APP_IDS", "1100000001, 1100000002")
	c, err = ConfigFromEnv(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(c.QQAppIDs, []string{"1100000001", "1100000002"}) || c.QQSubjectMode != QQSubjectModeUnionID || c.QQAPIBaseURL != "https://graph.qq.com" {
		t.Fatalf("QQ env defaults: %+v", c.QQAppIDs)
	}
	if !slices.Contains(c.providerKinds(), enum.IdentityQQ) {
		t.Fatal("configured QQ absent from provider kinds")
	}
	snapshot := c.qqConfig()
	c.QQAppIDs[0] = "changed"
	if snapshot.AppIDs[0] != "1100000001" {
		t.Fatal("QQ app slice aliases caller configuration")
	}
	for _, mode := range []string{"", " ", "unionid", "AUTO"} {
		t.Run("mode_"+mode, func(t *testing.T) {
			t.Setenv(prefix+"QQ_SUBJECT_MODE", mode)
			if _, err := ConfigFromEnv(prefix); err == nil || !strings.Contains(err.Error(), prefix+"QQ_SUBJECT_MODE") {
				t.Fatalf("invalid explicit mode: %v", err)
			}
		})
	}
	t.Setenv(prefix+"QQ_SUBJECT_MODE", "OPENID")
	t.Setenv(prefix+"QQ_API_BASE_URL", "http://127.0.0.1:123")
	if c, err := ConfigFromEnv(prefix); err != nil || c.QQSubjectMode != QQSubjectModeOpenID || c.QQAPIBaseURL != "http://127.0.0.1:123" {
		t.Fatalf("OPENID env: %v", err)
	}
	t.Setenv(prefix+"QQ_API_BASE_URL", "http://graph.qq.com")
	if _, err := ConfigFromEnv(prefix); err == nil || !strings.Contains(err.Error(), "QQAPIBaseURL") {
		t.Fatalf("insecure base URL accepted: %v", err)
	}
	t.Setenv(prefix+"QQ_API_BASE_URL", "")
	for _, list := range []string{"1100000001,1100000001", "1100000001,", "app-one"} {
		t.Run("apps_"+list, func(t *testing.T) {
			t.Setenv(prefix+"QQ_APP_IDS", list)
			if _, err := ConfigFromEnv(prefix); err == nil || !strings.Contains(err.Error(), prefix+"QQ_APP_IDS") || strings.Contains(err.Error(), "app-one") {
				t.Fatalf("invalid app list accepted or echoed: %v", err)
			}
		})
	}
	t.Setenv(prefix+"QQ_APP_IDS", "1100000001")
	t.Setenv(prefix+"OIDC_PROVIDERS", "QQ")
	if _, err := ConfigFromEnv(prefix); err == nil || !strings.Contains(err.Error(), "OIDC_PROVIDERS") {
		t.Fatalf("OIDC may not occupy QQ: %v", err)
	}
	if field, err := validateOIDCProvider(OIDCProvider{Kind: enum.IdentityQQ}); err == nil || field != "Kind" {
		t.Fatal("Go OIDC configuration may not occupy QQ")
	}
	if err := (Config{QQAppIDs: []string{"x"}}).validateBase(); err == nil || !strings.HasPrefix(err.Error(), "accountkit: idp: QQAppIDs[0]") {
		t.Fatalf("Validate must report QQ field: %v", err)
	}
}
```

- [ ] **Step 10: 运行确认失败**

Run: `GOWORK=off go test . -run TestQQConfigFromEnv`
Expected: 编译失败 `c.QQAppIDs undefined`

- [ ] **Step 11: 实现根包配置**

在 `config.go`：

1. 支付宝别名块之后：

```go
// QQSubjectMode 决定实例内所有 QQ 应用的固定身份识别方式。
type QQSubjectMode = idp.QQSubjectMode

const (
	QQSubjectModeUnionID = idp.QQSubjectModeUnionID
	QQSubjectModeOpenID  = idp.QQSubjectModeOpenID
)
```

2. `Config` 中 `AlipayGatewayURL` 字段之后：

```go
	// QQAppIDs 为空表示未启用 QQ 登录；App SDK 校验 access_token 不需要 AppKey。
	QQAppIDs      []string
	QQSubjectMode QQSubjectMode // 默认 UNIONID，所有应用固定使用同一模式。
	QQAPIBaseURL  string        // 默认 https://graph.qq.com；HTTP 仅允许回环测试地址。
```

3. `alipayAppIDPattern` 之后：`var qqAppIDPattern = regexp.MustCompile(`^[0-9]{1,20}$`)`

4. `providerKinds` 中支付宝分支之后：

```go
	if len(c.QQAppIDs) > 0 {
		kinds = append(kinds, enum.IdentityQQ)
	}
```

5. `alipayConfig` 之后：

```go
func (c Config) qqConfig() idp.QQConfig {
	return idp.QQConfig{AppIDs: slices.Clone(c.QQAppIDs), SubjectMode: c.QQSubjectMode, APIBaseURL: c.QQAPIBaseURL}
}
```

6. `applyDefaults` 中支付宝默认值之后：

```go
	c.QQAppIDs = slices.Clone(c.QQAppIDs)
	if c.QQSubjectMode == "" {
		c.QQSubjectMode = QQSubjectModeUnionID
	}
	if c.QQAPIBaseURL == "" {
		c.QQAPIBaseURL = "https://graph.qq.com"
	}
```

7. `validateBase` 中 `ValidateAlipayConfig` 之后：

```go
	if err := idp.ValidateQQConfig(c.qqConfig()); err != nil {
		return fmt.Errorf("accountkit: %w", err)
	}
```

8. `ConfigFromEnv` 中支付宝 `ALIPAY_APP_IDS` 块之后：

```go
	if raw, ok := os.LookupEnv(prefix + "QQ_SUBJECT_MODE"); ok {
		c.QQSubjectMode = QQSubjectMode(raw)
		if c.QQSubjectMode != QQSubjectModeUnionID && c.QQSubjectMode != QQSubjectModeOpenID {
			fail("QQ_SUBJECT_MODE", errors.New("must be UNIONID or OPENID"))
		}
	}
	c.QQAPIBaseURL = str("QQ_API_BASE_URL")
	if raw := str("QQ_APP_IDS"); raw != "" {
		seen := map[string]bool{}
		for _, item := range strings.Split(raw, ",") {
			appID := strings.TrimSpace(item)
			if !qqAppIDPattern.MatchString(appID) || seen[appID] {
				fail("QQ_APP_IDS", errors.New("entries must be unique AppIDs of 1-20 ASCII digits"))
				break
			}
			seen[appID] = true
			c.QQAppIDs = append(c.QQAppIDs, appID)
		}
	}
```

- [ ] **Step 12: 运行通过并回归**

Run: `GOWORK=off go test . ./enum/ ./user/idp/ -run 'QQ|Alipay|OIDC|Config'`
Expected: PASS

- [ ] **Step 13: README 配置表**

在 `README.md` 配置表 `ALIPAY_GATEWAY_URL` 行之后追加：

```markdown
| `QQ_APP_IDS` | 空（未启用） | 逗号分隔的不同 QQ 互联移动应用 AppID，每项 1–20 位 ASCII 数字；Go 配置为 `QQAppIDs []string`；App SDK 校验不需要 AppKey |
| `QQ_SUBJECT_MODE` | `UNIONID` | Go 字段 `QQSubjectMode`，全局固定 `UNIONID` / `OPENID`；显式空值、空白、小写或其他值启动失败；UNIONID 需在 QQ 互联开通 UnionID |
| `QQ_API_BASE_URL` | `https://graph.qq.com` | Go 字段 `QQAPIBaseURL`；HTTPS 绝对地址，无用户信息、查询或 fragment；HTTP 仅限数值回环测试地址 |
```

- [ ] **Step 14: gofmt、vet、提交**

```bash
gofmt -w enum user/idp config.go config_qq_test.go user/identity_policy.go
GOWORK=off go vet ./enum/ ./user/... .
git add enum user/identity_policy.go user/idp/qq_config.go user/idp/qq_config_test.go config.go config_qq_test.go README.md
git commit -m "feat(config): add qq app configuration"
```

勾选 OpenSpec `tasks.md` 的 1.1、1.2、1.3。

---

### Task 2: 令牌校验与固定身份（OpenSpec 2.1–2.4）

**Files:**
- Create: `user/idp/qq.go`、`user/idp/qq_api.go`、`user/idp/qq_test.go`
- Modify: `user/idp/policy.go`（文件末尾追加）
- Modify: `observability/classification.go`（追加 `DegradationQQProfileUnavailable`，Task 3 使用；Task 5 追加方法/依赖值）

**Interfaces:**
- Consumes: Task 1 的 `parseQQConfig`、`parsedQQConfig`、`QQSubjectMode*`、`enum.IdentityQQ`
- Consumes（不修改）: `alipayObject(raw []byte) (map[string]json.RawMessage, error)`、`alipayString(map[string]json.RawMessage, string) (string, error)`、`validAlipayIdentifier(string) bool`（位于 `alipay_json.go` / `alipay.go`）
- Produces: `idp.QQOptions{Config QQConfig; HTTP *http.Client; Logger *slog.Logger; Observer observability.Observer}`
- Produces: `func idp.NewQQ(QQOptions) (*QQ, error)`；`(*QQ).Kind()`、`(*QQ).Verify(ctx, json.RawMessage) (Identity, error)`、`(*QQ).PolicyFingerprint() string`
- Produces: 内部 `(*QQ).get(ctx, path string, query url.Values, budget time.Duration) (map[string]json.RawMessage, error)`、`qqFailure(kind error, stage string) error`、`qqMeError(code int64) error`、`(*QQ).profile(ctx, appID, token, openID string) (name, picture string, err error)`（Task 2 先返回空值，Task 3 实现）

- [ ] **Step 1: 写失败测试（凭证、/me、错误、subject、指纹）**

创建 `user/idp/qq_test.go`：

```go
package idp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
)

const qqTestApp, qqTestApp2 = "1100000001", "1100000002"

// qqServer answers /oauth2.0/me with me(r) and /user/get_user_info with profile(r).
func qqServer(t *testing.T, me, profile func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet {
			t.Error("QQ requests must be GET")
		}
		switch r.URL.Path {
		case "/oauth2.0/me":
			me(w, r)
		case "/user/get_user_info":
			if profile == nil {
				fmt.Fprint(w, `{"ret":0,"nickname":"","figureurl_qq_1":""}`)
				return
			}
			profile(w, r)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func newTestQQ(t *testing.T, srv *httptest.Server, mode QQSubjectMode, logs *bytes.Buffer) *QQ {
	t.Helper()
	var logger *slog.Logger
	if logs != nil {
		logger = slog.New(slog.NewTextHandler(logs, nil))
	}
	q, err := NewQQ(QQOptions{Config: QQConfig{AppIDs: []string{qqTestApp, qqTestApp2}, SubjectMode: mode, APIBaseURL: srv.URL}, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func qqCredential(app, token string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"app_id":%q,"access_token":%q}`, app, token))
}

func TestQQCredentialRejectedBeforeNetwork(t *testing.T) {
	srv, calls := qqServer(t, func(w http.ResponseWriter, r *http.Request) { t.Error("network called") }, nil)
	q := newTestQQ(t, srv, QQSubjectModeUnionID, nil)
	for raw, want := range map[string]error{
		`{"app_id":"1100000001"}`:                                   ErrMalformedCredential,
		`{"app_id":"1100000001","access_token":""}`:                 ErrMalformedCredential,
		`{"app_id":"1100000001","access_token":"t","openid":"o"}`:   ErrMalformedCredential,
		`{"app_id":"1100000001","access_token":"t","unionid":"u"}`:  ErrMalformedCredential,
		`["1100000001","t"]`:                                        ErrMalformedCredential,
		`{"app_id":"1100000001","access_token":"t"} {}`:             ErrMalformedCredential,
		`{"app_id":1100000001,"access_token":"t"}`:                  ErrMalformedCredential,
		`{"app_id":"1100000009","access_token":"t"}`:                ErrAppNotAllowed,
	} {
		if _, err := q.Verify(context.Background(), json.RawMessage(raw)); !errors.Is(err, want) {
			t.Fatalf("%s: %v, want %v", raw, err, want)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("rejected credential reached QQ")
	}
	if q.Kind() != enum.IdentityQQ {
		t.Fatal("kind")
	}
}

func TestQQVerifyModes(t *testing.T) {
	for _, mode := range []QQSubjectMode{QQSubjectModeUnionID, QQSubjectModeOpenID} {
		t.Run(string(mode), func(t *testing.T) {
			srv, _ := qqServer(t, func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if q.Get("access_token") != "synthetic-token" || q.Get("fmt") != "json" {
					t.Error("me query")
				}
				if (q.Get("unionid") == "1") != (mode == QQSubjectModeUnionID) {
					t.Error("unionid parameter must only be sent in UNIONID mode")
				}
				body := `{"client_id":"1100000001","openid":"OPEN-ONE"}`
				if mode == QQSubjectModeUnionID {
					body = `{"client_id":"1100000001","openid":"OPEN-ONE","unionid":"UID_ONE"}`
				}
				fmt.Fprint(w, body)
			}, nil)
			id, err := newTestQQ(t, srv, mode, nil).Verify(context.Background(), qqCredential(qqTestApp, "synthetic-token"))
			if err != nil {
				t.Fatal(err)
			}
			want := "unionid:UID_ONE"
			if mode == QQSubjectModeOpenID {
				want = "openid:1100000001:OPEN-ONE"
			}
			if id.Kind != enum.IdentityQQ || id.Subject != want || id.OpenIDs[qqTestApp] != "OPEN-ONE" || len(id.OpenIDs) != 1 {
				t.Fatalf("identity=%+v", id)
			}
			if mode == QQSubjectModeUnionID && id.UnionID != "UID_ONE" {
				t.Fatal("unionid metadata")
			}
		})
	}
}

func TestQQSharedUnionIDAcrossApps(t *testing.T) {
	srv, _ := qqServer(t, func(w http.ResponseWriter, r *http.Request) {
		app := strings.TrimPrefix(r.URL.Query().Get("access_token"), "token-")
		fmt.Fprintf(w, `{"client_id":%q,"openid":"open-%s","unionid":"UID_SHARED"}`, app, app)
	}, nil)
	q := newTestQQ(t, srv, QQSubjectModeUnionID, nil)
	a, errA := q.Verify(context.Background(), qqCredential(qqTestApp, "token-"+qqTestApp))
	b, errB := q.Verify(context.Background(), qqCredential(qqTestApp2, "token-"+qqTestApp2))
	if errA != nil || errB != nil || a.Subject != b.Subject || a.OpenIDs[qqTestApp] == b.OpenIDs[qqTestApp2] {
		t.Fatalf("shared union: %+v %+v %v %v", a, b, errA, errB)
	}
	q = newTestQQ(t, srv, QQSubjectModeOpenID, nil)
	a, _ = q.Verify(context.Background(), qqCredential(qqTestApp, "token-"+qqTestApp))
	b, _ = q.Verify(context.Background(), qqCredential(qqTestApp2, "token-"+qqTestApp2))
	if a.Subject == b.Subject {
		t.Fatal("OPENID mode must isolate apps")
	}
}

func TestQQMeClassification(t *testing.T) {
	cases := []struct {
		name   string
		mode   QQSubjectMode
		status int
		body   string
		want   error
	}{
		{"token check failed", QQSubjectModeUnionID, 200, `{"error":100016,"error_description":"access token check failed"}`, ErrInvalidCredential},
		{"token illegal", QQSubjectModeUnionID, 200, `{"error":100013,"error_description":"x"}`, ErrInvalidCredential},
		{"token expired", QQSubjectModeUnionID, 200, `{"error":100014,"error_description":"x"}`, ErrInvalidCredential},
		{"token revoked", QQSubjectModeUnionID, 200, `{"error":100015,"error_description":"x"}`, ErrInvalidCredential},
		{"unionid not enabled", QQSubjectModeUnionID, 200, `{"error":100048,"error_description":"companyid not set"}`, ErrMisconfigured},
		{"unknown code", QQSubjectModeUnionID, 200, `{"error":100099,"error_description":"x"}`, ErrUnavailable},
		{"non numeric code", QQSubjectModeUnionID, 200, `{"error":"x"}`, ErrUnavailable},
		{"other app", QQSubjectModeUnionID, 200, `{"client_id":"1100000002","openid":"o","unionid":"u"}`, ErrAppNotAllowed},
		{"missing client", QQSubjectModeUnionID, 200, `{"openid":"o","unionid":"u"}`, ErrMisconfigured},
		{"missing unionid", QQSubjectModeUnionID, 200, `{"client_id":"1100000001","openid":"o"}`, ErrMisconfigured},
		{"missing openid", QQSubjectModeOpenID, 200, `{"client_id":"1100000001"}`, ErrMisconfigured},
		{"bad openid", QQSubjectModeOpenID, 200, `{"client_id":"1100000001","openid":"a b"}`, ErrMisconfigured},
		{"wrong field type", QQSubjectModeOpenID, 200, `{"client_id":1100000001,"openid":"o"}`, ErrMisconfigured},
		{"jsonp", QQSubjectModeUnionID, 200, `callback( {"client_id":"1100000001","openid":"o","unionid":"u"} );`, ErrUnavailable},
		{"duplicate key", QQSubjectModeOpenID, 200, `{"client_id":"1100000001","openid":"o","openid":"p"}`, ErrUnavailable},
		{"trailing", QQSubjectModeOpenID, 200, `{"client_id":"1100000001","openid":"o"}{}`, ErrUnavailable},
		{"redirect", QQSubjectModeOpenID, 302, ``, ErrUnavailable},
		{"rate limited", QQSubjectModeOpenID, 429, ``, ErrUnavailable},
		{"server error", QQSubjectModeOpenID, 503, ``, ErrUnavailable},
		{"not found", QQSubjectModeOpenID, 404, ``, ErrUnavailable},
		{"too large", QQSubjectModeOpenID, 200, `{"client_id":"1100000001","openid":"o","pad":"` + strings.Repeat("x", 70<<10) + `"}`, ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			srv, calls := qqServer(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.status == 302 {
					w.Header().Set("Location", "https://elsewhere.example.test/")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}, nil)
			_, err := newTestQQ(t, srv, tc.mode, &logs).Verify(context.Background(), qqCredential(qqTestApp, "secret-token-value"))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
			if calls.Load() != 1 {
				t.Fatalf("failed /me must not query profile or retry: calls=%d", calls.Load())
			}
			if strings.Contains(err.Error(), "secret-token-value") || strings.Contains(logs.String(), "secret-token-value") || strings.Contains(err.Error(), "access token check failed") {
				t.Fatal("token or upstream text leaked")
			}
		})
	}
}

func TestQQTransportFailures(t *testing.T) {
	block := make(chan struct{})
	srv, _ := qqServer(t, func(w http.ResponseWriter, r *http.Request) { <-block }, nil)
	t.Cleanup(func() { close(block) })
	q := newTestQQ(t, srv, QQSubjectModeOpenID, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := q.Verify(ctx, qqCredential(qqTestApp, "secret-token-value")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("caller deadline: %v", err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := q.Verify(canceled, qqCredential(qqTestApp, "t")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	base := closed.URL
	closed.Close()
	q, _ = NewQQ(QQOptions{Config: QQConfig{AppIDs: []string{qqTestApp}, APIBaseURL: base}})
	_, err := q.Verify(context.Background(), qqCredential(qqTestApp, "secret-token-value"))
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "secret-token-value") || strings.Contains(err.Error(), base) {
		t.Fatalf("transport error: %v", err)
	}
}

func TestQQPolicyFingerprint(t *testing.T) {
	fp := func(c QQConfig) string {
		q, err := NewQQ(QQOptions{Config: c})
		if err != nil {
			t.Fatal(err)
		}
		return q.PolicyFingerprint()
	}
	base := fp(QQConfig{AppIDs: []string{qqTestApp, qqTestApp2}})
	if fp(QQConfig{AppIDs: []string{qqTestApp2, qqTestApp}}) != base {
		t.Fatal("app order changed fingerprint")
	}
	for _, c := range []QQConfig{
		{AppIDs: []string{qqTestApp}},
		{AppIDs: []string{qqTestApp, qqTestApp2}, SubjectMode: QQSubjectModeOpenID},
		{AppIDs: []string{qqTestApp, qqTestApp2}, APIBaseURL: "https://proxy.example.test"},
	} {
		if fp(c) == base {
			t.Fatalf("fingerprint ignored %+v", c)
		}
	}
	if _, err := NewQQ(QQOptions{Config: QQConfig{AppIDs: []string{"bad"}}}); !errors.Is(err, ErrMisconfigured) {
		t.Fatal("invalid config accepted")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `GOWORK=off go test ./user/idp/ -run 'TestQQ'`
Expected: 编译失败 `undefined: NewQQ`

- [ ] **Step 3: 实现 `user/idp/qq_api.go`**

```go
package idp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const maxQQBody = 64 << 10

func qqFailure(kind error, stage string) error { return fmt.Errorf("%w: qq %s", kind, stage) }

// get 只返回固定阶段错误：请求 URL 含 access_token，任何传输错误都不得被包装或记录。
func (q *QQ) get(ctx context.Context, path string, query url.Values, budget time.Duration) (map[string]json.RawMessage, error) {
	child, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	req, err := http.NewRequestWithContext(child, http.MethodGet, q.config.baseURL+path+"?"+query.Encode(), nil)
	if err != nil {
		return nil, qqFailure(ErrMisconfigured, "request construction")
	}
	resp, err := q.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, qqFailure(ErrUnavailable, "transport")
	}
	defer resp.Body.Close()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if resp.StatusCode != http.StatusOK {
		return nil, qqFailure(ErrUnavailable, "http status")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxQQBody+1))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || len(body) > maxQQBody {
		return nil, qqFailure(ErrUnavailable, "response read")
	}
	// alipayObject 是包内通用的严格 JSON 对象解析：拒绝重复键、尾随内容与非对象。
	object, err := alipayObject(body)
	if err != nil {
		return nil, qqFailure(ErrUnavailable, "response structure")
	}
	return object, nil
}

// qqMeError 仅按已核对的公共返回码分类；error_description 不参与判断也不输出。
func qqMeError(code int64) error {
	switch code {
	case 100013, 100014, 100015, 100016: // access token 非法 / 过期 / 已废除 / 校验失败
		return qqFailure(ErrInvalidCredential, "access token rejected")
	case 100048: // 未开通 UnionID 打通权限
		return qqFailure(ErrMisconfigured, "unionid not enabled")
	}
	return qqFailure(ErrUnavailable, "upstream error")
}
```

实施前打开 QQ 互联“公共返回码说明”页面核对 100013–100016 的含义；若与注释不符，按官方含义调整映射与测试表，并在 design.md 的 Decision 4 中记录。

- [ ] **Step 4: 实现 `user/idp/qq.go`**

```go
package idp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/observability"
)

// QQOptions 注入 QQ 互联配置和可选宿主依赖；构造不进行 I/O。
type QQOptions struct {
	Config   QQConfig
	HTTP     *http.Client
	Logger   *slog.Logger
	Observer observability.Observer
}

// QQ 校验 QQ 互联移动 SDK 的 access_token。令牌不做一次性限制，每次请求都向 QQ 校验。
type QQ struct {
	config   parsedQQConfig
	http     *http.Client
	logger   *slog.Logger
	observer *observability.Recorder
}

// NewQQ 校验并复制配置；浅拷贝宿主 HTTP 客户端并禁用重定向。
func NewQQ(options QQOptions) (*QQ, error) {
	config, err := parseQQConfig(options.Config)
	if err != nil {
		return nil, qqFailure(ErrMisconfigured, "configuration")
	}
	client := options.HTTP
	if client == nil {
		client = &http.Client{}
	}
	isolated := *client
	isolated.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &QQ{config: config, http: &isolated, logger: logger, observer: observability.NewRecorder(options.Observer, logger)}, nil
}

type qqCredential struct {
	AppID       string `json:"app_id"`
	AccessToken string `json:"access_token"`
}

func (*QQ) Kind() enum.IdentityKind { return enum.IdentityQQ }

// Verify 实现 Provider：credential 为 {"app_id","access_token"}。
func (q *QQ) Verify(ctx context.Context, credential json.RawMessage) (Identity, error) {
	var c qqCredential
	if err := decodeCredential(credential, &c); err != nil {
		return Identity{}, qqFailure(ErrMalformedCredential, "credential structure")
	}
	if c.AppID == "" || c.AccessToken == "" {
		return Identity{}, qqFailure(ErrMalformedCredential, "credential fields")
	}
	if !q.config.apps[c.AppID] {
		return Identity{}, qqFailure(ErrAppNotAllowed, "application not allowed")
	}
	if ctx.Err() != nil {
		return Identity{}, ctx.Err()
	}
	query := url.Values{"access_token": {c.AccessToken}, "fmt": {"json"}}
	if q.config.subjectMode == QQSubjectModeUnionID {
		// 未开通 UnionID 时上游整次返回 100048，OPENID 部署不能因此失败。
		query.Set("unionid", "1")
	}
	me, err := q.get(ctx, "/oauth2.0/me", query, 10*time.Second)
	if err != nil {
		return Identity{}, err
	}
	if raw, failed := me["error"]; failed {
		code, err := strconv.ParseInt(string(raw), 10, 64)
		if err != nil {
			return Identity{}, qqFailure(ErrUnavailable, "error structure")
		}
		return Identity{}, qqMeError(code)
	}
	client, clientErr := alipayString(me, "client_id")
	open, openErr := alipayString(me, "openid")
	union, unionErr := alipayString(me, "unionid")
	if clientErr != nil || openErr != nil || unionErr != nil || client == "" || !validAlipayIdentifier(open) || union != "" && !validAlipayIdentifier(union) {
		return Identity{}, qqFailure(ErrMisconfigured, "identity fields")
	}
	if client != c.AppID {
		return Identity{}, qqFailure(ErrAppNotAllowed, "token issued for another application")
	}
	subject := "openid:" + c.AppID + ":" + open
	if q.config.subjectMode == QQSubjectModeUnionID {
		if union == "" {
			return Identity{}, qqFailure(ErrMisconfigured, "missing unionid")
		}
		subject = "unionid:" + union
	}
	id := Identity{Kind: enum.IdentityQQ, Subject: subject, UnionID: union, OpenIDs: map[string]string{c.AppID: open}}
	name, picture, profileErr := q.profile(ctx, c.AppID, c.AccessToken, open)
	if ctx.Err() != nil {
		return Identity{}, ctx.Err()
	}
	if profileErr != nil {
		q.logger.WarnContext(ctx, "qq profile unavailable", "stage", "profile")
		q.observer.Observe(ctx, observability.DegradationObservation{Scenario: observability.DegradationQQProfileUnavailable, Count: 1})
	} else {
		id.Name, id.Picture = name, picture
	}
	return id, nil
}

// profile 在 Task 3 实现；此处保持无资料、无网络调用。
func (q *QQ) profile(ctx context.Context, appID, token, openID string) (string, string, error) {
	return "", "", nil
}
```

`/me` 的 `openid` 在两种模式下都必需（资料查询与 OpenID 元数据都依赖它），`validAlipayIdentifier("")` 为 false，因此缺失 openid 归为 `ErrMisconfigured`。

在 `observability/classification.go` 的降级常量块末尾追加：

```go
	DegradationQQProfileUnavailable      DegradationScenario = "qq_profile_unavailable"
```

- [ ] **Step 5: 实现指纹**

`user/idp/policy.go` 末尾追加：

```go
// PolicyFingerprint 包含应用集合、模式与接口地址；QQ 校验不使用秘密。
func (q *QQ) PolicyFingerprint() string {
	return configurationFingerprint("qq:"+string(q.config.subjectMode), q.config.baseURL, append([]string(nil), q.config.appIDs...))
}
```

（`configurationFingerprint` 会就地排序传入切片，因此必须传副本。）

- [ ] **Step 6: 运行通过**

Run: `GOWORK=off go test ./user/idp/ -run 'TestQQ|TestAlipay' -count=1`
Expected: PASS（`qqServer` 在 profile 为 nil 时也不会被请求，因为 Task 2 的 `profile` 不发请求；`TestQQMeClassification` 的 `calls==1` 断言成立）

- [ ] **Step 7: gofmt、vet、提交**

```bash
gofmt -w user/idp observability
GOWORK=off go vet ./user/idp/ ./observability/
git add user/idp/qq.go user/idp/qq_api.go user/idp/qq_test.go user/idp/policy.go observability/classification.go
git commit -m "feat(idp): verify qq access tokens"
```

勾选 OpenSpec 2.1–2.4。

---

### Task 3: 资料查询、头像改写与降级（OpenSpec 3.1–3.4）

**Files:**
- Modify: `user/idp/qq.go`（替换 `profile` 桩，追加 `qqAvatarURL`、`qqAvatarHosts`）
- Modify: `user/idp/qq_test.go`（追加测试）
- Modify: `user/idp/idp.go:1`（包注释）

**Interfaces:**
- Consumes: Task 2 的 `(*QQ).get`、`qqFailure`、`DegradationQQProfileUnavailable`
- Produces: `func qqAvatarURL(raw string) string`；`var qqAvatarHosts map[string]bool`

- [ ] **Step 1: 写失败测试**

在 `user/idp/qq_test.go` 追加：

```go
func qqMeOK(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, `{"client_id":"1100000001","openid":"OPEN-ONE","unionid":"UID_ONE"}`)
}

func TestQQProfileMapping(t *testing.T) {
	cases := []struct {
		name, body, wantName, wantPicture string
	}{
		{"large avatar", `{"ret":0,"nickname":"QQ Nick","figureurl_qq_1":"https://thirdqq.qlogo.cn/40","figureurl_qq_2":"https://thirdqq.qlogo.cn/100","figureurl_2":"https://qzapp.qlogo.cn/zone"}`, "QQ Nick", "https://thirdqq.qlogo.cn/100"},
		{"small fallback", `{"ret":0,"nickname":"QQ Nick","figureurl_qq_1":"https://thirdqq.qlogo.cn/40"}`, "QQ Nick", "https://thirdqq.qlogo.cn/40"},
		{"empty large falls back", `{"ret":0,"nickname":"N","figureurl_qq_1":"https://thirdqq.qlogo.cn/40","figureurl_qq_2":""}`, "N", "https://thirdqq.qlogo.cn/40"},
		{"listed http upgraded", `{"ret":0,"nickname":"N","figureurl_qq_2":"http://thirdqq.qlogo.cn/g?b=oidb&k=1&s=100"}`, "N", "https://thirdqq.qlogo.cn/g?b=oidb&k=1&s=100"},
		{"listed host case", `{"ret":0,"nickname":"N","figureurl_qq_2":"http://ThirdQQ.QLogo.cn/a"}`, "N", "https://ThirdQQ.QLogo.cn/a"},
		{"unlisted http dropped", `{"ret":0,"nickname":"N","figureurl_qq_2":"http://example.com/a.png"}`, "N", ""},
		{"listed host with port dropped", `{"ret":0,"nickname":"N","figureurl_qq_2":"http://thirdqq.qlogo.cn:8080/a"}`, "N", ""},
		{"userinfo dropped", `{"ret":0,"nickname":"N","figureurl_qq_2":"http://u@thirdqq.qlogo.cn/a"}`, "N", ""},
		{"missing fields", `{"ret":0}`, "", ""},
		{"unknown fields ignored", `{"ret":0,"msg":"","is_lost":0,"gender":"男","province":"P","nickname":"N"}`, "N", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := qqServer(t, qqMeOK, func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if q.Get("access_token") != "synthetic-token" || q.Get("oauth_consumer_key") != qqTestApp || q.Get("openid") != "OPEN-ONE" {
					t.Error("profile query")
				}
				fmt.Fprint(w, tc.body)
			})
			id, err := newTestQQ(t, srv, QQSubjectModeUnionID, nil).Verify(context.Background(), qqCredential(qqTestApp, "synthetic-token"))
			if err != nil || id.Name != tc.wantName || id.Picture != tc.wantPicture || calls.Load() != 2 {
				t.Fatalf("id=%+v err=%v calls=%d", id, err, calls.Load())
			}
		})
	}
}

func TestQQProfileFailuresAllowSignIn(t *testing.T) {
	for name, handler := range map[string]func(http.ResponseWriter, *http.Request){
		"ret nonzero":  func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"ret":1002,"msg":"请先登录","nickname":"Leaked"}`) },
		"missing ret":  func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"nickname":"Leaked"}`) },
		"is_lost":      func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"ret":0,"is_lost":1,"nickname":"Leaked"}`) },
		"bad type":     func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"ret":0,"nickname":7}`) },
		"server error": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) },
		"not json":     func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `<html>`) },
		"timeout":      func(w http.ResponseWriter, r *http.Request) { time.Sleep(3500 * time.Millisecond) },
	} {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			rec := &qqTestObserver{}
			srv, _ := qqServer(t, qqMeOK, handler)
			q, err := NewQQ(QQOptions{Config: QQConfig{AppIDs: []string{qqTestApp}, APIBaseURL: srv.URL}, Logger: slog.New(slog.NewTextHandler(&logs, nil)), Observer: rec})
			if err != nil {
				t.Fatal(err)
			}
			id, err := q.Verify(context.Background(), qqCredential(qqTestApp, "secret-token-value"))
			if err != nil || id.Subject != "unionid:UID_ONE" || id.Name != "" || id.Picture != "" {
				t.Fatalf("profile failure must allow sign-in without profile: %+v %v", id, err)
			}
			if rec.degradations.Load() != 1 {
				t.Fatal("profile failure not observed")
			}
			for _, leaked := range []string{"secret-token-value", "Leaked", "请先登录"} {
				if strings.Contains(logs.String(), leaked) {
					t.Fatalf("log leaked %q", leaked)
				}
			}
		})
	}
	t.Run("missing fields and dropped avatar are not degradation", func(t *testing.T) {
		rec := &qqTestObserver{}
		srv, _ := qqServer(t, qqMeOK, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"ret":0,"figureurl_qq_2":"http://example.com/a.png"}`)
		})
		q, _ := NewQQ(QQOptions{Config: QQConfig{AppIDs: []string{qqTestApp}, APIBaseURL: srv.URL}, Observer: rec})
		if _, err := q.Verify(context.Background(), qqCredential(qqTestApp, "t")); err != nil || rec.degradations.Load() != 0 {
			t.Fatalf("err=%v degradations=%d", err, rec.degradations.Load())
		}
	})
	t.Run("caller cancel during profile", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		srv, _ := qqServer(t, qqMeOK, func(w http.ResponseWriter, r *http.Request) {
			cancel()
			<-r.Context().Done()
		})
		rec := &qqTestObserver{}
		q, _ := NewQQ(QQOptions{Config: QQConfig{AppIDs: []string{qqTestApp}, APIBaseURL: srv.URL}, Observer: rec})
		if _, err := q.Verify(ctx, qqCredential(qqTestApp, "t")); !errors.Is(err, context.Canceled) || rec.degradations.Load() != 0 {
			t.Fatalf("cancel must abort verification: %v", err)
		}
	})
}

type qqTestObserver struct{ degradations atomic.Int32 }

func (o *qqTestObserver) Observe(_ context.Context, v observability.Observation) {
	if d, ok := v.(observability.DegradationObservation); ok && d.Scenario == observability.DegradationQQProfileUnavailable {
		o.degradations.Add(int32(d.Count))
	}
}

func TestQQAvatarURL(t *testing.T) {
	for raw, want := range map[string]string{
		"":                                    "",
		"https://any.example.test/a.png":      "https://any.example.test/a.png",
		"http://thirdqq.qlogo.cn/a":           "https://thirdqq.qlogo.cn/a",
		"HTTP://thirdqq.qlogo.cn/a":           "https://thirdqq.qlogo.cn/a",
		"http://qzapp.qlogo.cn/a":             "",
		"http://thirdqq.qlogo.cn.evil.test/a": "",
		"http://thirdqq.qlogo.cn:80/a":        "",
		"ftp://thirdqq.qlogo.cn/a":            "",
		"//thirdqq.qlogo.cn/a":                "",
	} {
		if got := qqAvatarURL(raw); got != want {
			t.Fatalf("%q -> %q, want %q", raw, got, want)
		}
	}
}
```

在文件 import 中加入 `"github.com/bbxx111/accountkit/observability"`。实施者先确认 `observability.Observer` 接口与 `observability.Observation` 类型名（见 `observability/` 包，`user/idp/alipay.go` 用法 `observability.NewRecorder(options.Observer, logger)`）；若 `Observe` 签名不同，按实际接口调整 `qqTestObserver`。

注意：`https` 头像不检查名单（交给既有净化）；改写后的 `https://ThirdQQ.QLogo.cn/a` 保留原始大小写主机，只替换 scheme。

- [ ] **Step 2: 运行确认失败**

Run: `GOWORK=off go test ./user/idp/ -run 'TestQQProfile|TestQQAvatarURL'`
Expected: 编译失败 `undefined: qqAvatarURL`

- [ ] **Step 3: 实现资料与头像**

用以下代码替换 `user/idp/qq.go` 中的 `profile` 桩，并在 import 中加入 `"strings"`：

```go
// qqAvatarHosts 是允许从 HTTP 升级为 HTTPS 的 QQ 头像主机；名单外的 HTTP 地址直接丢弃。
var qqAvatarHosts = map[string]bool{"thirdqq.qlogo.cn": true}

// qqAvatarURL 只做协议升级，长度、字符与主机格式仍由领域层既有净化负责。
func qqAvatarURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Opaque != "" || u.Host == "" {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return raw
	case "http":
		if u.Port() == "" && qqAvatarHosts[strings.ToLower(u.Hostname())] {
			u.Scheme = "https"
			return u.String()
		}
	}
	return ""
}

// profile 使用本次令牌查询资料；任何失败都只返回固定阶段错误，由 Verify 记为降级。
func (q *QQ) profile(ctx context.Context, appID, token, openID string) (string, string, error) {
	object, err := q.get(ctx, "/user/get_user_info", url.Values{"access_token": {token}, "oauth_consumer_key": {appID}, "openid": {openID}}, 3*time.Second)
	if err != nil {
		return "", "", err
	}
	ret, ok := object["ret"]
	if !ok || string(ret) != "0" {
		return "", "", qqFailure(ErrUnavailable, "profile result")
	}
	if lost, ok := object["is_lost"]; ok && string(lost) != "0" {
		return "", "", qqFailure(ErrUnavailable, "profile data lost")
	}
	name, nameErr := alipayString(object, "nickname")
	large, largeErr := alipayString(object, "figureurl_qq_2")
	small, smallErr := alipayString(object, "figureurl_qq_1")
	if nameErr != nil || largeErr != nil || smallErr != nil {
		return "", "", qqFailure(ErrUnavailable, "profile fields")
	}
	picture := large
	if strings.TrimSpace(picture) == "" {
		picture = small
	}
	return name, qqAvatarURL(picture), nil
}
```

`ret`、`is_lost` 用原始字节与 `0` 比较：`alipayObject` 已保证结构合法，数字 0 的规范 JSON 字节就是 `0`；`"0"`（字符串）或 `0.0` 视为不可信，记降级。

- [ ] **Step 4: 运行通过**

Run: `GOWORK=off go test ./user/idp/ -count=1`
Expected: PASS（含 `timeout` 子测试约 3 秒）

- [ ] **Step 5: 包注释**

`user/idp/idp.go` 第一行改为：

```go
// Package idp 把第三方登录凭证换成已验证的身份（微信/支付宝/QQ 标识、Apple/Google/OIDC sub）。
```

- [ ] **Step 6: gofmt、vet、提交**

```bash
gofmt -w user/idp
GOWORK=off go vet ./user/idp/
git add user/idp
git commit -m "feat(idp): read qq profile with avatar https rewrite"
```

勾选 OpenSpec 3.1–3.4。

---

### Task 4: 补填集合收敛与 QQ 领域行为（OpenSpec 4.1–4.4）

**Files:**
- Modify: `user/service_idp.go:60-68`、`user/service_signin_completion.go:99`、`user/service_signin_completion.go:539`
- Create: `user/service_qq_test.go`
- Modify: `docs/compatibility.md`（支付宝小节之后新增 QQ 小节）

**Interfaces:**
- Consumes: `enum.IdentityQQ`；测试复用同包 `user_test` 的 `alipayProvider(kind, calls)`、`providerCred`、`newFixture`、`newServiceWith`、`withProvider`、`mustSignInWithIdp`、`requireSignInTokens`、`assertAlipayProfile`、`alipayPendingPayload`、`completionCredential`、`principalOf`、`mustExec`、`replacementMutationGate`、`replacementWaitForPID`、`finishReplacementRace`、`replacementRaceResult`、`identityRaceLock`、`waitIdentityRaceBlocked`、常量 `dev1/dev2/meta1/email1/phone1/admin1`
- Produces: `func refillsProfileOnSignIn(kind enum.IdentityKind) bool`（`user` 包未导出）

- [ ] **Step 1: 写 QQ 领域失败测试**

创建 `user/service_qq_test.go`：

```go
package user_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/idp"
)

// QQ 复用合成提供方：领域补填只按 kind 判定，网络细节由 idp 包测试覆盖。
func qqCred(name, picture string) user.IdpCredential {
	return providerCred(enum.IdentityQQ, map[string]string{"sub": "qq-person", "app": "1100000001", "openid": "QQ-OPEN", "name": name, "picture": picture})
}

func useQQ(t *testing.T, f *fixture, calls *atomic.Int32, required []enum.IdentityKind) {
	t.Helper()
	newServiceWith(t, f, func(d *user.Deps) {
		*d = withProvider(*d, alipayProvider(enum.IdentityQQ, calls))
		d.RequiredIdentityKinds = required
	})
}

func TestQQLaterLoginBackfillsOnlyEmptyFields(t *testing.T) {
	for _, tc := range []struct{ name, avatar, wantName, wantAvatar string }{
		{"", "", "QQ Name", "https://thirdqq.example.test/qq.png"},
		{"Mine", "", "Mine", "https://thirdqq.example.test/qq.png"},
		{"", "https://example.test/mine.png", "QQ Name", "https://example.test/mine.png"},
		{"Mine", "https://example.test/mine.png", "Mine", "https://example.test/mine.png"},
	} {
		t.Run(tc.name+tc.avatar, func(t *testing.T) {
			f := newFixture(t)
			useQQ(t, f, nil, []enum.IdentityKind{})
			first := requireSignInTokens(t, mustSignInWithIdp(t, f, qqCred("", "")))
			if _, err := f.svc.UpdateProfile(context.Background(), first.UserID, user.ProfilePatch{DisplayName: &tc.name, AvatarURL: &tc.avatar}); err != nil {
				t.Fatal(err)
			}
			f.advance(time.Second)
			again := requireSignInTokens(t, mustSignInWithIdp(t, f, qqCred("QQ Name", "https://thirdqq.example.test/qq.png")))
			if again.UserID != first.UserID || again.IsNewUser {
				t.Fatal("later sign-in changed user")
			}
			assertAlipayProfile(t, f, first.UserID, tc.wantName, tc.wantAvatar)
		})
	}
}

func TestQQSameCredentialTwiceSignsInSameIdentity(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int32
	useQQ(t, f, &calls, []enum.IdentityKind{})
	a := requireSignInTokens(t, mustSignInWithIdp(t, f, qqCred("", "")))
	b := requireSignInTokens(t, mustSignInWithIdp(t, f, qqCred("", "")))
	if a.UserID != b.UserID || calls.Load() != 2 {
		t.Fatalf("repeat token: %s %s calls=%d", a.UserID, b.UserID, calls.Load())
	}
}

func TestQQPendingAlreadyOwnedProofBackfills(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int32
	useQQ(t, f, &calls, nil)
	owner := f.signIn(t, enum.IdentityEmail, email1, dev2)
	pending := mustSignInWithIdp(t, f, qqCred("Pending Owner", "https://example.test/pending.png"))
	if pending.Pending == nil {
		t.Fatal("expected pending")
	}
	bound, _, err := f.svc.BindWithIdp(context.Background(), principalOf(t, f, owner), qqCred("", ""), meta1)
	if err != nil {
		t.Fatal(err)
	}
	alipayPendingPayload(t, f, func(p map[string]any) {
		proof := p["Proofs"].([]any)[0].(map[string]any)
		proof["UserID"] = owner.UserID
		proof["IdentityID"] = bound.ID
	})
	cred := completionCredential(t, f, pending.Pending, enum.IdentityEmail, email1)
	done, err := f.svc.CompleteSignIn(context.Background(), pending.Pending.SignInCompletionToken, cred, dev1, meta1)
	if err != nil || requireSignInTokens(t, done).UserID != owner.UserID {
		t.Fatal(err)
	}
	assertAlipayProfile(t, f, owner.UserID, "Pending Owner", "https://example.test/pending.png")
	if calls.Load() != 2 {
		t.Fatal("completion re-verified provider")
	}
}

func TestQQPendingSanitizesAndCompletesWithoutVerifyReplay(t *testing.T) {
	f := newFixture(t)
	var calls atomic.Int32
	useQQ(t, f, &calls, nil)
	pending := mustSignInWithIdp(t, f, qqCred("Pending\n‮", "http://insecure.example.test/avatar"))
	if pending.Pending == nil {
		t.Fatal("expected pending")
	}
	provider := alipayPendingPayload(t, f, nil)["Proofs"].([]any)[0].(map[string]any)["Provider"].(map[string]any)
	if provider["Name"] != "Pending" || provider["Picture"] != "" {
		t.Fatalf("unsanitized pending provider: %+v", provider)
	}
	cred := completionCredential(t, f, pending.Pending, enum.IdentityEmail, email1)
	done, err := f.svc.CompleteSignIn(context.Background(), pending.Pending.SignInCompletionToken, cred, dev1, meta1)
	if err != nil {
		t.Fatal(err)
	}
	assertAlipayProfile(t, f, requireSignInTokens(t, done).UserID, "Pending", "")
	if calls.Load() != 1 {
		t.Fatal("completion called provider")
	}
}

func TestQQLoginProfileRollbackAfterSessionFailure(t *testing.T) {
	f := newFixture(t)
	useQQ(t, f, nil, []enum.IdentityKind{})
	first := requireSignInTokens(t, mustSignInWithIdp(t, f, qqCred("", "")))
	mustExec(t, f, `CREATE FUNCTION reject_qq_session() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic session failure'; END $$; CREATE TRIGGER reject_qq_session BEFORE INSERT ON session FOR EACH ROW EXECUTE FUNCTION reject_qq_session()`)
	if _, err := f.svc.SignInWithIdp(context.Background(), qqCred("Rollback", "https://example.test/rollback.png"), dev2, meta1); err == nil {
		t.Fatal("session failure succeeded")
	}
	assertAlipayProfile(t, f, first.UserID, "", "")
	rows, _ := f.repo.Q().ListActiveIdentitiesByUser(context.Background(), first.UserID)
	if strings.Contains(string(rows[0].ProviderMeta), "Rollback") {
		t.Fatal("provider metadata survived rollback")
	}
}

func TestQQConcurrentPATCHNonemptyProfileWins(t *testing.T) {
	f := newFixture(t)
	useQQ(t, f, nil, []enum.IdentityKind{})
	first := requireSignInTokens(t, mustSignInWithIdp(t, f, qqCred("", "")))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	gate := replacementMutationGate(t, ctx, f, "user_account", "UPDATE", "NEW.display_name IS DISTINCT FROM OLD.display_name")
	patch := make(chan replacementRaceResult, 1)
	name, avatar := "PATCH Name", "https://example.test/patch.png"
	go func() {
		_, err := f.svc.UpdateProfile(ctx, first.UserID, user.ProfilePatch{DisplayName: &name, AvatarURL: &avatar})
		patch <- replacementRaceResult{err: err}
	}()
	pid := replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), patch)
	login := make(chan replacementRaceResult, 1)
	go func() {
		_, err := f.svc.SignInWithIdp(ctx, qqCred("Provider", "https://example.test/provider.png"), dev2, meta1)
		login <- replacementRaceResult{err: err}
	}()
	replacementWaitForPID(t, ctx, f, pid, login)
	if err := gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if out := finishReplacementRace(t, ctx, patch); out.err != nil {
		t.Fatal(out.err)
	}
	if out := finishReplacementRace(t, ctx, login); out.err != nil {
		t.Fatal(out.err)
	}
	assertAlipayProfile(t, f, first.UserID, name, avatar)
}

func TestQQLoginRechecksAccountState(t *testing.T) {
	for _, state := range []enum.UserState{enum.UserFrozen, enum.UserPendingDeletion, enum.UserDeleted} {
		t.Run(state.String(), func(t *testing.T) {
			f := newFixture(t)
			useQQ(t, f, nil, []enum.IdentityKind{})
			first := requireSignInTokens(t, mustSignInWithIdp(t, f, qqCred("", "")))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			tx := identityRaceLock(t, ctx, f, first.UserID, "")
			done := make(chan error, 1)
			go func() {
				_, err := f.svc.SignInWithIdp(ctx, qqCred("Provider", "https://example.test/provider.png"), dev2, meta1)
				done <- err
			}()
			waitIdentityRaceBlocked(t, ctx, f, tx, done)
			if _, err := tx.Exec(ctx, `UPDATE user_account SET state=$2 WHERE id=$1`, first.UserID, state); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err == nil {
				t.Fatal("inactive login accepted")
			}
			var name, avatar *string
			if err := f.pool.QueryRow(ctx, `SELECT display_name,avatar_url FROM user_account WHERE id=$1`, first.UserID).Scan(&name, &avatar); err != nil || name != nil && *name != "" || avatar != nil && *avatar != "" {
				t.Fatal("inactive account was backfilled")
			}
		})
	}
}

func TestQQLoginCannotRestoreProfileAfterConcurrentPurge(t *testing.T) {
	f := newFixture(t)
	useQQ(t, f, nil, []enum.IdentityKind{})
	first := requireSignInTokens(t, mustSignInWithIdp(t, f, qqCred("", "")))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := f.svc.AdminDeleteUser(ctx, admin1, first.UserID, meta1); err != nil {
		t.Fatal(err)
	}
	f.advance(360*time.Hour + time.Second)
	gate := replacementMutationGate(t, ctx, f, "user_account", "UPDATE", "NEW.state <> OLD.state")
	purge := make(chan replacementRaceResult, 1)
	go func() { _, err := f.svc.PurgeDueUsers(ctx); purge <- replacementRaceResult{err: err} }()
	pid := replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), purge)
	login := make(chan replacementRaceResult, 1)
	go func() {
		_, err := f.svc.SignInWithIdp(ctx, qqCred("Must Not Restore", "https://example.test/late.png"), dev2, meta1)
		login <- replacementRaceResult{err: err}
	}()
	replacementWaitForPID(t, ctx, f, pid, login)
	if err := gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if out := finishReplacementRace(t, ctx, purge); out.err != nil {
		t.Fatal(out.err)
	}
	if out := finishReplacementRace(t, ctx, login); !errors.Is(out.err, idp.ErrInvalidCredential) {
		t.Fatalf("stale identity accepted after purge: %v", out.err)
	}
	row, err := f.repo.Q().GetUserByID(ctx, first.UserID)
	if err != nil || row.State != enum.UserDeleted || row.DisplayName != nil || row.AvatarUrl != nil {
		t.Fatalf("login restored anonymized profile: %+v %v", row, err)
	}
}

func TestQQConcurrentAppsShareSingleUnionIdentity(t *testing.T) {
	f := newFixture(t)
	useQQ(t, f, nil, []enum.IdentityKind{})
	start := make(chan struct{})
	type outcome struct {
		result user.SignInResult
		err    error
	}
	done := make(chan outcome, 2)
	for _, app := range []string{"1100000001", "1100000002"} {
		go func(app string) {
			<-start
			r, err := f.svc.SignInWithIdp(context.Background(), providerCred(enum.IdentityQQ, map[string]string{"sub": "shared", "app": app, "openid": app + "-open"}), user.Device{ID: app}, meta1)
			done <- outcome{r, err}
		}(app)
	}
	close(start)
	a, b := <-done, <-done
	if a.err != nil || b.err != nil {
		t.Fatalf("concurrent login: %v %v", a.err, b.err)
	}
	if requireSignInTokens(t, a.result).UserID != requireSignInTokens(t, b.result).UserID {
		t.Fatal("same UnionID across QQ apps created two accounts")
	}
	var n int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM identity WHERE kind='QQ'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("qq identities=%d %v", n, err)
	}
}

func TestQQBindUnbindAndDisabledHistory(t *testing.T) {
	f := newFixture(t)
	useQQ(t, f, nil, nil)
	phone := f.signIn(t, enum.IdentityPhone, phone1, dev1)
	p := principalOf(t, f, phone)
	bound, created, err := f.svc.BindWithIdp(context.Background(), p, qqCred("", ""), meta1)
	if err != nil || !created {
		t.Fatal(err)
	}
	newServiceWith(t, f, func(d *user.Deps) {
		d.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail}
		*d = withProvider(*d, alipayProvider(enum.IdentityQQ, nil))
	})
	if _, err := f.svc.SignInWithIdp(context.Background(), qqCred("Must Not Seed", ""), dev2, meta1); !errors.Is(err, user.ErrIdentityKindNotEnabled) {
		t.Fatal(err)
	}
	rows, err := f.svc.ListIdentities(context.Background(), phone.UserID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("disabled QQ history invisible: %+v %v", rows, err)
	}
	if err := f.svc.UnbindIdentity(context.Background(), p, bound.ID, meta1); err != nil {
		t.Fatal(err)
	}
	assertAlipayProfile(t, f, phone.UserID, "", "")
}
```

若同包已存在 `fixture` 字段/方法名与上面不同（例如 `f.advance`、`f.signIn`），以 `user/service_alipay_test.go` 的实际用法为准——上面的调用均摘自该文件。

- [ ] **Step 2: 运行确认失败**

Run: `GOWORK=off SERVER_TEST_DB_DSN=<一次性库> go test ./user/ -run 'TestQQ' -count=1`
Expected: `TestQQLaterLoginBackfillsOnlyEmptyFields`、`TestQQPendingAlreadyOwnedProofBackfills` 等补填用例 FAIL（显示名/头像仍为空），其余 PASS。没有 DSN 时数据库用例会 skip——此步必须提供 DSN，不能以 skip 视为复现。

- [ ] **Step 3: 实现集合判定**

在 `user/service_idp.go` 中，把：

```go
	// Pending proofs retain only the current, sanitized Alipay claims. Other
	// providers keep their existing proof and first-identity profile semantics.
	if cred.Kind == enum.IdentityAlipay {
```

替换为：

```go
	// Pending proofs retain only the current, sanitized claims of sign-in refill
	// providers. Other providers keep their existing proof and first-identity profile semantics.
	if refillsProfileOnSignIn(cred.Kind) {
```

并在该函数之后新增：

```go
// refillsProfileOnSignIn 报告该提供方在首次及后续成功登录时是否补填账号仍为空的显示名与头像。
// 集合固定为 ALIPAY 与 QQ；宿主或 OIDC 提供方不在集合内。
func refillsProfileOnSignIn(kind enum.IdentityKind) bool {
	return kind == enum.IdentityAlipay || kind == enum.IdentityQQ
}
```

在 `user/service_signin_completion.go` 第 99 行与第 539 行，把 `proof.Kind == enum.IdentityAlipay` 改为 `refillsProfileOnSignIn(proof.Kind)`。

- [ ] **Step 4: 运行通过并回归支付宝**

Run: `GOWORK=off SERVER_TEST_DB_DSN=<一次性库> go test ./user/ -run 'TestQQ|TestAlipay|Profile' -count=1`
Expected: PASS，且支付宝全部用例不变通过。

- [ ] **Step 5: 兼容说明**

在 `docs/compatibility.md` 的“支付宝 App 极简登录与空资料补填”小节之后新增：

```markdown
## QQ App SDK 登录

新增内置 `QQ`、`QQSubjectMode` 及 `Config.QQAppIDs` / `QQSubjectMode` / `QQAPIBaseURL`，沿用统一凭证信封：`{"kind":"QQ","credential":{"app_id","access_token"}}`。仅支持 QQ 互联移动 SDK；无网页回调、授权码或 AppKey 配置。

服务端调用 `oauth2.0/me` 校验令牌，`client_id` 必须等于请求的 `app_id`，另一 App 的令牌为 400 `IDP_APP_NOT_ALLOWED`。QQ access_token 与 Facebook Classic 一样不做一次性限制，有效期内可重复登录；客户端须保密令牌。

UNIONID 默认使用 `unionid:<unionid>`，需在 QQ 互联开通 UnionID 打通，未开通时登录返回固定 500；OPENID 使用 `openid:<app_id>:<openid>` 且不请求 UnionID。切换模式会让既有 QQ 身份无法命中，不自动迁移或合并。

登录补填提供方集合现为 ALIPAY、QQ：两者首次及后续成功登录都按字段补填仍为空的显示名与头像；其他提供方仍只在首次写入身份时填充。QQ 头像仅对 `thirdqq.qlogo.cn` 的 `http://` 地址升级为 `https://`，其他 HTTP 地址丢弃。指标追加 `qq`、`verify_qq`、`qq_profile_unavailable`。无 schema/迁移变化。
```

- [ ] **Step 6: gofmt、vet、提交**

```bash
gofmt -w user
GOWORK=off go vet ./user/...
git add user/service_idp.go user/service_signin_completion.go user/service_qq_test.go docs/compatibility.md
git commit -m "feat(user): refill empty profile for qq sign-in"
```

勾选 OpenSpec 4.1–4.4。

---

### Task 5: 库装配、服务配置与观测（OpenSpec 5.1–5.4）

**Files:**
- Modify: `accountkit.go:488-494`（支付宝装配之后）
- Create: `accountkit_qq_test.go`
- Modify: `internal/accountsvc/config.go`（显式启用检查、错误名映射）、`internal/accountsvc/config_test.go`
- Modify: `observability/classification.go`、`observability/internal/metricdata/project.go`、`project_test.go`
- Modify: `user/observability.go:62`、`user/observability.go:486`
- Modify: `docs/metrics.md`、`docs/go-integration.md`、`docs/accountsvc.md`、`README.md`、`deploy/accountsvc/.env.example`

**Interfaces:**
- Consumes: `idp.NewQQ`、`Config.qqConfig()`、`DegradationQQProfileUnavailable`
- Produces: `observability.MethodQQ = "qq"`、`observability.DependencyVerifyQQ = "verify_qq"`

- [ ] **Step 1: 写装配失败测试**

创建 `accountkit_qq_test.go`：

```go
package accountkit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/idp"
)

func TestQQProviderAssembly(t *testing.T) {
	providers, err := identityProviders(Config{QQAppIDs: []string{"1100000001"}}, Deps{})
	if err != nil || len(providers) != 1 {
		t.Fatalf("configured QQ provider not assembled: %d %v", len(providers), err)
	}
	if _, ok := providers[0].(*idp.QQ); !ok || providers[0].Kind() != enum.IdentityQQ {
		t.Fatal("wrong provider")
	}
	if _, err := providers[0].Verify(context.Background(), json.RawMessage(`{"app_id":"1100000009","access_token":"t"}`)); !errors.Is(err, idp.ErrAppNotAllowed) {
		t.Fatalf("provider configuration not used: %v", err)
	}
	if _, err := identityProviders(Config{QQAppIDs: []string{"bad"}}, Deps{}); err == nil {
		t.Fatal("assembly ignored invalid QQ configuration")
	}
	if providers, err := identityProviders(Config{}, Deps{}); err != nil || len(providers) != 0 {
		t.Fatal("unconfigured QQ should stay disabled")
	}
}
```

在 `internal/accountsvc/config_test.go` 追加（`configEnvironment` 为文件已有辅助函数，设置最小 serve 环境）：

```go
func TestConfigQQ(t *testing.T) {
	t.Run("pure QQ without SMTP", func(t *testing.T) {
		configEnvironment(t)
		t.Setenv("ACCOUNTKIT_QQ_APP_IDS", "1100000001,1100000002")
		t.Setenv("ACCOUNTKIT_ENABLED_IDENTITY_KINDS", "QQ")
		t.Setenv("ACCOUNTKIT_REQUIRED_IDENTITY_KINDS", "[]")
		c, err := LoadConfig("serve")
		if err != nil || len(c.Library.QQAppIDs) != 2 || c.SMTP.Host != "" {
			t.Fatalf("pure QQ service: %v", err)
		}
	})
	t.Run("enabled without apps", func(t *testing.T) {
		configEnvironment(t)
		t.Setenv("ACCOUNTKIT_ENABLED_IDENTITY_KINDS", "QQ")
		t.Setenv("ACCOUNTKIT_REQUIRED_IDENTITY_KINDS", "[]")
		if _, err := LoadConfig("serve"); err == nil || !strings.Contains(err.Error(), "ACCOUNTKIT_QQ_APP_IDS") {
			t.Fatalf("missing apps: %v", err)
		}
	})
	for name, env := range map[string][2]string{
		"ACCOUNTKIT_QQ_APP_IDS":      {"ACCOUNTKIT_QQ_APP_IDS", "1100000001,sensitive-app"},
		"ACCOUNTKIT_QQ_SUBJECT_MODE": {"ACCOUNTKIT_QQ_SUBJECT_MODE", "sensitive-mode"},
		"ACCOUNTKIT_QQ_API_BASE_URL": {"ACCOUNTKIT_QQ_API_BASE_URL", "http://sensitive.example.test"},
	} {
		for _, mode := range []string{"serve", "migrate"} {
			t.Run(mode+"_"+name, func(t *testing.T) {
				configEnvironment(t)
				t.Setenv("ACCOUNTKIT_QQ_APP_IDS", "1100000001")
				t.Setenv(env[0], env[1])
				_, err := LoadConfig(mode)
				if err == nil || !strings.Contains(err.Error(), name) || strings.Contains(err.Error(), "sensitive") {
					t.Fatalf("%s: %v", name, err)
				}
			})
		}
	}
	t.Run("migrate without QQ", func(t *testing.T) {
		configEnvironment(t)
		if _, err := LoadConfig("migrate"); err != nil {
			t.Fatalf("migrate must not require QQ: %v", err)
		}
	})
}
```

实施者先看 `TestConfigAlipay`（`internal/accountsvc/config_test.go:44`）确认 `configEnvironment` 是否已满足 serve 所需全部变量（含 SMTP 省略时的 Required 处理）；若纯 QQ 子测试还需额外变量，照抄 `TestConfigAlipay` 的 “pure Alipay without SMTP” 子测试中设置的变量。

在 `observability/internal/metricdata/project_test.go` 追加一个与 `TestAlipayMetricProjection` 结构相同的 `TestQQMetricProjection`：打开该文件，复制 `TestAlipayMetricProjection` 整个函数，函数名改为 `TestQQMetricProjection`，并把其中的 `MethodAlipay`、`DependencyVerifyAlipay`、`DegradationAlipayProfileUnavailable` 分别替换为 `MethodQQ`、`DependencyVerifyQQ`、`DegradationQQProfileUnavailable`，期望的标签字符串 `alipay`/`verify_alipay`/`alipay_profile_unavailable` 替换为 `qq`/`verify_qq`/`qq_profile_unavailable`。

- [ ] **Step 2: 运行确认失败**

Run: `GOWORK=off go test . ./internal/accountsvc/ ./observability/... -run 'QQ' -count=1`
Expected: 编译失败（`MethodQQ` 未定义）或 `TestQQProviderAssembly` FAIL（提供方数为 0）

- [ ] **Step 3: 实现装配**

`accountkit.go` 支付宝装配块之后：

```go
	if len(cfg.QQAppIDs) > 0 {
		provider, err := idp.NewQQ(idp.QQOptions{Config: cfg.qqConfig(), HTTP: deps.HTTPClient, Logger: deps.Logger, Observer: deps.Observer})
		if err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
```

- [ ] **Step 4: 实现观测取值**

`observability/classification.go`：方法常量块 `MethodAlipay` 之后加 `MethodQQ Method = "qq"`；依赖常量块 `DependencyVerifyAlipay` 之后加 `DependencyVerifyQQ DependencyOperation = "verify_qq"`（gofmt 对齐）。

`observability/internal/metricdata/project.go`：
- 第 44 行 `normalize(...)` 参数中 `o.MethodAlipay` 之后加 `o.MethodQQ`；
- 第 48 行条件中 `method == o.MethodAlipay ||` 之后加 `method == o.MethodQQ ||`；
- 第 77 行 `oneOf(...)` 中 `o.DegradationAlipayProfileUnavailable` 之后加 `o.DegradationQQProfileUnavailable`；
- 第 164 行 `oneOf(...)` 中 `o.DependencyVerifyAlipay` 之后加 `o.DependencyVerifyQQ`。

`user/observability.go`：第 62 行附近 `case enum.IdentityAlipay: return ob.MethodAlipay` 之后加

```go
	case enum.IdentityQQ:
		return ob.MethodQQ
```

第 486 行附近 `case enum.IdentityAlipay: return ob.DependencyVerifyAlipay` 之后加

```go
	case enum.IdentityQQ:
		return ob.DependencyVerifyQQ
```

- [ ] **Step 5: 实现 accountsvc**

`internal/accountsvc/config.go`：
- 支付宝显式启用检查之后：

```go
		if c.identityEnabled(enum.IdentityQQ) && len(c.Library.QQAppIDs) == 0 {
			return Config{}, invalidConfig("ACCOUNTKIT_QQ_APP_IDS")
		}
```

- `libraryAlipayFieldError` 之后：

```go
var libraryQQFieldError = regexp.MustCompile(`^accountkit: idp: (QQAppIDs|QQSubjectMode|QQAPIBaseURL)(?:\[| )`)
```

- `safeLibraryConfigError` 中支付宝字段分支之后：

```go
	if matches := libraryQQFieldError.FindStringSubmatch(err.Error()); len(matches) == 2 {
		return invalidConfig(map[string]string{"QQAppIDs": "ACCOUNTKIT_QQ_APP_IDS", "QQSubjectMode": "ACCOUNTKIT_QQ_SUBJECT_MODE", "QQAPIBaseURL": "ACCOUNTKIT_QQ_API_BASE_URL"}[matches[1]])
	}
```

环境变量层面的非法 AppID / 模式由 `ConfigFromEnv` 以 `accountkit: ACCOUNTKIT_QQ_...:` 报出，已被现有 `libraryErrorName` 捕获；非 HTTPS 地址由库 `Validate` 报 `accountkit: idp: QQAPIBaseURL ...`，由新正则映射。

- [ ] **Step 6: 运行通过**

Run: `GOWORK=off go test . ./internal/accountsvc/ ./observability/... ./user/ -count=1`
Expected: PASS（无 DSN 时 user 包数据库用例 skip；此处只验证编译与非数据库用例，数据库验证在 Task 4 与 Task 6 进行）

- [ ] **Step 7: 文档**

1. `docs/metrics.md`：第 118 行 method 列表 `alipay`、之后加 `qq`、；第 123 行依赖列表 `verify_alipay/` 之后加 `verify_qq/`；第 125 行 scenario 列表末尾加 `、qq_profile_unavailable`；第 140 行段落之后新增一段：

```markdown
QQ 一次 Verify 包含 `/oauth2.0/me` 校验和可选资料请求，只计一次 `verify_qq` 逻辑调用及整体耗时。资料失败、`ret` 非 0 或 `is_lost=1` 但身份可信时依赖可为 success，另计 `qq_profile_unavailable`；正常缺昵称/头像或头像因域名名单被丢弃不计降级；外层取消按取消计。AppID、openid、unionid、access_token、URL 和上游原文均不进入标签。
```

2. `docs/go-integration.md` 第 34 行（支付宝段落）之后：

```markdown
配置 `QQAppIDs []string`（QQ 互联移动应用 AppID）启用 QQ；App SDK 校验 access_token 不需要 AppKey。`QQSubjectMode` 缺省 UNIONID（需在 QQ 互联开通 UnionID 打通），OPENID 按 AppID 隔离且不请求 UnionID。`New` 无网络 I/O。QQ access_token 在有效期内可重复用于登录，客户端只经 HTTPS 提交并且不得记录它。纯 QQ 可设 Enabled 为 QQ、Required 为显式空列表，无需发送器。详见 [QQ 接入指南](qq-sign-in.md)。
```

3. `docs/accountsvc.md` 支付宝两段之后：

```markdown
QQ App SDK 登录使用 `ACCOUNTKIT_QQ_APP_IDS`（逗号分隔的唯一 1–20 位数字 AppID）、`ACCOUNTKIT_QQ_SUBJECT_MODE`（默认 `UNIONID`）和测试用 `ACCOUNTKIT_QQ_API_BASE_URL`；不需要 AppKey。显式 Enabled 含 QQ 却未配置应用时启动失败并指出 `ACCOUNTKIT_QQ_APP_IDS`，非法值只报告变量名。

纯 QQ 部署设 `ACCOUNTKIT_ENABLED_IDENTITY_KINDS=QQ`、`ACCOUNTKIT_REQUIRED_IDENTITY_KINDS=[]`，无需 SMTP；默认 Required 仍要求手机号或邮箱补全。migrate 不需要 QQ 配置，但已提供的非法 QQ 配置会被拒绝。
```

4. `deploy/accountsvc/.env.example` 支付宝块之后：

```bash
# QQ App SDK sign-in (no AppKey required)
# ACCOUNTKIT_QQ_APP_IDS=1100000001,1100000002
# ACCOUNTKIT_QQ_SUBJECT_MODE=UNIONID
# ACCOUNTKIT_QQ_API_BASE_URL=https://graph.qq.com
# Pure QQ: explicitly pass ENABLED_IDENTITY_KINDS=QQ and REQUIRED_IDENTITY_KINDS=[].
```

5. `README.md`：凭证示例块（`ALIPAY` 行之后）加

```json
{"kind": "QQ", "credential": {"app_id": "1100000001", "access_token": "synthetic-sdk-access-token"}}
```

并在“支付宝”要点之后加一条：

```markdown
- **QQ**：仅 QQ 互联移动 SDK；客户端提交 SDK 返回的 `access_token`，不提交 openid。服务端以最多 10 秒调用 `oauth2.0/me`（`fmt=json`），`client_id` 须等于 `app_id`，否则 `IDP_APP_NOT_ALLOWED`；令牌失效为 400 `IDP_CREDENTIAL_INVALID`，网络/5xx/未知错误码为 503 `IDP_UNAVAILABLE`。令牌不做一次性限制。`QQSubjectMode` 默认 UNIONID（`unionid:<unionid>`，未开通 UnionID 为 500），OPENID 为 `openid:<app_id>:<openid>`。资料以独立 3 秒查询 `get_user_info`，取 `nickname` 与 `figureurl_qq_2`（缺失用 `figureurl_qq_1`），`thirdqq.qlogo.cn` 的 http 头像升级为 https；资料失败不阻断登录。与支付宝相同，首次及后续成功登录补填空昵称/头像。无 schema/迁移变更。见 [QQ 接入指南](docs/qq-sign-in.md)。
```

- [ ] **Step 8: gofmt、vet、提交**

```bash
gofmt -w accountkit.go accountkit_qq_test.go internal observability user
GOWORK=off go vet ./...
git add accountkit.go accountkit_qq_test.go internal/accountsvc observability user/observability.go docs/metrics.md docs/go-integration.md docs/accountsvc.md README.md deploy/accountsvc/.env.example
git commit -m "feat(accountsvc): assemble qq sign-in and observability"
```

勾选 OpenSpec 5.1–5.4。

---

### Task 6: 接入指南与正式 HTTP 契约验收（OpenSpec 6.1–6.4）

**Files:**
- Create: `docs/qq-sign-in.md`
- Create: `accountkit_qq_http_test.go`
- Modify: `scripts/required-tests.txt`
- Create: `openspec/changes/add-qq-app-sign-in/verification.md`

**Interfaces:**
- Consumes（同包 `accountkit_test`，不修改）: `newAlipayHTTPFixture(t, cfg) *alipayHTTPFixture`、`(*alipayHTTPFixture).call`、`.tokenResult`、`.assertMe`、`.counts`、`alipayHTTPString`、`requireAlipayHTTPDependencies`、`minimal()`
- Produces: `TestQQHTTPLoginContract`、`TestQQHTTPMultiAppIdentityContract`

- [ ] **Step 1: 写 HTTP 契约测试**

创建 `accountkit_qq_http_test.go`：

```go
package accountkit_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/enum"
)

// The mock is independent of idp's parser: it only serves fixed JSON for known tokens.
type qqHTTPGrant struct {
	app, open, union, name, avatar string
	profileUnavailable             bool
}

type qqHTTPServer struct {
	t        *testing.T
	server   *httptest.Server
	grants   map[string]qqHTTPGrant
	mu       sync.Mutex
	me, info map[string]int
}

func newQQHTTPServer(t *testing.T, grants map[string]qqHTTPGrant) *qqHTTPServer {
	t.Helper()
	s := &qqHTTPServer{t: t, grants: grants, me: map[string]int{}, info: map[string]int{}}
	s.server = httptest.NewServer(http.HandlerFunc(s.serveHTTP))
	t.Cleanup(s.server.Close)
	return s
}

func (s *qqHTTPServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	token := q.Get("access_token")
	s.mu.Lock()
	defer s.mu.Unlock()
	grant, ok := s.grants[token]
	switch r.URL.Path {
	case "/oauth2.0/me":
		s.me[token]++
		if q.Get("fmt") != "json" {
			s.t.Error("me must request JSON")
		}
		if !ok {
			fmt.Fprint(w, `{"error":100016,"error_description":"access token check failed"}`)
			return
		}
		body := map[string]string{"client_id": grant.app, "openid": grant.open}
		if q.Get("unionid") == "1" && grant.union != "" {
			body["unionid"] = grant.union
		}
		_ = json.NewEncoder(w).Encode(body)
	case "/user/get_user_info":
		s.info[token]++
		if !ok || q.Get("oauth_consumer_key") != grant.app || q.Get("openid") != grant.open {
			s.t.Error("profile request does not match verified identity")
			w.WriteHeader(400)
			return
		}
		if grant.profileUnavailable {
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ret": 0, "nickname": grant.name, "figureurl_qq_2": grant.avatar})
	default:
		s.t.Error("unexpected QQ path")
		w.WriteHeader(404)
	}
}

func (s *qqHTTPServer) assertCalls(t *testing.T, token string, me, info int) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.me[token] != me || s.info[token] != info {
		t.Fatalf("QQ call counts: me=%d info=%d, want %d/%d", s.me[token], s.info[token], me, info)
	}
}

func (s *qqHTTPServer) config(mode accountkit.QQSubjectMode, apps ...string) accountkit.Config {
	cfg := minimal()
	cfg.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityQQ}
	cfg.RequiredIdentityKinds = []enum.IdentityKind{}
	cfg.QQAppIDs, cfg.QQSubjectMode, cfg.QQAPIBaseURL = apps, mode, s.server.URL
	return cfg
}

func qqLoginBody(app, token string) map[string]any {
	return map[string]any{"kind": "QQ", "credential": map[string]string{"app_id": app, "access_token": token}}
}

func TestQQHTTPLoginContract(t *testing.T) {
	requireAlipayHTTPDependencies(t)
	const app, other = "1100000001", "1100000002"
	s := newQQHTTPServer(t, map[string]qqHTTPGrant{
		"first":       {app: app, open: "OPEN-ONE", union: "UID_ONE", profileUnavailable: true},
		"name":        {app: app, open: "OPEN-ONE", union: "UID_ONE", name: "QQ name"},
		"avatar":      {app: app, open: "OPEN-ONE", union: "UID_ONE", name: "Other", avatar: "http://thirdqq.qlogo.cn/g?s=100"},
		"other-app":   {app: other, open: "OPEN-X", union: "UID_X"},
	})
	f := newAlipayHTTPFixture(t, s.config(accountkit.QQSubjectModeUnionID, app, other))
	token := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", qqLoginBody(app, "first"), 200))
	f.assertMe(token, "", "")
	s.assertCalls(t, "first", 1, 1)
	again := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", qqLoginBody(app, "first"), 200))
	if again.user != token.user {
		t.Fatal("repeated access token did not sign in the same account")
	}
	refreshed := f.tokenResult(f.call("POST", "/token", "", map[string]string{"grant_type": "refresh_token", "refresh_token": token.refresh}, 200))
	if refreshed.user != token.user || refreshed.refresh == token.refresh {
		t.Fatal("refresh did not rotate")
	}
	before := f.counts()
	for _, tc := range []struct {
		app, token, reason string
	}{{app, "unknown", "IDP_CREDENTIAL_INVALID"}, {app, "other-app", "IDP_APP_NOT_ALLOWED"}, {"1100000009", "first", "IDP_APP_NOT_ALLOWED"}} {
		body := f.call("POST", "/users:signInWithIdp", "", qqLoginBody(tc.app, tc.token), 400)
		var failure struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(body["error"], &failure); err != nil || failure.Reason != tc.reason {
			t.Fatalf("%s: unexpected safe error classification", tc.token)
		}
		if f.counts() != before {
			t.Fatal("rejected login changed account, identity, session or pending rows")
		}
	}
	s.assertCalls(t, "other-app", 1, 0)
	token = f.tokenResult(f.call("POST", "/users:signInWithIdp", "", qqLoginBody(app, "name"), 200))
	f.assertMe(token, "QQ name", "")
	token = f.tokenResult(f.call("POST", "/users:signInWithIdp", "", qqLoginBody(app, "avatar"), 200))
	f.assertMe(token, "QQ name", "https://thirdqq.qlogo.cn/g?s=100")
	counts := f.counts()
	if counts[0] != 1 || counts[1] != 1 || counts[3] != 0 {
		t.Fatal("single-app QQ login did not retain one account/identity without pending rows")
	}
}

func TestQQHTTPMultiAppIdentityContract(t *testing.T) {
	requireAlipayHTTPDependencies(t)
	const appA, appB = "1100000001", "1100000002"
	t.Run("UNIONID shares identity and merges OpenIDs", func(t *testing.T) {
		s := newQQHTTPServer(t, map[string]qqHTTPGrant{
			"a": {app: appA, open: "open-a", union: "UID_SHARED", profileUnavailable: true},
			"b": {app: appB, open: "open-b", union: "UID_SHARED", name: "Cross", avatar: "https://thirdqq.qlogo.cn/cross"},
		})
		f := newAlipayHTTPFixture(t, s.config(accountkit.QQSubjectModeUnionID, appA, appB))
		a := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", qqLoginBody(appA, "a"), 200))
		b := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", qqLoginBody(appB, "b"), 200))
		if a.user != b.user {
			t.Fatal("same UnionID across QQ apps created separate accounts")
		}
		f.assertMe(b, "Cross", "https://thirdqq.qlogo.cn/cross")
		var subject string
		var raw []byte
		if err := f.pool.QueryRow(context.Background(), `SELECT provider_subject, provider_meta FROM identity WHERE kind = 'QQ' AND delete_time IS NULL`).Scan(&subject, &raw); err != nil {
			t.Fatal(err)
		}
		var meta struct {
			UnionID string            `json:"unionid"`
			OpenIDs map[string]string `json:"openids"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil || subject != "unionid:UID_SHARED" || meta.UnionID != "UID_SHARED" || meta.OpenIDs[appA] != "open-a" || meta.OpenIDs[appB] != "open-b" {
			t.Fatal("UnionID identity did not merge both application OpenIDs")
		}
	})
	t.Run("OPENID isolates apps", func(t *testing.T) {
		s := newQQHTTPServer(t, map[string]qqHTTPGrant{
			"a": {app: appA, open: "SAME-OPEN", union: "UID_IGNORED"},
			"b": {app: appB, open: "SAME-OPEN", union: "UID_IGNORED"},
		})
		f := newAlipayHTTPFixture(t, s.config(accountkit.QQSubjectModeOpenID, appA, appB))
		a := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", qqLoginBody(appA, "a"), 200))
		b := f.tokenResult(f.call("POST", "/users:signInWithIdp", "", qqLoginBody(appB, "b"), 200))
		if a.user == b.user {
			t.Fatal("same OpenID text across apps shared an account")
		}
		counts := f.counts()
		if counts[0] != 2 || counts[1] != 2 {
			t.Fatal("OPENID mode did not keep two accounts/identities")
		}
	})
}
```

`HTTP 400` 的错误体字段名以 `TestAlipayHTTPLoginContract` 中使用的 `reason` 为准（该测试已证实其结构）。

- [ ] **Step 2: 运行**

Run: `GOWORK=off SERVER_TEST_DB_DSN=<一次性库> ACCOUNTSVC_TEST_REDIS_URL=<一次性 Redis> go test . -run 'TestQQHTTP' -count=1 -v`
Expected: 两个测试 PASS（不能是 SKIP）。若失败，先按 superpowers:systematic-debugging 定位，不得放宽断言。

- [ ] **Step 3: 必需测试清单**

在 `scripts/required-tests.txt` 中按现有排序习惯（参考支付宝条目所在位置）加入：

```
github.com/bbxx111/accountkit/internal/accountsvc/TestConfigQQ
github.com/bbxx111/accountkit/observability/internal/metricdata/TestQQMetricProjection
github.com/bbxx111/accountkit/TestQQConfigFromEnv
github.com/bbxx111/accountkit/TestQQProviderAssembly
github.com/bbxx111/accountkit/TestQQHTTPLoginContract
github.com/bbxx111/accountkit/TestQQHTTPMultiAppIdentityContract
github.com/bbxx111/accountkit/enum/TestIdentityQQIsBuiltinThirdParty
github.com/bbxx111/accountkit/user/idp/TestQQConfigValidation
github.com/bbxx111/accountkit/user/idp/TestQQCredentialRejectedBeforeNetwork
github.com/bbxx111/accountkit/user/idp/TestQQVerifyModes
github.com/bbxx111/accountkit/user/idp/TestQQSharedUnionIDAcrossApps
github.com/bbxx111/accountkit/user/idp/TestQQMeClassification
github.com/bbxx111/accountkit/user/idp/TestQQTransportFailures
github.com/bbxx111/accountkit/user/idp/TestQQPolicyFingerprint
github.com/bbxx111/accountkit/user/idp/TestQQProfileMapping
github.com/bbxx111/accountkit/user/idp/TestQQProfileFailuresAllowSignIn
github.com/bbxx111/accountkit/user/idp/TestQQAvatarURL
github.com/bbxx111/accountkit/user/TestQQLaterLoginBackfillsOnlyEmptyFields
github.com/bbxx111/accountkit/user/TestQQSameCredentialTwiceSignsInSameIdentity
github.com/bbxx111/accountkit/user/TestQQPendingAlreadyOwnedProofBackfills
github.com/bbxx111/accountkit/user/TestQQPendingSanitizesAndCompletesWithoutVerifyReplay
github.com/bbxx111/accountkit/user/TestQQLoginProfileRollbackAfterSessionFailure
github.com/bbxx111/accountkit/user/TestQQConcurrentPATCHNonemptyProfileWins
github.com/bbxx111/accountkit/user/TestQQLoginRechecksAccountState
github.com/bbxx111/accountkit/user/TestQQLoginCannotRestoreProfileAfterConcurrentPurge
github.com/bbxx111/accountkit/user/TestQQConcurrentAppsShareSingleUnionIdentity
github.com/bbxx111/accountkit/user/TestQQBindUnbindAndDisabledHistory
```

先看 `scripts/` 下读取该文件的脚本（如 `scripts/verify.sh` 中的 required tests 检查），确认条目格式与排序要求。

- [ ] **Step 4: 接入指南**

创建 `docs/qq-sign-in.md`，结构参照 `docs/alipay-sign-in.md`，包含以下小节与内容：

```markdown
# QQ App SDK 登录

本指南覆盖 Android/iOS 原生 App 通过 QQ 互联移动 SDK 登录，以及 accountkit/accountsvc 的既有第三方登录与绑定接口。库不提供网页授权、回调或授权码兑换。

## 平台准备

在 [QQ 互联](https://connect.qq.com/) 创建移动应用，取得 AppID。跨 App 共用账号时，在同一开发者账号下申请 UnionID 打通（[UnionID 介绍](https://wiki.connect.qq.com/unionid%E4%BB%8B%E7%BB%8D/)），并实测同一用户在各 App 返回相同 unionid。只有一个 App 且未开通 UnionID 的部署应显式配置 `OPENID`。

## 客户端流程

1. 集成 QQ 互联 Android/iOS SDK 并完成登录，取得本次 `access_token`。
2. 仅经 HTTPS 提交统一凭证，不提交 SDK 返回的 openid、unionid 或资料：

{"kind":"QQ","credential":{"app_id":"1100000001","access_token":"synthetic-sdk-access-token"}}

3. 不要记录、缓存或上报 access_token。QQ 令牌有效期内可被重复用于登录（与 Facebook Classic 相同），泄露即等同于登录凭据泄露。

## 错误

`CREDENTIAL_INCOMPLETE`、`IDP_APP_NOT_ALLOWED`（含另一 App 的令牌）、`IDP_CREDENTIAL_INVALID`（令牌失效）为 400；网络、5xx、超时或未知上游错误码为 503 `IDP_UNAVAILABLE`；UNIONID 模式未开通 UnionID 为固定消息 500。

## Go 装配

（给出 `cfg.QQAppIDs = []string{"1100000001"}`、`cfg.QQSubjectMode = accountkit.QQSubjectModeUnionID`、`cfg.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityQQ}` 的片段，说明 `New` 无 I/O、Required 默认仍需手机/邮箱补全。）

## accountsvc 配置

（列出 `ACCOUNTKIT_QQ_APP_IDS`、`ACCOUNTKIT_QQ_SUBJECT_MODE`、`ACCOUNTKIT_QQ_API_BASE_URL` 示例与启动失败行为，以及 Compose 不会自动传入 IdP 变量的提醒，措辞对照 docs/alipay-sign-in.md 对应小节。）

## 身份、资料与头像

UNIONID → `unionid:<unionid>`；OPENID → `openid:<app_id>:<openid>`，不在模式间回退或合并。pending 指纹覆盖排序后的 AppID、模式与接口地址。每次验证先以最多 10 秒调用 `oauth2.0/me`，再以独立最多 3 秒调用 `get_user_info`；资料失败、`ret` 非 0 或 `is_lost=1` 不阻断登录。头像优先 100px `figureurl_qq_2`，缺失时 40px `figureurl_qq_1`；`thirdqq.qlogo.cn` 的 http 地址升级为 https，其他 http 地址丢弃。首次及后续成功登录按字段补填仍为空的显示名和头像，不覆盖非空值。无 SQL/schema 或迁移变化。

## 验收边界

`TestQQHTTPLoginContract` 与 `TestQQHTTPMultiAppIdentityContract` 经正式门面、终端用户 handler、实际 QQ Provider、本地模拟接口和真实 PostgreSQL/Redis 验证契约。它们不证明真实 SDK 行为、UnionID 打通返回或头像主机 HTTPS 可用性。

## 宿主联调清单（未执行）

- [ ] 真机 Android/iOS SDK 登录后提交令牌，后端登录成功
- [ ] 移动 SDK 令牌调用 `/me` 返回的 `client_id` 等于移动应用 AppID
- [ ] 两个已打通 App 对同一用户返回相同 unionid
- [ ] 实际 `figureurl_qq_*` 主机均在名单内，且对应 https 地址可访问
```

括号中的说明是给实施者的写作要求，写成正文时替换为实际内容（照 `docs/alipay-sign-in.md` 对应小节的写法），不保留括号。

- [ ] **Step 5: 完整验证**

```bash
export GOWORK=off
go build ./...
go vet ./...
SERVER_TEST_DB_DSN=<一次性库> ACCOUNTSVC_TEST_REDIS_URL=<一次性 Redis> go test -count=1 ./...
bash scripts/check-generated.sh
openspec validate --all --strict
```

Expected: 全部通过；QQ 数据库与 HTTP 用例不得为 SKIP。若环境具备，继续运行 `bash scripts/verify.sh`（需三个一次性库与 CGO；Windows 缺编译器时用 `tests/Dockerfile` 镜像）；不具备则在验收记录中标为未执行，不得宣称完整验证通过。

- [ ] **Step 6: 验收记录**

创建 `openspec/changes/add-qq-app-sign-in/verification.md`，记录：实际执行的命令与结果（通过/失败/跳过数量）、使用的一次性资源类型（不写连接串）、`scripts/verify.sh` 是否执行、宿主联调清单四项均为“未执行”、Open Questions 现状、Facebook 变更归档顺序风险仍待处理。

- [ ] **Step 7: 提交**

```bash
git add docs/qq-sign-in.md accountkit_qq_http_test.go scripts/required-tests.txt openspec/changes/add-qq-app-sign-in/verification.md openspec/changes/add-qq-app-sign-in/tasks.md
git commit -m "test(qq): verify public http sign-in contracts"
```

勾选 OpenSpec 6.1–6.4。

---

## Self-Review 记录

- 规格覆盖：qq-sign-in 的 14 条需求分别落在 Task 1（配置/环境变量）、Task 2（凭证/令牌校验/重复使用/模式/指纹/错误）、Task 3（资料/头像/降级）、Task 4（领域边界/补填）、Task 6（验收边界）；registry delta 由 Task 1（类型/OIDC 保留）与 Task 4（补填集合）覆盖；user-basic-profile delta 由 Task 4 覆盖；accountsvc-runtime delta 由 Task 5 覆盖；standardized-metrics delta 由 Task 3（降级）与 Task 5（取值/投影）覆盖。
- 与 design 的偏离：design Decision 2 写“在 QQ 文件内实现小型解析”，本计划改为直接调用（不修改）`alipay_json.go` 的 `alipayObject` / `alipayString` 与 `validAlipayIdentifier`，避免重复实现同一严格解析；实施 Task 2 时同步更新 design.md 该句。
- 类型一致性：`QQConfig`/`parsedQQConfig.appIDs`/`QQOptions`/`qqFailure`/`qqMeError`/`qqAvatarURL`/`refillsProfileOnSignIn`/`MethodQQ`/`DependencyVerifyQQ`/`DegradationQQProfileUnavailable` 在各任务中名称一致。
