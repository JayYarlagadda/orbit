package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
)

type Handler interface {
	Apply(context.Context, *orbitv1.CommandDelivery) ([]byte, error)
}

type StateStore struct {
	mu        sync.Mutex
	path      string
	deviceID  string
	retention int
	state     persistedState
}

type persistedState struct {
	SchemaVersion    int             `json:"schema_version"`
	DeviceID         string          `json:"device_id"`
	LastSeenSequence int64           `json:"last_seen_sequence"`
	LastSessionEpoch int64           `json:"last_session_epoch"`
	Records          []appliedRecord `json:"records"`
}

type appliedRecord struct {
	CommandID  string `json:"command_id"`
	Sequence   int64  `json:"sequence"`
	ResultHash string `json:"result_hash"`
}

func OpenStateStore(path, deviceID string, retention int) (*StateStore, error) {
	if path == "" || deviceID == "" {
		return nil, errors.New("client state path and device ID are required")
	}
	if retention < 1 || retention > 100_000 {
		return nil, errors.New("client deduplication retention must be between 1 and 100000")
	}
	store := &StateStore{
		path:      path,
		deviceID:  deviceID,
		retention: retention,
		state: persistedState{
			SchemaVersion: 1,
			DeviceID:      deviceID,
		},
	}
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read client state: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&store.state); err != nil {
		return nil, fmt.Errorf("decode client state: %w", err)
	}
	if store.state.SchemaVersion != 1 || store.state.DeviceID != deviceID || store.state.LastSeenSequence < 0 || store.state.LastSessionEpoch < 0 {
		return nil, errors.New("client state identity or schema is invalid")
	}
	if len(store.state.Records) > retention {
		store.state.Records = append([]appliedRecord(nil), store.state.Records[len(store.state.Records)-retention:]...)
	}
	return store, nil
}

func (s *StateStore) LastSessionEpoch() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.LastSessionEpoch
}

func (s *StateStore) ObserveSession(epoch int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if epoch < 1 || epoch < s.state.LastSessionEpoch {
		return errors.New("session epoch regressed")
	}
	if epoch == s.state.LastSessionEpoch {
		return nil
	}
	previous := s.state.LastSessionEpoch
	s.state.LastSessionEpoch = epoch
	if err := s.persist(); err != nil {
		s.state.LastSessionEpoch = previous
		return err
	}
	return nil
}

func (s *StateStore) LastSeenSequence() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.LastSeenSequence
}

func (s *StateStore) Apply(
	ctx context.Context,
	delivery *orbitv1.CommandDelivery,
	handler Handler,
) ([sha256.Size]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if delivery == nil || handler == nil {
		return [sha256.Size]byte{}, false, errors.New("delivery and handler are required")
	}
	if delivery.DeviceId != s.deviceID || delivery.SequenceNumber < 1 {
		return [sha256.Size]byte{}, false, errors.New("delivery identity or sequence is invalid")
	}
	payloadHash := sha256.Sum256(delivery.Payload)
	if !equalHash(payloadHash[:], delivery.PayloadHash) {
		return [sha256.Size]byte{}, false, errors.New("delivery payload hash mismatch")
	}
	for _, record := range s.state.Records {
		if record.CommandID == delivery.CommandId {
			decoded, err := hex.DecodeString(record.ResultHash)
			if err != nil || len(decoded) != sha256.Size {
				return [sha256.Size]byte{}, false, errors.New("stored result hash is invalid")
			}
			var result [sha256.Size]byte
			copy(result[:], decoded)
			return result, false, nil
		}
	}
	if delivery.SequenceNumber <= s.state.LastSeenSequence {
		return sha256.Sum256(nil), false, nil
	}

	result, err := handler.Apply(ctx, delivery)
	if err != nil {
		return [sha256.Size]byte{}, false, fmt.Errorf("apply command: %w", err)
	}
	resultHash := sha256.Sum256(result)
	previous := s.state
	s.state.LastSeenSequence = delivery.SequenceNumber
	s.state.Records = append(s.state.Records, appliedRecord{
		CommandID:  delivery.CommandId,
		Sequence:   delivery.SequenceNumber,
		ResultHash: hex.EncodeToString(resultHash[:]),
	})
	if len(s.state.Records) > s.retention {
		s.state.Records = append([]appliedRecord(nil), s.state.Records[len(s.state.Records)-s.retention:]...)
	}
	if err := s.persist(); err != nil {
		s.state = previous
		return [sha256.Size]byte{}, false, err
	}
	return resultHash, true, nil
}

func (s *StateStore) persist() error {
	contents, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode client state: %w", err)
	}
	contents = append(contents, '\n')
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create client state directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".orbit-state-*")
	if err != nil {
		return fmt.Errorf("create temporary client state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("secure temporary client state: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary client state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary client state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary client state: %w", err)
	}
	if err := replaceFile(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replace client state: %w", err)
	}
	return nil
}

func equalHash(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}
