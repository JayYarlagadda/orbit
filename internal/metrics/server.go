package metrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func Serve(ctx context.Context, address string, logger *slog.Logger, component string) error {
	if address == "" {
		<-ctx.Done()
		return nil
	}
	if component == "" {
		component = "orbit"
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	server := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErrors := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrors <- err
		}
		close(serveErrors)
	}()
	logger.Info("metrics listening", "component", component, "address", address)

	select {
	case err := <-serveErrors:
		if err != nil {
			return fmt.Errorf("metrics server: %w", err)
		}
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown metrics server: %w", err)
	}
	if err := <-serveErrors; err != nil {
		return fmt.Errorf("metrics server: %w", err)
	}
	return nil
}
