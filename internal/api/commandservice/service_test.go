package commandservice

import (
	"context"
	"errors"
	"testing"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"github.com/JayYarlagadda/orbit/internal/command"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const testCommandID = "018f4f1e-7d5a-7a42-8b8f-a8ab5d83b947"

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type fakeStore struct {
	submitResult command.Command
	submitErr    error
	getResult    command.Command
	getErr       error
	cancelResult command.Command
	cancelErr    error
	submission   command.Submission
	actor        string
	correlation  string
	submitCalls  int
}

func (s *fakeStore) Submit(_ context.Context, submission command.Submission, actor, correlation string) (command.Command, bool, error) {
	s.submitCalls++
	s.submission = submission
	s.actor = actor
	s.correlation = correlation
	return s.submitResult, true, s.submitErr
}

func (s *fakeStore) Get(context.Context, string) (command.Command, error) {
	return s.getResult, s.getErr
}

func (s *fakeStore) Cancel(_ context.Context, _ string, actor, correlation string) (command.Command, error) {
	s.actor = actor
	s.correlation = correlation
	return s.cancelResult, s.cancelErr
}

func TestSubmitCommand(t *testing.T) {
	now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
	stored := testCommand(now)
	store := &fakeStore{submitResult: stored}
	service := New(store, fixedClock{now: now})
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(correlationMetadataKey, "trace-42"))

	got, err := service.SubmitCommand(ctx, &orbitv1.SubmitCommandRequest{
		ProducerId:     "producer-1",
		IdempotencyKey: "request-1",
		DeviceId:       "device-1",
		Priority:       3,
		Payload:        []byte("collect-diagnostics"),
		ExpiresAt:      timestamppb.New(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("SubmitCommand() error = %v", err)
	}
	if got.CommandId != testCommandID || got.State != orbitv1.CommandState_COMMAND_STATE_QUEUED {
		t.Fatalf("SubmitCommand() = %+v", got)
	}
	if store.actor != "producer:producer-1" || store.correlation != "trace-42" {
		t.Fatalf("audit context = (%q, %q)", store.actor, store.correlation)
	}
	if string(store.submission.Payload) != "collect-diagnostics" {
		t.Fatalf("stored payload = %q", store.submission.Payload)
	}
}

func TestSubmitCommandRejectsBeforeStorage(t *testing.T) {
	now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
	store := &fakeStore{}
	service := New(store, fixedClock{now: now})

	tests := []struct {
		name    string
		request *orbitv1.SubmitCommandRequest
	}{
		{name: "nil request", request: nil},
		{name: "missing expiry", request: &orbitv1.SubmitCommandRequest{}},
		{name: "priority narrowing", request: &orbitv1.SubmitCommandRequest{Priority: 32768, ExpiresAt: timestamppb.New(now.Add(time.Hour))}},
		{name: "expired", request: &orbitv1.SubmitCommandRequest{ProducerId: "p", IdempotencyKey: "k", DeviceId: "d", Payload: []byte("x"), ExpiresAt: timestamppb.New(now)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.SubmitCommand(context.Background(), test.request)
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("SubmitCommand() code = %s, want InvalidArgument", status.Code(err))
			}
		})
	}
	if store.submitCalls != 0 {
		t.Fatalf("store Submit() calls = %d, want 0", store.submitCalls)
	}
}

func TestServiceErrorMapping(t *testing.T) {
	now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "not found", err: command.ErrNotFound, code: codes.NotFound},
		{name: "conflict", err: command.ErrIdempotencyConflict, code: codes.AlreadyExists},
		{name: "transition", err: &command.TransitionError{From: command.StateLeased, To: command.StateCancelled}, code: codes.FailedPrecondition},
		{name: "canceled", err: context.Canceled, code: codes.Canceled},
		{name: "internal", err: errors.New("database password leaked here"), code: codes.Internal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{getErr: test.err}
			service := New(store, fixedClock{now: now})
			_, err := service.GetCommand(context.Background(), &orbitv1.GetCommandRequest{CommandId: testCommandID})
			if status.Code(err) != test.code {
				t.Fatalf("GetCommand() code = %s, want %s", status.Code(err), test.code)
			}
			if test.code == codes.Internal && status.Convert(err).Message() != "internal command service error" {
				t.Fatalf("internal error leaked detail: %q", status.Convert(err).Message())
			}
		})
	}
}

func TestCancelCommandUsesGeneratedCorrelationID(t *testing.T) {
	now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
	store := &fakeStore{cancelResult: testCommand(now)}
	service := New(store, fixedClock{now: now})

	if _, err := service.CancelCommand(context.Background(), &orbitv1.CancelCommandRequest{CommandId: testCommandID}); err != nil {
		t.Fatalf("CancelCommand() error = %v", err)
	}
	if store.actor != "command-api" || len(store.correlation) != 32 {
		t.Fatalf("audit context = (%q, %q)", store.actor, store.correlation)
	}
}

func testCommand(now time.Time) command.Command {
	return command.Command{
		ID:             testCommandID,
		ProducerID:     "producer-1",
		IdempotencyKey: "request-1",
		DeviceID:       "device-1",
		SequenceNumber: 1,
		Priority:       3,
		Payload:        []byte("collect-diagnostics"),
		State:          command.StateQueued,
		CreatedAt:      now,
		ExpiresAt:      now.Add(time.Hour),
		NextAttemptAt:  now,
	}
}
