package observability

import (
	"context"
	"log/slog"
	"reflect"
	"sync/atomic"
)

// Recorder protects business callers from observer and diagnostic-handler panics.
// Its first observer panic emits at most one fixed diagnostic for this recorder.
// Sharing the Recorder across components also shares that diagnostic bound.
type Recorder struct {
	observer  Observer
	logger    *slog.Logger
	diagnosed atomic.Bool
}

// NewRecorder normalizes optional nil implementations and reuses an existing
// Recorder, preserving both its identity and its original diagnostic logger.
// A nil logger disables diagnostics. No I/O occurs here.
func NewRecorder(observer Observer, logger *slog.Logger) *Recorder {
	if nilObserver(observer) {
		return &Recorder{}
	}
	if recorder, ok := observer.(*Recorder); ok {
		return recorder
	}
	return &Recorder{observer: observer, logger: logger}
}

func nilObserver(observer Observer) bool {
	if observer == nil {
		return true
	}
	v := reflect.ValueOf(observer)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

// Enabled reports whether recording is configured. A nil Recorder is disabled.
func (r *Recorder) Enabled() bool { return r != nil && r.observer != nil }

// Observe invokes only the observer inside its recovery boundary. It neither
// inspects panic contents nor recovers an already-unwinding business panic.
func (r *Recorder) Observe(ctx context.Context, observation Observation) {
	if !r.Enabled() {
		return
	}
	defer func() {
		if recover() != nil {
			r.diagnose()
		}
	}()
	r.observer.Observe(ctx, observation)
}

func (r *Recorder) diagnose() {
	if r.logger == nil || !r.diagnosed.CompareAndSwap(false, true) {
		return
	}
	// Host handlers can panic too. This nested recovery protects only diagnostics.
	defer func() { _ = recover() }()
	// Avoid handing the host logger request values or a panic's secret contents.
	r.logger.WarnContext(context.Background(), "accountkit observer panic; observation lost")
}
