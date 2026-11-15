// Package resourcename 构造 HTTP 资源名称；ID 格式与归属继续由调用方校验。
package resourcename

// User 返回账号资源名。
func User(id string) string { return "users/" + id }

// Identity 返回账号下的身份资源名。
func Identity(userID, id string) string { return User(userID) + "/identities/" + id }

// Session 返回账号下的会话资源名。
func Session(userID, id string) string { return User(userID) + "/sessions/" + id }

// Profile 返回账号的资料资源名。
func Profile(userID string) string { return User(userID) + "/profile" }

// AuditEvent 返回账号下的审计事件资源名。
func AuditEvent(userID, id string) string { return User(userID) + "/auditEvents/" + id }
