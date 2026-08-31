package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"github.com/JayYarlagadda/orbit/internal/config"
	"github.com/JayYarlagadda/orbit/internal/faultschedule"
	"github.com/JayYarlagadda/orbit/internal/gateway"
	"github.com/JayYarlagadda/orbit/internal/heartbeat"
	"github.com/JayYarlagadda/orbit/internal/metrics"
	"github.com/JayYarlagadda/orbit/internal/shutdownsignal"
	"github.com/JayYarlagadda/orbit/internal/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

const maxGRPCMessageBytes = 70 * 1024

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	settings, err := config.LoadGateway(os.LookupEnv)
	if err != nil {
		return err
	}
	telemetrySettings := config.LoadTelemetry(os.LookupEnv, "orbit-gateway")
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

	instanceID, err := randomID()
	if err != nil {
		return err
	}

	hub, err := gateway.NewHub(gateway.HubConfig{
		GatewayID:        settings.GatewayID,
		ControlBuffer:    settings.ControlBuffer,
		ConnectionBuffer: settings.ConnectionBuffer,
	})
	if err != nil {
		return err
	}
	controlConnection, err := grpc.NewClient(
		settings.ControlAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxGRPCMessageBytes),
			grpc.MaxCallSendMsgSize(maxGRPCMessageBytes),
		),
	)
	if err != nil {
		return err
	}
	defer controlConnection.Close()
	heartbeatSettings := heartbeat.Settings{
		Interval: settings.HeartbeatInterval,
		Timeout:  settings.HeartbeatTimeout,
	}
	controlErrors := make(chan error, 1)
	go func() {
		controlErrors <- gateway.RunControl(
			rootContext,
			orbitv1.NewGatewayControlServiceClient(controlConnection),
			hub,
			gateway.ControlConfig{
				GatewayInstanceID: instanceID,
				InitialDelay:      settings.ReconnectInitialDelay,
				MaxDelay:          settings.ReconnectMaxDelay,
				MaxAttempts:       settings.MaxReconnectAttempts,
				Heartbeat:         heartbeatSettings,
				Logger:            logger,
			},
		)
	}()

	listener, err := net.Listen("tcp", settings.ListenAddress)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(maxGRPCMessageBytes),
		grpc.MaxSendMsgSize(maxGRPCMessageBytes),
	)
	deviceService := gateway.NewDeviceService(hub)
	deviceService.Heartbeat = heartbeatSettings
	if schedulePath, ok := os.LookupEnv("ORBIT_GATEWAY_FAULT_SCHEDULE_PATH"); ok && schedulePath != "" {
		startedAt := time.Now().UTC()
		if value, ok := os.LookupEnv("ORBIT_SCENARIO_STARTED_AT"); ok && value != "" {
			parsed, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return fmt.Errorf("parse ORBIT_SCENARIO_STARTED_AT: %w", err)
			}
			startedAt = parsed.UTC()
		}
		faults, err := faultschedule.LoadController(schedulePath, startedAt)
		if err != nil {
			return err
		}
		deviceService.Faults = faults
	}
	orbitv1.RegisterDeviceServiceServer(server, deviceService)
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	logger.Info("gateway listening", "gateway_id", settings.GatewayID, "address", listener.Addr().String())

	metricsErrors := make(chan error, 1)
	go func() {
		metricsErrors <- metrics.Serve(rootContext, settings.MetricsAddress, logger, "gateway")
	}()

	var runError error
	select {
	case <-rootContext.Done():
	case runError = <-controlErrors:
	case runError = <-serveErrors:
	case runError = <-metricsErrors:
	}
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	logger.Info("gateway draining", "timeout", settings.ShutdownTimeout.String())

	drained := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(drained)
	}()
	timer := time.NewTimer(settings.ShutdownTimeout)
	defer timer.Stop()
	select {
	case <-drained:
	case <-timer.C:
		server.Stop()
		<-drained
	}
	if err := <-metricsErrors; err != nil && runError == nil {
		runError = err
	}
	return runError
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
