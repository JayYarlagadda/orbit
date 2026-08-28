package client

import (
	"context"
	"crypto/sha256"
	"testing"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
)

type countingHandler struct{ calls int }

func (h *countingHandler) Apply(context.Context, *orbitv1.CommandDelivery) ([]byte, error) {
	h.calls++
	return []byte("applied"), nil
}

func TestStateStorePersistsBeforeDuplicateAcknowledgement(t *testing.T) {
	path := t.TempDir() + "/state.json"
	store, err := OpenStateStore(path, "device-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	handler := &countingHandler{}
	delivery := testDelivery("command-1", 1, "payload-1")
	firstHash, applied, err := store.Apply(context.Background(), delivery, handler)
	if err != nil || !applied || handler.calls != 1 {
		t.Fatalf("first Apply() = (%x, %v, %v), calls=%d", firstHash, applied, err, handler.calls)
	}
	duplicateHash, applied, err := store.Apply(context.Background(), delivery, handler)
	if err != nil || applied || handler.calls != 1 || duplicateHash != firstHash {
		t.Fatalf("duplicate Apply() = (%x, %v, %v), calls=%d", duplicateHash, applied, err, handler.calls)
	}

	reopened, err := OpenStateStore(path, "device-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.LastSeenSequence() != 1 {
		t.Fatalf("LastSeenSequence() = %d", reopened.LastSeenSequence())
	}
	if _, applied, err := reopened.Apply(context.Background(), delivery, handler); err != nil || applied || handler.calls != 1 {
		t.Fatalf("reopened duplicate Apply() = (%v, %v), calls=%d", applied, err, handler.calls)
	}
}

func TestStateStoreBoundsRecordsAndRejectsPayloadCorruption(t *testing.T) {
	store, err := OpenStateStore(t.TempDir()+"/state.json", "device-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	handler := &countingHandler{}
	for sequence := int64(1); sequence <= 3; sequence++ {
		if _, _, err := store.Apply(context.Background(), testDelivery("command-"+string(rune('0'+sequence)), sequence, "payload"), handler); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.state.Records) != 2 || store.state.Records[0].Sequence != 2 {
		t.Fatalf("records = %+v", store.state.Records)
	}
	corrupt := testDelivery("command-4", 4, "payload")
	corrupt.Payload[0] ^= 0xff
	if _, _, err := store.Apply(context.Background(), corrupt, handler); err == nil {
		t.Fatal("corrupt delivery unexpectedly applied")
	}
}

func testDelivery(commandID string, sequence int64, payload string) *orbitv1.CommandDelivery {
	hash := sha256.Sum256([]byte(payload))
	return &orbitv1.CommandDelivery{
		CommandId:      commandID,
		DeviceId:       "device-1",
		SequenceNumber: sequence,
		Payload:        []byte(payload),
		PayloadHash:    hash[:],
	}
}
