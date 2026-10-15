package event

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bbxx111/accountkit/enum"
)

func validEvent() Event {
	return Event{ID: uuid.MustParse("019a3350-1234-7abc-8def-0123456789ab"), Source: "shop/production", Action: "ORDER_APPROVED", Actor: Actor{Type: ActorUser, Namespace: "users", ID: "user-1"}, OccurTime: time.Date(2026, 10, 16, 1, 2, 3, 123456789, time.UTC)}
}

func resource() Resource { return Resource{Type: "ORDER", Namespace: "shop", ID: "order-1"} }

// Rejecting invalid identities and oversized payloads prevents silent corruption at ingestion.
func TestEventValidation(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(*Event)
		valid bool
	}{
		{"minimal", func(*Event) {}, true},
		{"nil UUID", func(e *Event) { e.ID = uuid.Nil }, false},
		{"v4", func(e *Event) { e.ID[6] = 0x4a }, false},
		{"non RFC variant", func(e *Event) { e.ID[8] = 0x01 }, false},
		{"source 128", func(e *Event) { e.Source = strings.Repeat("a", 128) }, true},
		{"source 129", func(e *Event) { e.Source = strings.Repeat("a", 129) }, false},
		{"source empty", func(e *Event) { e.Source = "" }, false},
		{"source unicode", func(e *Event) { e.Source = "订单" }, false},
		{"source punctuation", func(e *Event) { e.Source = "/shop" }, false},
		{"action 128", func(e *Event) { e.Action = strings.Repeat("A", 128) }, true},
		{"action 129", func(e *Event) { e.Action = strings.Repeat("A", 129) }, false},
		{"action lowercase", func(e *Event) { e.Action = "approve" }, false},
		{"action empty", func(e *Event) { e.Action = "" }, false},
		{"actor missing namespace", func(e *Event) { e.Actor.Namespace = "" }, false},
		{"actor missing id", func(e *Event) { e.Actor.ID = "" }, false},
		{"actor unknown type", func(e *Event) { e.Actor.Type = "ADMIN" }, false},
		{"anonymous", func(e *Event) { e.Actor = Actor{Type: ActorAnonymous} }, true},
		{"unknown", func(e *Event) { e.Actor = Actor{Type: ActorUnknown} }, true},
		{"anonymous identity", func(e *Event) { e.Actor.Type = ActorAnonymous }, false},
		{"namespace 512", func(e *Event) { e.Actor.Namespace = strings.Repeat("界", 170) + "ab" }, true},
		{"namespace 513", func(e *Event) { e.Actor.Namespace = strings.Repeat("界", 171) }, false},
		{"id 513", func(e *Event) { e.Actor.ID = strings.Repeat("a", 513) }, false},
		{"name 256 runes", func(e *Event) { e.Actor.Name = strings.Repeat("界", 256) }, true},
		{"name 257 runes", func(e *Event) { e.Actor.Name = strings.Repeat("界", 257) }, false},
		{"invalid UTF8", func(e *Event) { e.Actor.Name = string([]byte{0xff}) }, false},
		{"nul identity", func(e *Event) { e.Actor.ID = "one\x00two" }, false},
		{"targets 32", func(e *Event) { e.Targets = makeResources(32) }, true},
		{"targets 33 including repeats", func(e *Event) { e.Targets = makeResources(33) }, false},
		{"related 32", func(e *Event) { e.Context.RelatedResources = makeResources(32) }, true},
		{"related 33", func(e *Event) { e.Context.RelatedResources = makeResources(33) }, false},
		{"resource missing identity", func(e *Event) { e.Targets = []Resource{{Type: "ORDER"}} }, false},
		{"resource type 128", func(e *Event) { r := resource(); r.Type = strings.Repeat("A", 128); e.Targets = []Resource{r} }, true},
		{"resource type 129", func(e *Event) { r := resource(); r.Type = strings.Repeat("A", 129); e.Targets = []Resource{r} }, false},
		{"resource name 257", func(e *Event) { r := resource(); r.Name = strings.Repeat("界", 257); e.Targets = []Resource{r} }, false},
		{"result success", func(e *Event) { r := enum.ResultSuccess; e.Result = &r }, true},
		{"result unspecified", func(e *Event) { r := enum.ResultUnspecified; e.Result = &r }, false},
		{"result unsupported", func(e *Event) { r := enum.Result(3); e.Result = &r }, false},
		{"no occur time", func(e *Event) { e.OccurTime = time.Time{} }, false},
		{"data array", func(e *Event) { e.Data = json.RawMessage(`[]`) }, false},
		{"data null", func(e *Event) { e.Data = json.RawMessage(`null`) }, false},
		{"data duplicate keys", func(e *Event) { e.Data = json.RawMessage(`{"a":1,"a":2}`) }, false},
		{"data illegal number", func(e *Event) { e.Data = json.RawMessage(`{"a":NaN}`) }, false},
		{"data precise number", func(e *Event) { e.Data = json.RawMessage(`{"a":9007199254740993}`) }, true},
		{"data unpaired high surrogate", func(e *Event) { e.Data = json.RawMessage(`{"a":"\ud800"}`) }, false},
		{"data unpaired low surrogate", func(e *Event) { e.Data = json.RawMessage(`{"a":"\udc00"}`) }, false},
		{"data surrogate pair", func(e *Event) { e.Data = json.RawMessage(`{"a":"\ud83d\ude00"}`) }, true},
		{"data literal surrogate escape", func(e *Event) { e.Data = json.RawMessage(`{"a":"\\ud800"}`) }, true},
		{"data numeric overflow", func(e *Event) { e.Data = json.RawMessage(`{"a":1e131072}`) }, false},
		{"data numeric scale overflow", func(e *Event) { e.Data = json.RawMessage(`{"a":1e-16384}`) }, false},
		{"data decimal expansion exceeds event", func(e *Event) { e.Data = json.RawMessage(`{"a":1e100000}`) }, false},
		{"data massive exponent", func(e *Event) { e.Data = json.RawMessage(`{"a":1e2147483647}`) }, false},
		{"request id 128", func(e *Event) { e.Context.RequestID = strings.Repeat("a", 128) }, true},
		{"request id 129", func(e *Event) { e.Context.RequestID = strings.Repeat("a", 129) }, false},
		{"valid trace", func(e *Event) {
			e.Context.TraceID = "0123456789abcdef0123456789abcdef"
			e.Context.SpanID = "0123456789abcdef"
		}, true},
		{"zero trace", func(e *Event) { e.Context.TraceID = strings.Repeat("0", 32) }, false},
		{"uppercase trace", func(e *Event) { e.Context.TraceID = "0123456789ABCDEF0123456789ABCDEF" }, false},
		{"span without trace", func(e *Event) { e.Context.SpanID = "0123456789abcdef" }, false},
		{"valid IPv6", func(e *Event) { e.Context.ClientIP = "2001:db8::1" }, true},
		{"bad IP", func(e *Event) { e.Context.ClientIP = "forged" }, false},
		{"scoped IP", func(e *Event) { e.Context.ClientIP = "fe80::1%eth0" }, false},
		{"user agent 2049", func(e *Event) { e.Context.UserAgent = strings.Repeat("a", 2049) }, false},
		{"device id 257", func(e *Event) { e.Context.DeviceID = strings.Repeat("a", 257) }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := validEvent()
			tt.edit(&e)
			if err := Validate(e); (err == nil) != tt.valid {
				t.Fatalf("Validate() = %v, valid = %v", err, tt.valid)
			}
		})
	}
	t.Run("complete JSON size", func(t *testing.T) {
		e := validEvent()
		e.Data = json.RawMessage(`{"padding":""}`)
		f, err := Freeze(e)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		e.Data = json.RawMessage(`{"padding":"` + strings.Repeat("x", 65536-len(wire)) + `"}`)
		if err := Validate(e); err != nil {
			t.Fatalf("64 KiB boundary: %v", err)
		}
		e.Data = append(e.Data[:len(e.Data)-2], 'x', '"', '}')
		if err := Validate(e); err == nil {
			t.Fatal("accepted 64 KiB + 1")
		}
	})
}

func TestEventFreezeExactDecimalNumbers(t *testing.T) {
	e := validEvent()
	e.Data = json.RawMessage(`{"positive":1.2345678901234567890123456789e5,"negative":-123456789.01234567890123456789,"small":1e-20,"zero":-0.00}`)
	frozen, err := Freeze(e)
	if err != nil {
		t.Fatal(err)
	}
	if string(frozen.Data) != `{"negative":-123456789.01234567890123456789,"positive":123456.78901234567890123456789,"small":0.00000000000000000001,"zero":0}` {
		t.Fatalf("lost exact decimal value: %s", frozen.Data)
	}
	wire, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeEvent(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !Equivalent(frozen, decoded) {
		t.Fatal("normalized decimal round-trip changed event")
	}
}

func makeResources(n int) []Resource {
	r := make([]Resource, n)
	for i := range r {
		r[i] = resource()
	}
	return r
}

func TestEventFreezeOwnership(t *testing.T) {
	e := validEvent()
	result := enum.ResultSuccess
	e.Result = &result
	e.Data = json.RawMessage(`{"nested":{"n":9007199254740993}}`)
	e.Targets = []Resource{resource()}
	e.Context.RelatedResources = []Resource{resource()}
	frozen, err := Freeze(e)
	if err != nil {
		t.Fatal(err)
	}
	e.Data[2] = 'X'
	e.Targets[0].ID = "changed"
	e.Context.RelatedResources[0].ID = "changed"
	result = enum.ResultFailure
	if string(frozen.Data) != `{"nested":{"n":9007199254740993}}` || frozen.Targets[0].ID != "order-1" || frozen.Context.RelatedResources[0].ID != "order-1" || *frozen.Result != enum.ResultSuccess {
		t.Fatal("caller mutation changed frozen event")
	}
	if frozen.OccurTime.Nanosecond() != 123456000 || frozen.OccurTime.Location() != time.UTC {
		t.Fatalf("not normalized to UTC microseconds: %v", frozen.OccurTime)
	}
	minimal, err := Freeze(validEvent())
	if err != nil {
		t.Fatal(err)
	}
	wire, _ := json.Marshal(minimal)
	if !bytes.Contains(wire, []byte(`"targets":[]`)) || !bytes.Contains(wire, []byte(`"context":{}`)) || !bytes.Contains(wire, []byte(`"data":{}`)) {
		t.Fatalf("empty structures: %s", wire)
	}
}

func TestDecodeEventDuplicateKeys(t *testing.T) {
	wire, _ := json.Marshal(validEvent())
	for _, data := range []string{
		strings.Replace(string(wire), `"source":`, `"source":"first","source":`, 1),
		strings.Replace(string(wire), `"actor":{`, `"actor":{"type":"UNKNOWN",`, 1),
		strings.Replace(string(wire), `"data":{}`, `"data":{"nested":{"a":1,"\u0061":2}}`, 1),
		strings.Replace(string(wire), `"data":{}`, `"data":{"nested":[{"a":1,"a":2}]}`, 1),
		string(wire) + `{}`,
	} {
		if _, err := DecodeEvent([]byte(data)); err == nil {
			t.Fatalf("accepted duplicate keys or trailing JSON: %s", data)
		}
	}
	if _, err := DecodeEvent(wire); err != nil {
		t.Fatalf("decode minimal: %v", err)
	}
	if _, err := DecodeEvent([]byte(`[]`)); err == nil {
		t.Fatal("accepted array event")
	}
}

func TestEventEquivalentPrecisionAndResourceOrder(t *testing.T) {
	a := validEvent()
	a.Data = json.RawMessage(`{"a":9007199254740993,"b":1.00,"c":null}`)
	x := resource()
	y := x
	y.ID = "order-2"
	a.Targets = []Resource{x, y, x}
	b := a
	b.OccurTime = a.OccurTime.UTC().Truncate(time.Microsecond)
	b.Targets = []Resource{y, x}
	b.Data = json.RawMessage(`{"c":null,"b":1e0,"a":9007199254740993}`)
	if !Equivalent(a, b) {
		t.Fatal("resource ordering, repeated complete refs, JSON ordering and microsecond precision must not conflict")
	}
	for _, edit := range []func(*Event){
		func(e *Event) { e.Data = json.RawMessage(`{"a":9007199254740992,"b":1,"c":null}`) },
		func(e *Event) { e.Data = json.RawMessage(`{"a":9007199254740993,"b":1}`) },
		func(e *Event) { e.Source = "other" },
		func(e *Event) { e.Action = "ORDER_REJECTED" },
		func(e *Event) { e.Actor.ID = "other" },
		func(e *Event) { e.OccurTime = e.OccurTime.Add(time.Microsecond) },
		func(e *Event) { e.Targets = []Resource{{Type: "ORDER", Namespace: "shop", ID: "other"}} },
	} {
		c := b
		edit(&c)
		if Equivalent(a, c) {
			t.Fatal("distinct stable content considered equivalent")
		}
	}
}

func TestEventScrubControlledProjection(t *testing.T) {
	e := validEvent()
	e.Actor.Name = "display"
	r := resource()
	r.Name = "display"
	e.Targets = []Resource{r}
	e.Context = EventContext{RequestID: "req", TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef", ClientIP: "192.0.2.1", DeviceID: "device", UserAgent: "agent", RelatedResources: []Resource{r}}
	e.Data = json.RawMessage(`{"reason":"DENIED","accountkit":{"subject_hint":"abcdefgh","flow_id":"f_test","identity_kind":1,"legacy_event_id":"e_old"},"business":{"value":42}}`)
	s := Scrub(e)
	if s.Actor.Name != "" || s.Targets[0].Name != "" || s.Context.RelatedResources[0].Name != "" || s.Context.ClientIP != "" || s.Context.DeviceID != "" || s.Context.UserAgent != "" {
		t.Fatal("privacy fields retained")
	}
	if s.Context.RequestID != "req" || s.Context.TraceID != "0123456789abcdef0123456789abcdef" || s.Context.SpanID != "0123456789abcdef" {
		t.Fatal("scrub removed non-privacy correlation")
	}
	if s.Actor.ID != "user-1" || s.Targets[0].ID != "order-1" || s.Context.RelatedResources[0].ID != "order-1" {
		t.Fatal("scrub removed stable identities")
	}
	if !bytes.Equal(s.Data, e.Data) {
		t.Fatalf("generic scrub changed business data: %s", s.Data)
	}
	if !Equivalent(s, Scrub(e)) || Equivalent(s, e) {
		t.Fatal("strict equivalence must require explicit scrub projection")
	}
	if e.Actor.Name != "display" || e.Targets[0].Name != "display" || !bytes.Contains(e.Data, []byte("subject_hint")) {
		t.Fatal("scrub mutated caller event")
	}
}

func TestEventJSONResultAndRecordTime(t *testing.T) {
	e := validEvent()
	r := enum.ResultSuccess
	e.Result = &r
	wire, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(wire, []byte(`"result":"SUCCESS"`)) {
		t.Fatalf("result must use API enum: %s", wire)
	}
	decoded, err := DecodeEvent(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !Equivalent(e, decoded) {
		t.Fatal("JSON round trip changed event")
	}
	for _, bad := range []string{`0`, `1`, `"UNSPECIFIED"`, `"unknown"`} {
		invalid := bytes.Replace(wire, []byte(`"SUCCESS"`), []byte(bad), 1)
		if _, err := DecodeEvent(invalid); err == nil {
			t.Fatalf("accepted result %s", bad)
		}
	}
	record := RecordView{Event: e, CreateTime: time.Date(2026, 10, 16, 2, 3, 4, 0, time.UTC)}
	recordWire, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(recordWire, []byte(`"create_time":"2026-10-16T02:03:04Z"`)) {
		t.Fatalf("record lost create_time: %s", recordWire)
	}
	var restored RecordView
	if err := json.Unmarshal(recordWire, &restored); err != nil {
		t.Fatal(err)
	}
	if !Equivalent(record.Event, restored.Event) || !record.CreateTime.Equal(restored.CreateTime) {
		t.Fatal("record JSON round trip changed stored fields")
	}
	if _, err := DecodeEvent(recordWire); err == nil {
		t.Fatal("producer accepted storage create_time")
	}
}

func TestRecordViewMaximumEventRoundTrip(t *testing.T) {
	e := validEvent()
	e.Data = json.RawMessage(`{"padding":""}`)
	frozen, err := Freeze(e)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	e.Data = json.RawMessage(`{"padding":"` + strings.Repeat("x", 65536-len(wire)) + `"}`)
	frozen, err = Freeze(e)
	if err != nil {
		t.Fatal(err)
	}
	r := RecordView{Event: frozen, CreateTime: time.Date(2026, 10, 16, 2, 3, 4, 0, time.UTC)}
	stored, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var restored RecordView
	if err := json.Unmarshal(stored, &restored); err != nil {
		t.Fatalf("storage time must not consume producer event allowance: %v", err)
	}
	if !Equivalent(r.Event, restored.Event) || !r.CreateTime.Equal(restored.CreateTime) {
		t.Fatal("maximum event round-trip changed record")
	}
}
