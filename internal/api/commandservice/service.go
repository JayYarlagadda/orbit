package commandservice

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"github.com/JayYarlagadda/orbit/internal/command"
	"github.com/JayYarlagadda/orbit/internal/telemetry"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const correlationMetadataKey = "x-correlation-id"

type Store interface {
	Submit(context.Context, command.Submission, string, string) (command.Command, bool, error)
	Get(context.Context, string) (command.Command, error)
	Cancel(context.Context, string, string, string) (command.Command, error)
}

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

type Service struct {
	orbitv1.UnimplementedCommandServiceServer
	store Store
	clock Clock
}

func New(store Store, clock Clock) *Service {
	if clock == nil {
		clock = SystemClock{}
	}
	return &Service{store: store, clock: clock}
}

func (s *Service) SubmitCommand(ctx context.Context, request *orbitv1.SubmitCommandRequest) (*orbitv1.Command, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request must not be nil")
	}
	if request.ExpiresAt == nil {
		return nil, status.Error(codes.InvalidArgument, "expires_at must be provided")
	}
	if err := request.ExpiresAt.CheckValid(); err != nil {
		return nil, status.Error(codes.InvalidArgument, "expires_at is invalid")
	}
	if request.Priority < command.MinPriority || request.Priority > command.MaxPriority {
		return nil, status.Errorf(codes.InvalidArgument, "priority must be between %d and %d", command.MinPriority, command.MaxPriority)
	}

	submission, err := command.NewSubmission(
		request.ProducerId,
		request.IdempotencyKey,
		request.DeviceId,
		int16(request.Priority),
		request.Payload,
		request.ExpiresAt.AsTime(),
		s.clock.Now(),
	)
	if err != nil {
		return nil, mapError(err)
	}

	requestCorrelationID, err := correlationID(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not create request correlation ID")
	}
	ctx, span := telemetry.Start(
		ctx,
		"orbit.command.submit",
		telemetry.CorrelationID(requestCorrelationID),
		telemetry.DeviceID(submission.DeviceID),
	)
	var submitErr error
	defer func() { telemetry.End(span, submitErr) }()

	stored, _, submitErr := s.store.Submit(
		ctx,
		submission,
		"producer:"+submission.ProducerID,
		requestCorrelationID,
	)
	if submitErr != nil {
		return nil, mapError(submitErr)
	}
	span.SetAttributes(telemetry.CommandID(stored.ID))
	return toProto(stored), nil
}

func (s *Service) GetCommand(ctx context.Context, request *orbitv1.GetCommandRequest) (*orbitv1.Command, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request must not be nil")
	}
	if !command.ValidateID(request.CommandId) {
		return nil, status.Error(codes.InvalidArgument, "command_id must be a UUID")
	}

	stored, err := s.store.Get(ctx, request.CommandId)
	if err != nil {
		return nil, mapError(err)
	}
	return toProto(stored), nil
}

func (s *Service) CancelCommand(ctx context.Context, request *orbitv1.CancelCommandRequest) (*orbitv1.Command, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request must not be nil")
	}
	if !command.ValidateID(request.CommandId) {
		return nil, status.Error(codes.InvalidArgument, "command_id must be a UUID")
	}

	requestCorrelationID, err := correlationID(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "could not create request correlation ID")
	}
	stored, err := s.store.Cancel(ctx, request.CommandId, "command-api", requestCorrelationID)
	if err != nil {
		return nil, mapError(err)
	}
	return toProto(stored), nil
}

func correlationID(ctx context.Context) (string, error) {
	if incoming, ok := metadata.FromIncomingContext(ctx); ok {
		values := incoming.Get(correlationMetadataKey)
		if len(values) > 0 && len(values[0]) <= 128 && values[0] != "" {
			return values[0], nil
		}
	}

	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("read cryptographic randomness: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func mapError(err error) error {
	var validationError *command.ValidationError
	var transitionError *command.TransitionError
	switch {
	case errors.As(err, &validationError):
		return status.Error(codes.InvalidArgument, validationError.Error())
	case errors.Is(err, command.ErrIdempotencyConflict):
		return status.Error(codes.AlreadyExists, command.ErrIdempotencyConflict.Error())
	case errors.Is(err, command.ErrNotFound):
		return status.Error(codes.NotFound, command.ErrNotFound.Error())
	case errors.Is(err, command.ErrAdmissionLimited):
		return status.Error(codes.ResourceExhausted, command.ErrAdmissionLimited.Error())
	case errors.As(err, &transitionError):
		return status.Error(codes.FailedPrecondition, transitionError.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "request canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "request deadline exceeded")
	default:
		return status.Error(codes.Internal, "internal command service error")
	}
}

func toProto(source command.Command) *orbitv1.Command {
	result := &orbitv1.Command{
		CommandId:      source.ID,
		ProducerId:     source.ProducerID,
		IdempotencyKey: source.IdempotencyKey,
		DeviceId:       source.DeviceID,
		SequenceNumber: source.SequenceNumber,
		Priority:       int32(source.Priority),
		Payload:        append([]byte(nil), source.Payload...),
		PayloadHash:    append([]byte(nil), source.PayloadHash[:]...),
		State:          stateToProto(source.State),
		CreatedAt:      timestamppb.New(source.CreatedAt),
		ExpiresAt:      timestamppb.New(source.ExpiresAt),
		NextAttemptAt:  timestamppb.New(source.NextAttemptAt),
		AttemptCount:   source.AttemptCount,
		LeaseToken:     source.LeaseToken,
		FailureReason:  source.FailureReason,
	}
	if source.AcknowledgedAt != nil {
		result.AcknowledgedAt = timestamppb.New(*source.AcknowledgedAt)
	}
	return result
}

func stateToProto(state command.State) orbitv1.CommandState {
	switch state {
	case command.StateQueued:
		return orbitv1.CommandState_COMMAND_STATE_QUEUED
	case command.StateLeased:
		return orbitv1.CommandState_COMMAND_STATE_LEASED
	case command.StateInFlight:
		return orbitv1.CommandState_COMMAND_STATE_IN_FLIGHT
	case command.StateRetryWait:
		return orbitv1.CommandState_COMMAND_STATE_RETRY_WAIT
	case command.StateAcknowledged:
		return orbitv1.CommandState_COMMAND_STATE_ACKNOWLEDGED
	case command.StateExpired:
		return orbitv1.CommandState_COMMAND_STATE_EXPIRED
	case command.StateDeadLetter:
		return orbitv1.CommandState_COMMAND_STATE_DEAD_LETTER
	case command.StateCancelled:
		return orbitv1.CommandState_COMMAND_STATE_CANCELLED
	default:
		return orbitv1.CommandState_COMMAND_STATE_UNSPECIFIED
	}
}
