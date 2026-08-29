package client

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const sessionTestTimeout = 2 * time.Second

type scriptedDeviceClient struct {
	stream orbitv1.DeviceService_ConnectClient
}

func (c *scriptedDeviceClient) Connect(context.Context, ...grpc.CallOption) (orbitv1.DeviceService_ConnectClient, error) {
	return c.stream, nil
}

type scriptedDeviceStream struct {
	ctx       context.Context
	incoming  chan *orbitv1.ServerFrame
	sent      chan *orbitv1.DeviceFrame
	sendError error
	mu        sync.Mutex
}

func newScriptedDeviceStream(ctx context.Context) *scriptedDeviceStream {
	return &scriptedDeviceStream{
		ctx:      ctx,
		incoming: make(chan *orbitv1.ServerFrame, 8),
		sent:     make(chan *orbitv1.DeviceFrame, 8),
	}
}

func (s *scriptedDeviceStream) Send(frame *orbitv1.DeviceFrame) error {
	s.mu.Lock()
	err := s.sendError
	s.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case s.sent <- frame:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *scriptedDeviceStream) Recv() (*orbitv1.ServerFrame, error) {
	select {
	case frame, ok := <-s.incoming:
		if !ok {
			return nil, io.EOF
		}
		return frame, nil
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func (s *scriptedDeviceStream) Header() (metadata.MD, error) { return nil, nil }
func (s *scriptedDeviceStream) Trailer() metadata.MD         { return nil }
func (s *scriptedDeviceStream) CloseSend() error             { return nil }
func (s *scriptedDeviceStream) Context() context.Context     { return s.ctx }
func (s *scriptedDeviceStream) SendMsg(any) error            { return nil }
func (s *scriptedDeviceStream) RecvMsg(any) error            { return nil }

func (s *scriptedDeviceStream) failSend(err error) {
	s.mu.Lock()
	s.sendError = err
	s.mu.Unlock()
}

func awaitDeviceFrame(t *testing.T, stream *scriptedDeviceStream) *orbitv1.DeviceFrame {
	t.Helper()
	select {
	case frame := <-stream.sent:
		return frame
	case <-time.After(sessionTestTimeout):
		t.Fatal("timed out waiting for a device frame")
		return nil
	}
}

func TestRunSessionAcknowledgesDuplicateWithoutReapplying(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), sessionTestTimeout)
	defer cancel()
	stream := newScriptedDeviceStream(ctx)
	store, err := OpenStateStore(t.TempDir()+"/state.json", "device-1", 8)
	if err != nil {
		t.Fatal(err)
	}
	handler := &countingHandler{}
	done := make(chan error, 1)
	go func() {
		done <- RunSession(ctx, &scriptedDeviceClient{stream: stream}, SessionConfig{
			DeviceID:         "device-1",
			ClientInstanceID: "client-1",
		}, store, handler)
	}()

	hello := awaitDeviceFrame(t, stream).GetHello()
	if hello == nil || hello.DeviceId != "device-1" {
		t.Fatalf("first frame = %+v, want DeviceHello", hello)
	}
	stream.incoming <- &orbitv1.ServerFrame{Body: &orbitv1.ServerFrame_SessionOpened{
		SessionOpened: &orbitv1.SessionOpened{DeviceId: "device-1", SessionEpoch: 4},
	}}
	delivery := testDelivery("command-1", 1, "payload-1")
	delivery.SessionEpoch = 4
	delivery.LeaseToken = 1
	for range 2 {
		stream.incoming <- &orbitv1.ServerFrame{Body: &orbitv1.ServerFrame_Command{Command: delivery}}
	}
	close(stream.incoming)

	acks := make([]*orbitv1.CommandAck, 0, 2)
	for len(acks) < 2 {
		frame := awaitDeviceFrame(t, stream)
		ack := frame.GetAck()
		if ack == nil {
			t.Fatalf("device frame = %+v, want CommandAck", frame)
		}
		acks = append(acks, ack)
	}
	if err := <-done; err != nil {
		t.Fatalf("RunSession() error = %v", err)
	}
	if handler.calls != 1 {
		t.Fatalf("handler calls = %d, want 1", handler.calls)
	}
	if acks[0].CommandId != "command-1" || acks[1].CommandId != "command-1" {
		t.Fatalf("acks = %+v", acks)
	}
	if string(acks[0].ResultHash) != string(acks[1].ResultHash) {
		t.Fatal("duplicate ACK used a different result hash")
	}
	if store.LastSeenSequence() != 1 {
		t.Fatalf("LastSeenSequence() = %d, want 1", store.LastSeenSequence())
	}
}

func TestRunSessionDisconnectDuringAckLeavesRecoverableState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), sessionTestTimeout)
	defer cancel()
	stream := newScriptedDeviceStream(ctx)
	path := t.TempDir() + "/state.json"
	store, err := OpenStateStore(path, "device-1", 8)
	if err != nil {
		t.Fatal(err)
	}
	handler := &countingHandler{}
	done := make(chan error, 1)
	go func() {
		done <- RunSession(ctx, &scriptedDeviceClient{stream: stream}, SessionConfig{
			DeviceID:         "device-1",
			ClientInstanceID: "client-1",
		}, store, handler)
	}()

	_ = awaitDeviceFrame(t, stream)
	stream.incoming <- &orbitv1.ServerFrame{Body: &orbitv1.ServerFrame_SessionOpened{
		SessionOpened: &orbitv1.SessionOpened{DeviceId: "device-1", SessionEpoch: 2},
	}}
	stream.failSend(errors.New("connection reset while sending ACK"))
	delivery := testDelivery("command-1", 1, "payload-1")
	delivery.SessionEpoch = 2
	delivery.LeaseToken = 9
	stream.incoming <- &orbitv1.ServerFrame{Body: &orbitv1.ServerFrame_Command{Command: delivery}}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("RunSession() succeeded after ACK send failed")
		}
	case <-time.After(sessionTestTimeout):
		t.Fatal("timed out waiting for RunSession to return")
	}
	if handler.calls != 1 {
		t.Fatalf("handler calls = %d, want 1", handler.calls)
	}

	reopened, err := OpenStateStore(path, "device-1", 8)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.LastSeenSequence() != 1 {
		t.Fatalf("LastSeenSequence() after crash = %d, want 1", reopened.LastSeenSequence())
	}
	wantHash := sha256.Sum256([]byte("applied"))
	gotHash, applied, err := reopened.Apply(context.Background(), delivery, handler)
	if err != nil || applied || handler.calls != 1 || gotHash != wantHash {
		t.Fatalf("replay Apply() = (%x, %v, %v), calls=%d", gotHash, applied, err, handler.calls)
	}
}
