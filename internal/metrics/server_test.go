package metrics

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestMetricsHandlerExposesCommandCounters(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	recorder := httptest.NewRecorder()
	promhttp.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /metrics status = %d", recorder.Code)
	}
	body := recorder.Body.String()
	for _, metric := range []string{
		"orbit_commands_submitted_total",
		"orbit_commands_admission_rejected_total",
		"orbit_commands_leased_total",
		"orbit_commands_acknowledged_total",
	} {
		if !strings.Contains(body, metric) {
			t.Fatalf("GET /metrics body missing %s", metric)
		}
	}
}

func TestServeEmptyAddressBlocksUntilContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, "", slog.Default(), "test")
	}()

	select {
	case err := <-done:
		t.Fatalf("Serve returned early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve after cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return after context cancel")
	}
}
