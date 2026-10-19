// Command metricsotel records synthetic observations, collects locally once, and exits.
// It does not execute authentication or connect to a monitoring service.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"time"

	"github.com/bbxx111/accountkit/observability"
	akotel "github.com/bbxx111/accountkit/observability/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, output io.Writer) error {
	reader := sdkmetric.NewManualReader()
	// Default SDK aggregation honors the adapter's explicit bucket advice.
	// A host View can override those boundaries.
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer provider.Shutdown(context.Background()) // This command owns the provider.
	observer, err := akotel.New(provider, "example")
	if err != nil {
		return err
	}
	for _, duration := range []time.Duration{125 * time.Millisecond, 375 * time.Millisecond} {
		observer.Observe(ctx, observability.AuthObservation{
			Operation: observability.AuthAuthenticate,
			Result:    observability.ResultSuccess, Grant: observability.GrantUser,
			Duration: duration,
		})
	}
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &collected); err != nil {
		return err
	}
	var lines []string
	for _, scope := range collected.ScopeMetrics {
		for _, metric := range scope.Metrics {
			switch data := metric.Data.(type) {
			case metricdata.Sum[int64]:
				for _, point := range data.DataPoints {
					lines = append(lines, fmt.Sprintf("%s %s count=%d", metric.Name, labels(point.Attributes), point.Value))
				}
			case metricdata.Histogram[float64]:
				for _, point := range data.DataPoints {
					lines = append(lines, fmt.Sprintf("%s %s count=%d sum_seconds=%.3f", metric.Name, labels(point.Attributes), point.Count, point.Sum))
				}
			}
		}
	}
	sort.Strings(lines)
	if _, err := fmt.Fprintln(output, "synthetic observations; no authentication performed"); err != nil {
		return err
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(output, line); err != nil {
			return err
		}
	}
	return nil
}

func labels(attributes attribute.Set) string {
	instance, _ := attributes.Value("accountkit_instance")
	operation, _ := attributes.Value("operation")
	result, _ := attributes.Value("result")
	return fmt.Sprintf("instance=%s operation=%s result=%s", instance.AsString(), operation.AsString(), result.AsString())
}
