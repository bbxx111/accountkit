// Package audit 定义认证审计事件与记录器接口。
//
// 本阶段只提供接口、Noop 与测试用 Memory；写入 audit_event 表的实现（异步、失败不影响
// 业务结果、按保留期清理）在阶段 5。凭证（验证码、refresh token、IdP code/id_token/nonce）
// 永不进入 Event；身份只以 kind + digest 前 8 位出现。
package audit

import (
	"context"
	"sync"
	"time"

	"github.com/bbxx111/accountkit/enum"
)

// Event 是一条审计事件。
type Event struct {
	Type         enum.EventType
	Actor        enum.ActorKind
	Result       enum.Result
	Reason       string
	UserID       string
	SessionID    string
	IdentityKind enum.IdentityKind
	SubjectHint  string
	IP           string
	DeviceID     string
	RequestID    string

	AdminIssuer   string
	AdminSubject  string
	AdminUsername string

	OccurTime time.Time
}

// Recorder 记录审计事件。实现不得让记录失败改变调用方的业务结果。
type Recorder interface {
	Record(ctx context.Context, e Event)
}

// Noop 丢弃所有事件。
type Noop struct{}

// Record 实现 Recorder。
func (Noop) Record(context.Context, Event) {}

// Hint 取 digest 或 provider_subject 的前 8 个字符作为脱敏提示。
func Hint(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}

// Memory 是测试用记录器。
type Memory struct {
	mu     sync.Mutex
	events []Event
}

// Record 实现 Recorder。
func (m *Memory) Record(_ context.Context, e Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
}

// Events 返回已记录事件的拷贝。
func (m *Memory) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Event(nil), m.events...)
}
