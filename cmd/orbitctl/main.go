package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultAddress = "127.0.0.1:50051"

type options struct {
	address       string
	timeout       time.Duration
	correlationID string
	command       string
	commandID     string
	producerID    string
	idempotency   string
	deviceID      string
	priority      int
	payload       string
	payloadFile   string
	expiresAfter  time.Duration
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	parsed, err := parseOptions(arguments)
	if err != nil {
		return err
	}
	correlationID := parsed.correlationID
	if correlationID == "" {
		correlationID, err = newCorrelationID()
		if err != nil {
			return err
		}
	}

	connection, err := grpc.NewClient(
		parsed.address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("create gRPC client: %w", err)
	}
	defer connection.Close()

	ctx, cancel := context.WithTimeout(context.Background(), parsed.timeout)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("x-correlation-id", correlationID))
	client := orbitv1.NewCommandServiceClient(connection)

	var result *orbitv1.Command
	switch parsed.command {
	case "submit":
		payload, err := readPayload(parsed)
		if err != nil {
			return err
		}
		result, err = client.SubmitCommand(ctx, &orbitv1.SubmitCommandRequest{
			ProducerId:     parsed.producerID,
			IdempotencyKey: parsed.idempotency,
			DeviceId:       parsed.deviceID,
			Priority:       int32(parsed.priority),
			Payload:        payload,
			ExpiresAt:      timestamppb.New(time.Now().UTC().Add(parsed.expiresAfter)),
		})
	case "get":
		result, err = client.GetCommand(ctx, &orbitv1.GetCommandRequest{CommandId: parsed.commandID})
	case "cancel":
		result, err = client.CancelCommand(ctx, &orbitv1.CancelCommandRequest{CommandId: parsed.commandID})
	default:
		return fmt.Errorf("unsupported command %q", parsed.command)
	}
	if err != nil {
		return fmt.Errorf("%s command: %w", parsed.command, err)
	}

	output, err := (protojson.MarshalOptions{Indent: "  ", UseProtoNames: true}).Marshal(result)
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	fmt.Println(string(output))
	return nil
}

func parseOptions(arguments []string) (options, error) {
	if len(arguments) == 0 {
		return options{}, errors.New("usage: orbitctl <submit|get|cancel> [flags]")
	}
	result := options{command: arguments[0], address: defaultAddress, timeout: 5 * time.Second}
	flags := flag.NewFlagSet("orbitctl "+result.command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&result.address, "address", defaultAddress, "orbitd gRPC address")
	flags.DurationVar(&result.timeout, "timeout", 5*time.Second, "request timeout")
	flags.StringVar(&result.correlationID, "correlation-id", "", "optional audit correlation ID")

	switch result.command {
	case "submit":
		flags.StringVar(&result.producerID, "producer", "", "producer identity")
		flags.StringVar(&result.idempotency, "idempotency-key", "", "producer-scoped idempotency key")
		flags.StringVar(&result.deviceID, "device", "", "target device identity")
		flags.IntVar(&result.priority, "priority", 0, "priority from 0 through 9")
		flags.StringVar(&result.payload, "payload", "", "UTF-8 command payload")
		flags.StringVar(&result.payloadFile, "payload-file", "", "path to a binary command payload")
		flags.DurationVar(&result.expiresAfter, "expires-after", time.Hour, "command lifetime")
	case "get", "cancel":
		flags.StringVar(&result.commandID, "command-id", "", "server-assigned command UUID")
	default:
		return options{}, fmt.Errorf("unsupported command %q", result.command)
	}
	if err := flags.Parse(arguments[1:]); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	if result.address == "" || result.timeout <= 0 {
		return options{}, errors.New("address must not be empty and timeout must be positive")
	}
	if len(result.correlationID) > 128 {
		return options{}, errors.New("correlation-id must not exceed 128 bytes")
	}
	if result.command == "submit" {
		if result.producerID == "" || result.idempotency == "" || result.deviceID == "" {
			return options{}, errors.New("submit requires producer, idempotency-key, and device")
		}
		if (result.payload == "") == (result.payloadFile == "") {
			return options{}, errors.New("submit requires exactly one of payload or payload-file")
		}
		if result.expiresAfter <= 0 {
			return options{}, errors.New("expires-after must be positive")
		}
	} else if result.commandID == "" {
		return options{}, fmt.Errorf("%s requires command-id", result.command)
	}
	return result, nil
}

func readPayload(parsed options) ([]byte, error) {
	if parsed.payloadFile == "" {
		return []byte(parsed.payload), nil
	}
	payload, err := os.ReadFile(parsed.payloadFile)
	if err != nil {
		return nil, fmt.Errorf("read payload file: %w", err)
	}
	return payload, nil
}

func newCorrelationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create correlation ID: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}
