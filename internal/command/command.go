// Package command defines Orbit's durable command model and state machine.
package command

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	MaxProducerIDLength     = 64
	MaxIdempotencyKeyLength = 128
	MaxDeviceIDLength       = 64
	MaxPayloadBytes         = 64 * 1024
	MaxLeaseOwnerLength     = 128
	MaxLeaseBatchSize       = 256
	MinLeaseDuration        = time.Second
	MaxLeaseDuration        = 5 * time.Minute
	MinPriority             = 0
	MaxPriority             = 9
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
var idPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type State string

const (
	StateQueued       State = "QUEUED"
	StateLeased       State = "LEASED"
	StateInFlight     State = "IN_FLIGHT"
	StateRetryWait    State = "RETRY_WAIT"
	StateAcknowledged State = "ACKNOWLEDGED"
	StateExpired      State = "EXPIRED"
	StateDeadLetter   State = "DEAD_LETTER"
	StateCancelled    State = "CANCELLED"
)

var allowedTransitions = map[State]map[State]struct{}{
	StateQueued: {
		StateLeased: {}, StateExpired: {}, StateCancelled: {},
	},
	StateLeased: {
		StateInFlight: {}, StateRetryWait: {}, StateExpired: {}, StateDeadLetter: {},
	},
	StateInFlight: {
		StateAcknowledged: {}, StateRetryWait: {}, StateExpired: {}, StateDeadLetter: {},
	},
	StateRetryWait: {
		StateLeased: {}, StateExpired: {}, StateDeadLetter: {}, StateCancelled: {},
	},
}

type Command struct {
	ID             string
	ProducerID     string
	IdempotencyKey string
	DeviceID       string
	SequenceNumber int64
	Priority       int16
	Payload        []byte
	PayloadHash    [sha256.Size]byte
	RequestHash    [sha256.Size]byte
	State          State
	CreatedAt      time.Time
	ExpiresAt      time.Time
	NextAttemptAt  time.Time
	AttemptCount   int32
	LeaseOwner     string
	LeaseToken     int64
	LeaseExpiresAt *time.Time
	AcknowledgedAt *time.Time
	FailureReason  string
}

type Submission struct {
	ProducerID     string
	IdempotencyKey string
	DeviceID       string
	Priority       int16
	Payload        []byte
	ExpiresAt      time.Time
	RequestHash    [sha256.Size]byte
	PayloadHash    [sha256.Size]byte
}

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

type TransitionError struct {
	From State
	To   State
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("command transition %s -> %s is not allowed", e.From, e.To)
}

var (
	ErrNotFound            = errors.New("command not found")
	ErrIdempotencyConflict = errors.New("idempotency key conflicts with an existing command")
	ErrStaleLease          = errors.New("command lease is stale or no longer active")
	ErrAdmissionLimited    = errors.New("command admission limit reached")
)

type LeaseRequest struct {
	Owner    string
	Limit    int
	Duration time.Duration
}

type Lease struct {
	Command      Command
	SessionEpoch int64
}

type Acknowledgement struct {
	CommandID       string
	DeviceID        string
	SequenceNumber  int64
	GatewayID       string
	LeaseToken      int64
	SessionEpoch    int64
	ResultHash      [sha256.Size]byte
	ClientAppliedAt *time.Time
}

func NewAcknowledgement(
	commandID string,
	deviceID string,
	sequenceNumber int64,
	gatewayID string,
	leaseToken int64,
	sessionEpoch int64,
	resultHash []byte,
	clientAppliedAt *time.Time,
) (Acknowledgement, error) {
	if !ValidateID(commandID) {
		return Acknowledgement{}, &ValidationError{Field: "command_id", Message: "must be a UUID"}
	}
	if err := validateIdentifier("device_id", deviceID, MaxDeviceIDLength); err != nil {
		return Acknowledgement{}, err
	}
	if err := validateIdentifier("gateway_id", gatewayID, MaxDeviceIDLength); err != nil {
		return Acknowledgement{}, err
	}
	if sequenceNumber < 1 {
		return Acknowledgement{}, &ValidationError{Field: "sequence_number", Message: "must be positive"}
	}
	if leaseToken < 1 {
		return Acknowledgement{}, &ValidationError{Field: "lease_token", Message: "must be positive"}
	}
	if sessionEpoch < 1 {
		return Acknowledgement{}, &ValidationError{Field: "session_epoch", Message: "must be positive"}
	}
	if len(resultHash) != sha256.Size {
		return Acknowledgement{}, &ValidationError{Field: "result_hash", Message: fmt.Sprintf("must contain %d bytes", sha256.Size)}
	}
	result := Acknowledgement{
		CommandID:      commandID,
		DeviceID:       deviceID,
		SequenceNumber: sequenceNumber,
		GatewayID:      gatewayID,
		LeaseToken:     leaseToken,
		SessionEpoch:   sessionEpoch,
	}
	copy(result.ResultHash[:], resultHash)
	if clientAppliedAt != nil {
		value := clientAppliedAt.UTC()
		result.ClientAppliedAt = &value
	}
	return result, nil
}

func ValidateID(value string) bool { return idPattern.MatchString(value) }

func NewLeaseRequest(owner string, limit int, duration time.Duration) (LeaseRequest, error) {
	owner = strings.TrimSpace(owner)
	if err := validateIdentifier("lease_owner", owner, MaxLeaseOwnerLength); err != nil {
		return LeaseRequest{}, err
	}
	if limit < 1 || limit > MaxLeaseBatchSize {
		return LeaseRequest{}, &ValidationError{
			Field:   "lease_limit",
			Message: fmt.Sprintf("must be between 1 and %d", MaxLeaseBatchSize),
		}
	}
	if duration < MinLeaseDuration || duration > MaxLeaseDuration {
		return LeaseRequest{}, &ValidationError{
			Field:   "lease_duration",
			Message: fmt.Sprintf("must be between %s and %s", MinLeaseDuration, MaxLeaseDuration),
		}
	}
	return LeaseRequest{Owner: owner, Limit: limit, Duration: duration}, nil
}

func NewSubmission(
	producerID string,
	idempotencyKey string,
	deviceID string,
	priority int16,
	payload []byte,
	expiresAt time.Time,
	now time.Time,
) (Submission, error) {
	producerID = strings.TrimSpace(producerID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	deviceID = strings.TrimSpace(deviceID)
	now = now.UTC()
	expiresAt = expiresAt.UTC()

	if err := validateIdentifier("producer_id", producerID, MaxProducerIDLength); err != nil {
		return Submission{}, err
	}
	if idempotencyKey == "" {
		return Submission{}, &ValidationError{Field: "idempotency_key", Message: "must not be empty"}
	}
	if len(idempotencyKey) > MaxIdempotencyKeyLength {
		return Submission{}, &ValidationError{Field: "idempotency_key", Message: fmt.Sprintf("must not exceed %d bytes", MaxIdempotencyKeyLength)}
	}
	if strings.ContainsAny(idempotencyKey, "\x00\r\n") {
		return Submission{}, &ValidationError{Field: "idempotency_key", Message: "must not contain NUL or line breaks"}
	}
	if err := validateIdentifier("device_id", deviceID, MaxDeviceIDLength); err != nil {
		return Submission{}, err
	}
	if priority < MinPriority || priority > MaxPriority {
		return Submission{}, &ValidationError{Field: "priority", Message: fmt.Sprintf("must be between %d and %d", MinPriority, MaxPriority)}
	}
	if len(payload) == 0 {
		return Submission{}, &ValidationError{Field: "payload", Message: "must not be empty"}
	}
	if len(payload) > MaxPayloadBytes {
		return Submission{}, &ValidationError{Field: "payload", Message: fmt.Sprintf("must not exceed %d bytes", MaxPayloadBytes)}
	}
	if !expiresAt.After(now) {
		return Submission{}, &ValidationError{Field: "expires_at", Message: "must be after the server time"}
	}

	payloadCopy := append([]byte(nil), payload...)
	submission := Submission{
		ProducerID:     producerID,
		IdempotencyKey: idempotencyKey,
		DeviceID:       deviceID,
		Priority:       priority,
		Payload:        payloadCopy,
		ExpiresAt:      expiresAt,
		PayloadHash:    sha256.Sum256(payloadCopy),
	}
	submission.RequestHash = hashSubmission(submission)
	return submission, nil
}

func (s State) IsKnown() bool {
	switch s {
	case StateQueued, StateLeased, StateInFlight, StateRetryWait,
		StateAcknowledged, StateExpired, StateDeadLetter, StateCancelled:
		return true
	default:
		return false
	}
}

func (s State) IsTerminal() bool {
	switch s {
	case StateAcknowledged, StateExpired, StateDeadLetter, StateCancelled:
		return true
	default:
		return false
	}
}

func ValidateTransition(from, to State) error {
	if _, ok := allowedTransitions[from][to]; !ok {
		return &TransitionError{From: from, To: to}
	}
	return nil
}

func validateIdentifier(field, value string, maxLength int) error {
	if value == "" {
		return &ValidationError{Field: field, Message: "must not be empty"}
	}
	if len(value) > maxLength {
		return &ValidationError{Field: field, Message: fmt.Sprintf("must not exceed %d bytes", maxLength)}
	}
	if !identifierPattern.MatchString(value) {
		return &ValidationError{Field: field, Message: "contains unsupported characters"}
	}
	return nil
}

func hashSubmission(submission Submission) [sha256.Size]byte {
	hash := sha256.New()
	writeHashField(hash, []byte(submission.DeviceID))
	var priority [2]byte
	binary.BigEndian.PutUint16(priority[:], uint16(submission.Priority))
	writeHashField(hash, priority[:])
	writeHashField(hash, submission.Payload)
	var expiresAt [8]byte
	binary.BigEndian.PutUint64(expiresAt[:], uint64(submission.ExpiresAt.UnixNano()))
	writeHashField(hash, expiresAt[:])

	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

type hashWriter interface {
	Write([]byte) (int, error)
}

func writeHashField(hash hashWriter, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write(value)
}
