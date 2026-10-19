package observability_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/observability"
	akotel "github.com/bbxx111/accountkit/observability/otel"
	akprom "github.com/bbxx111/accountkit/observability/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// BenchmarkMetricRecording measures the same public recording boundary used by
// services. One auth observation updates a counter and a duration histogram.
// Construction, collection, shutdown, and export are outside the measured work.
func BenchmarkMetricRecording(b *testing.B) {
	for _, backend := range []string{"Disabled", "OTel", "Prometheus"} {
		for _, parallel := range []bool{false, true} {
			name := backend + "/Serial"
			if parallel {
				name = backend + "/Parallel"
			}
			b.Run(name, func(b *testing.B) {
				ctx := context.Background()
				var observer observability.Observer
				switch backend {
				case "OTel":
					provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewManualReader()))
					b.Cleanup(func() { _ = provider.Shutdown(ctx) })
					var err error
					observer, err = akotel.New(provider, "benchmark")
					if err != nil {
						b.Fatal(err)
					}
				case "Prometheus":
					var err error
					observer, err = akprom.New(prometheus.NewRegistry(), "benchmark")
					if err != nil {
						b.Fatal(err)
					}
				}
				recorder := observability.NewRecorder(observer, nil)
				event := observability.AuthObservation{
					Operation: observability.AuthAuthenticate, Result: observability.ResultSuccess,
					Grant: observability.GrantUser, Duration: time.Millisecond,
				}
				record := func() {
					if recorder.Enabled() {
						recorder.Observe(ctx, event)
					}
				}
				record() // Warm up the fixed label series before measuring.
				before := runtime.NumGoroutine()
				b.ReportAllocs()
				b.ResetTimer()
				if parallel {
					b.RunParallel(func(pb *testing.PB) {
						for pb.Next() {
							record()
						}
					})
				} else {
					for range b.N {
						record()
					}
				}
				b.StopTimer()
				b.ReportMetric(float64(runtime.NumGoroutine()-before), "goroutines_delta")
			})
		}
	}
}
