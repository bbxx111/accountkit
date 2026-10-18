// Command metricsprometheus records synthetic observations, gathers once, and exits.
// It does not start an HTTP listener or execute authentication.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/bbxx111/accountkit/observability"
	akprom "github.com/bbxx111/accountkit/observability/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, output io.Writer) error {
	registry := prometheus.NewRegistry()
	observer, err := akprom.New(registry, "example")
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
	families, err := registry.Gather()
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(output, "# synthetic observations; no authentication performed"); err != nil {
		return err
	}
	for _, family := range families {
		if _, err := expfmt.MetricFamilyToText(output, family); err != nil {
			return err
		}
	}
	return nil
}
