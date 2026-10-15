// Package audit 提供通用审计契约、认证映射入口和本地记录器。
package audit

import (
	"context"
	"log/slog"
	"sync"

	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/bbxx111/accountkit/audit/event"
)

type Event = event.Event
type Actor = event.Actor
type ActorType = event.ActorType
type Resource = event.Resource
type EventContext = event.EventContext
type RecordView = event.RecordView
type Recorder = event.Recorder
type Inserter = event.Inserter
type BatchResult = event.BatchResult
type EventCursor = event.EventCursor
type Query = event.Query
type Page = event.Page
type FlowLink = event.FlowLink
type Reader = event.Reader
type AuthenticationEvent = authentication.Input
type AuthenticationOptions = authentication.Options
type AuthenticationRecorder = authentication.Recorder

const (
	ActorUser      = event.ActorUser
	ActorService   = event.ActorService
	ActorSystem    = event.ActorSystem
	ActorAnonymous = event.ActorAnonymous
	ActorUnknown   = event.ActorUnknown
)

// NewAuthenticationRecorder 构造认证输入到通用事件的映射器，不做 I/O。
func NewAuthenticationRecorder(r Recorder, o AuthenticationOptions, logger *slog.Logger) (*AuthenticationRecorder, error) {
	return authentication.NewRecorder(r, o, logger)
}

// Noop 丢弃所有事件。
type Noop struct{}

func (Noop) Record(context.Context, Event) {}

// Hint 返回已脱敏摘要的前八字节。
func Hint(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// Memory 保存通过校验的独立事件副本，适用于测试。
type Memory struct {
	mu     sync.Mutex
	events []Event
}

func (m *Memory) Record(_ context.Context, e Event) {
	frozen, err := event.Freeze(e)
	if err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, frozen)
}

// Events 返回深拷贝；修改返回内容不会改变已记录事件。
func (m *Memory) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.events))
	for i, e := range m.events {
		out[i], _ = event.Freeze(e)
	}
	return out
}
