package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"github.com/JayYarlagadda/orbit/internal/config"
	"github.com/JayYarlagadda/orbit/internal/gateway"
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
	instanceID, err := randomID()
	if err != nil {
		return err
	}
	rootContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

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
	orbitv1.RegisterDeviceServiceServer(server, gateway.NewDeviceService(hub))
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	logger.Info("gateway listening", "gateway_id", settings.GatewayID, "address", listener.Addr().String())

	var runError error
	select {
	case <-rootContext.Done():
	case runError = <-controlErrors:
	case runError = <-serveErrors:
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
	return runError
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
