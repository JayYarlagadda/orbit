package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"github.com/JayYarlagadda/orbit/internal/api/commandservice"
	"github.com/JayYarlagadda/orbit/internal/api/gatewaycontrol"
	"github.com/JayYarlagadda/orbit/internal/command"
	"github.com/JayYarlagadda/orbit/internal/config"
	"github.com/JayYarlagadda/orbit/internal/heartbeat"
	"github.com/JayYarlagadda/orbit/internal/metrics"
	"github.com/JayYarlagadda/orbit/internal/shutdownsignal"
	"github.com/JayYarlagadda/orbit/internal/storage/postgres"
	"github.com/JayYarlagadda/orbit/internal/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

const maxGRPCMessageBytes = 70 * 1024

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("orbitd stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	settings, err := config.LoadOrbitd(os.LookupEnv)
	if err != nil {
		return err
	}
	telemetrySettings := config.LoadTelemetry(os.LookupEnv, "orbitd")

	rootContext, stopSignals := shutdownsignal.NotifyContext(context.Background())
	defer stopSignals()

	telemetryProvider, err := telemetry.Init(rootContext, telemetry.Config{
		ServiceName:  telemetrySettings.ServiceName,
		OTLPEndpoint: telemetrySettings.OTLPEndpoint,
		Enabled:      telemetrySettings.Enabled,
	})
	if err != nil {
		return err
	}
	defer func() { _ = telemetryProvider.Shutdown(rootContext) }()

	pool, err := postgres.Open(rootContext, settings.DatabaseURL, settings.DBMaxConnections)
	if err != nil {
		return err
	}
	defer pool.Close()

	listener, err := net.Listen("tcp", settings.ListenAddress)
	if err != nil {
		return err
	}
	defer listener.Close()

	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(maxGRPCMessageBytes),
		grpc.MaxSendMsgSize(maxGRPCMessageBytes),
	)
	store := postgres.NewCommandStore(pool, nil, postgres.StorePolicy{
		Retry: command.RetryPolicy{
			MaxAttempts: settings.MaxDeliveryAttempts,
			BaseDelay:   settings.RetryBaseDelay,
			MaxDelay:    settings.RetryMaxDelay,
		},
		Admission: command.AdmissionLimits{
			GlobalMax:    settings.GlobalAdmissionLimit,
			PerDeviceMax: settings.PerDeviceAdmissionLimit,
		},
	})
	orbitv1.RegisterCommandServiceServer(server, commandservice.New(store, nil))
	gatewayService, err := gatewaycontrol.New(store, gatewaycontrol.Config{
		OutboundBuffer: settings.GatewayOutboundBuffer,
		BatchSize:      settings.SchedulerLeaseBatch,
		SweepLimit:     settings.SchedulerSweepBatch,
		LeaseDuration:  settings.SchedulerLeaseDuration,
		PollInterval:   settings.SchedulerPollInterval,
		Heartbeat: heartbeat.Settings{
			Interval: settings.HeartbeatInterval,
			Timeout:  settings.HeartbeatTimeout,
		},
	})
	if err != nil {
		return err
	}
	orbitv1.RegisterGatewayControlServiceServer(server, gatewayService)
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	serveErrors := make(chan error, 1)
	go func() {
		serveErrors <- server.Serve(listener)
	}()
	logger.Info("orbitd listening", "address", listener.Addr().String())

	metricsErrors := make(chan error, 1)
	go func() {
		metricsErrors <- metrics.Serve(rootContext, settings.MetricsAddress, logger, "orbitd")
	}()

	select {
	case err := <-serveErrors:
		return err
	case err := <-metricsErrors:
		return err
	case <-rootContext.Done():
		healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
		logger.Info("orbitd draining", "timeout", settings.ShutdownTimeout.String())
	}

	drained := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(drained)
	}()
	timer := time.NewTimer(settings.ShutdownTimeout)
	defer timer.Stop()
	select {
	case <-drained:
		logger.Info("orbitd stopped cleanly")
	case <-timer.C:
		logger.Warn("orbitd drain deadline exceeded")
		server.Stop()
		<-drained
	}
	if err := <-metricsErrors; err != nil {
		return err
	}
	return nil
}
