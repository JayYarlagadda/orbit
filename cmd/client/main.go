package main

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"os/signal"
	"syscall"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"github.com/JayYarlagadda/orbit/internal/client"
	"github.com/JayYarlagadda/orbit/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const maxGRPCMessageBytes = 70 * 1024

// healthySession is how long a session must last before the reconnect backoff
// is treated as recovered and reset to its initial delay.
const healthySession = 30 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("client stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	settings, err := config.LoadClient(os.LookupEnv)
	if err != nil {
		return err
	}
	instanceID := settings.ClientInstanceID
	if instanceID == "" {
		if instanceID, err = randomID(); err != nil {
			return err
		}
	}
	state, err := client.OpenStateStore(settings.StatePath, settings.DeviceID, settings.DedupRetention)
	if err != nil {
		return err
	}

	rootContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	connection, err := grpc.NewClient(
		settings.GatewayAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxGRPCMessageBytes),
			grpc.MaxCallSendMsgSize(maxGRPCMessageBytes),
		),
	)
	if err != nil {
		return err
	}
	defer connection.Close()

	deviceClient := orbitv1.NewDeviceServiceClient(connection)
	handler := &applyHandler{logger: logger}
	logger.Info("client starting",
		"device_id", settings.DeviceID,
		"client_instance_id", instanceID,
		"gateway_address", settings.GatewayAddress,
		"state_path", settings.StatePath,
	)

	delay := settings.ReconnectInitialDelay
	failures := 0
	for {
		startedAt := time.Now()
		sessionErr := client.RunSession(
			rootContext,
			deviceClient,
			client.SessionConfig{DeviceID: settings.DeviceID, ClientInstanceID: instanceID},
			state,
			handler,
		)
		if rootContext.Err() != nil {
			logger.Info("client stopping", "last_seen_sequence", state.LastSeenSequence())
			return nil
		}
		if sessionErr == nil {
			logger.Info("device session closed by the gateway")
		} else {
			logger.Warn("device session ended", "error", sessionErr)
		}

		// A session that stayed up is evidence the endpoint recovered, so the
		// next outage restarts from the initial delay rather than the cap.
		if time.Since(startedAt) >= healthySession {
			delay = settings.ReconnectInitialDelay
			failures = 0
		}
		failures++
		if settings.MaxReconnectAttempts > 0 && failures > settings.MaxReconnectAttempts {
			return fmt.Errorf("device session failed %d consecutive times: %w", failures-1, sessionErr)
		}

		wait := jitter(delay)
		logger.Info("reconnecting", "attempt", failures, "delay", wait.String())
		timer := time.NewTimer(wait)
		select {
		case <-rootContext.Done():
			timer.Stop()
			logger.Info("client stopping", "last_seen_sequence", state.LastSeenSequence())
			return nil
		case <-timer.C:
		}
		if delay = delay * 2; delay > settings.ReconnectMaxDelay {
			delay = settings.ReconnectMaxDelay
		}
	}
}

// applyHandler stands in for real device work. It is deliberately deterministic
// so the acknowledgement result hash is reproducible across replays.
type applyHandler struct{ logger *slog.Logger }

func (h *applyHandler) Apply(_ context.Context, delivery *orbitv1.CommandDelivery) ([]byte, error) {
	h.logger.Info("applying command",
		"command_id", delivery.CommandId,
		"sequence_number", delivery.SequenceNumber,
		"lease_token", delivery.LeaseToken,
		"session_epoch", delivery.SessionEpoch,
		"payload_bytes", len(delivery.Payload),
	)
	return []byte("applied"), nil
}

// jitter spreads reconnects across the second half of the delay window so that
// many devices recovering from one outage do not retry in lockstep.
func jitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	half := delay / 2
	return half + time.Duration(rand.Int64N(int64(delay-half)+1))
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := cryptorand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
