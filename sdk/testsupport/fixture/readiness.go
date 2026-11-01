package main

import (
	"context"
	"net/http"
	"time"
)

// A silent local listener must not hold startup beyond the probe budget or caller cancellation.
func probeReady(ctx context.Context, origin string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/readyz", nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
