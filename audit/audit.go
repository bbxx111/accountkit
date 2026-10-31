// Package audit 提供通用审计契约、认证映射入口和本地记录器。
package audit

import (
	"context"
	"log/slog"
	"sync"

	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/bbxx111/accountkit/audit/event"
)

// Event 是 event.Event 的别名：生产方投递的通用审计事件。
type Event = event.Event

// Actor 是 event.Actor 的别名：事件操作者。
type Actor = event.Actor

// ActorType 是 event.ActorType 的别名：操作者分类。
type ActorType = event.ActorType

// Resource 是 event.Resource 的别名：以 Namespace + Type + ID 标识的资源引用。
type Resource = event.Resource

// EventContext 是 event.EventContext 的别名：受控的请求与追踪上下文。
type EventContext = event.EventContext

// RecordView 是 event.RecordView 的别名：附带存储写入时间的已存记录。
type RecordView = event.RecordView

// Recorder 是 event.Recorder 的别名：审计写侧契约，记录失败不得改变业务结果。
type Recorder = event.Recorder

// Inserter 是 event.Inserter 的别名：批量写入冻结事件的存储契约。
type Inserter = event.Inserter

// BatchResult 是 event.BatchResult 的别名：一次批量写入的分类计数。
type BatchResult = event.BatchResult

// EventCursor 是 event.EventCursor 的别名：按 occur_time、id 的分页游标。
type EventCursor = event.EventCursor

// Query 是 event.Query 的别名：按来源与资源范围查询审计记录的条件。
type Query = event.Query

// Page 是 event.Page 的别名：一页记录及独立的流程归属证据。
type Page = event.Page

// FlowLink 是 event.FlowLink 的别名：流程与账号的归属证据。
type FlowLink = event.FlowLink

// Reader 是 event.Reader 的别名：审计读侧契约。
type Reader = event.Reader

// AuthenticationEvent 是 authentication.Input 的别名：已脱敏的认证领域事件输入。
type AuthenticationEvent = authentication.Input

// AuthenticationOptions 是 authentication.Options 的别名：认证映射使用的来源与资源命名空间。
type AuthenticationOptions = authentication.Options

// AuthenticationRecorder 是 authentication.Recorder 的别名：把认证输入映射为通用事件后转交下游 Recorder。
type AuthenticationRecorder = authentication.Recorder

// 操作者分类常量，与 event 包中的同名常量相同。
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

// Record 实现 Recorder：直接丢弃事件。
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

// Record 实现 Recorder：保存通过 event.Freeze 校验的副本，非法事件被静默丢弃。
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
