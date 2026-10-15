package event

import (
	"encoding/json"
	"time"

	"github.com/bbxx111/accountkit/enum"
)

// eventFields 避免 Event 的 JSON 方法在嵌入 RecordView 时提升并遗漏 CreateTime。
type eventFields Event

type eventWire struct {
	*eventFields
	Result     *string    `json:"result"`
	CreateTime *time.Time `json:"create_time,omitempty"`
}

// MarshalJSON 使用公开枚举名称及非 null 的集合/对象。
func (e Event) MarshalJSON() ([]byte, error) { return marshalEvent(e, nil) }

func marshalEvent(e Event, createTime *time.Time) ([]byte, error) {
	if e.Targets == nil {
		e.Targets = []Resource{}
	}
	if len(e.Data) == 0 {
		e.Data = json.RawMessage(`{}`)
	}
	var result *string
	if e.Result != nil {
		if !e.Result.Valid() {
			return nil, invalid("result")
		}
		name := e.Result.String()
		result = &name
	}
	fields := eventFields(e)
	return json.Marshal(eventWire{eventFields: &fields, Result: result, CreateTime: createTime})
}

// UnmarshalJSON 与 DecodeEvent 一样拒绝重复键和非法事件。
func (e *Event) UnmarshalJSON(wire []byte) error {
	decoded, err := DecodeEvent(wire)
	if err != nil {
		return err
	}
	*e = decoded
	return nil
}

func unmarshalEvent(wire []byte, e *Event) error {
	var fields eventFields
	v := struct {
		*eventFields
		Result json.RawMessage `json:"result"`
	}{eventFields: &fields}
	if err := json.Unmarshal(wire, &v); err != nil {
		return invalid("event")
	}
	if len(v.Result) != 0 && string(v.Result) != "null" {
		var name string
		if err := json.Unmarshal(v.Result, &name); err != nil {
			return invalid("result")
		}
		var result enum.Result
		switch name {
		case "SUCCESS":
			result = enum.ResultSuccess
		case "FAILURE":
			result = enum.ResultFailure
		default:
			return invalid("result")
		}
		fields.Result = &result
	}
	*e = Event(fields)
	return nil
}

// MarshalJSON 保留存储生成的 CreateTime，不依赖嵌入 Event 的方法提升。
func (r RecordView) MarshalJSON() ([]byte, error) { return marshalEvent(r.Event, &r.CreateTime) }

// UnmarshalJSON 解码通用存储视图，生产侧 DecodeEvent 仍拒绝 create_time。
func (r *RecordView) UnmarshalJSON(wire []byte) error {
	// 64 KiB 是生产事件限额；存储附加时间戳后仍须能回读。
	// 时间戳字段的 JSON 开销小于 64 字节，移除后 DecodeEvent 再核验事件限额。
	v, err := parseJSONLimit(wire, MaxEventBytes+64)
	if err != nil {
		return invalid("record")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return invalid("record")
	}
	stamp, ok := obj["create_time"].(string)
	if !ok {
		return invalid("create_time")
	}
	created, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || created.IsZero() {
		return invalid("create_time")
	}
	delete(obj, "create_time")
	producerWire, err := json.Marshal(obj)
	if err != nil {
		return invalid("record")
	}
	e, err := DecodeEvent(producerWire)
	if err != nil {
		return err
	}
	*r = RecordView{Event: e, CreateTime: created.UTC().Truncate(time.Microsecond)}
	return nil
}
