// Package event 定义与认证领域无关的审计事件、写入和查询契约。
package event

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/bbxx111/accountkit/enum"
)

// ActorType 是操作者分类；角色不属于操作者稳定身份。
type ActorType string

const (
	ActorUser      ActorType = "USER"
	ActorService   ActorType = "SERVICE"
	ActorSystem    ActorType = "SYSTEM"
	ActorAnonymous ActorType = "ANONYMOUS"
	ActorUnknown   ActorType = "UNKNOWN"
)

// Actor 使用 Namespace + ID 表示稳定身份，Name 仅是可清理的展示快照。
type Actor struct {
	Type      ActorType `json:"type"`
	Namespace string    `json:"namespace,omitempty"`
	ID        string    `json:"id,omitempty"`
	Name      string    `json:"name,omitempty"`
}

// Resource 的稳定身份是 Namespace + Type + ID。
type Resource struct {
	Type      string `json:"type"`
	Namespace string `json:"namespace"`
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
}

// EventContext 只包含受控上下文字段，不承载原始请求或凭证。
type EventContext struct {
	RequestID        string     `json:"request_id,omitempty"`
	TraceID          string     `json:"trace_id,omitempty"`
	SpanID           string     `json:"span_id,omitempty"`
	ClientIP         string     `json:"client_ip,omitempty"`
	UserAgent        string     `json:"user_agent,omitempty"`
	DeviceID         string     `json:"device_id,omitempty"`
	RelatedResources []Resource `json:"related_resources,omitempty"`
}

// Event 是生产方在首次投递前生成身份和发生时间的事件。
// 投递前调用 Freeze；Result 的 nil 表示未知或不适用。
type Event struct {
	ID        uuid.UUID       `json:"id"`
	Source    string          `json:"source"`
	Action    string          `json:"action"`
	Result    *enum.Result    `json:"result"`
	Actor     Actor           `json:"actor"`
	Targets   []Resource      `json:"targets"`
	Context   EventContext    `json:"context"`
	Data      json.RawMessage `json:"data"`
	OccurTime time.Time       `json:"occur_time"`
}

// RecordView 附带由存储首次写入时产生的 CreateTime。
type RecordView struct {
	Event
	CreateTime time.Time `json:"create_time"`
}

// Recorder 的记录失败不得改变业务动作结果；组件生命周期由创建方管理。
type Recorder interface{ Record(context.Context, Event) }

// BatchResult 区分首次写入、重复、基础设施失败、校验失败和身份冲突。
// 各分类互斥；重复不计为新的 Written，冲突不得覆盖既有记录。
type BatchResult struct{ Written, Duplicate, Failed, Invalid, Conflicts int }

// Inserter 消费冻结事件；错误不得直接作为可公开日志或响应文本。
type Inserter interface {
	InsertBatch(context.Context, []Event) (BatchResult, error)
}

// EventCursor 使用实际发生时间和唯一身份消除排序并列。
type EventCursor struct {
	Time time.Time
	ID   uuid.UUID
}

// Query 指定来源和完整资源范围；调用方仍负责认证与授权。
type Query struct {
	Source   string
	Resource Resource
	// Actions is optional: empty means all actions, including business events.
	// Nonempty sets are validated, sorted and deduplicated before ORDER/LIMIT.
	Actions []string
	After   *EventCursor
	Limit   int
}

// FlowLink 是范围复核用的归属证据，不修改原始事件资源引用。
type FlowLink struct {
	Flow     Resource
	User     Resource
	Evidence RecordView
	// Live requires a trusted domain ownership check; Evidence remains empty.
	Live bool
}

// Page 将记录和归属证据分开，证据不得作为任意业务数据输出。
type Page struct {
	Records   []RecordView
	FlowLinks []FlowLink
}

// Reader 返回按 occur_time、id 排序且匹配查询范围的记录。
// Actions 必须在分页 Limit 前过滤；FlowLinks 的归属证据独立于记录动作筛选。
type Reader interface {
	ListByResource(context.Context, Query) (Page, error)
}
