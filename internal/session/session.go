package session

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const MaxIdentifierLength = 64

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

var ErrStale = errors.New("device session is stale or no longer active")

type Session struct {
	DeviceID         string
	GatewayID        string
	ClientInstanceID string
	Epoch            int64
	AcquiredAt       time.Time
}

type Acquisition struct {
	DeviceID         string
	GatewayID        string
	ClientInstanceID string
}

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Message) }

func NewAcquisition(deviceID, gatewayID, clientInstanceID string) (Acquisition, error) {
	values := []struct {
		field string
		value string
	}{
		{field: "device_id", value: strings.TrimSpace(deviceID)},
		{field: "gateway_id", value: strings.TrimSpace(gatewayID)},
		{field: "client_instance_id", value: strings.TrimSpace(clientInstanceID)},
	}
	for _, item := range values {
		if _, err := NormalizeIdentifier(item.field, item.value); err != nil {
			return Acquisition{}, err
		}
	}
	return Acquisition{
		DeviceID:         values[0].value,
		GatewayID:        values[1].value,
		ClientInstanceID: values[2].value,
	}, nil
}

func NormalizeIdentifier(field, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", &ValidationError{Field: field, Message: "must not be empty"}
	}
	if len(value) > MaxIdentifierLength {
		return "", &ValidationError{Field: field, Message: fmt.Sprintf("must not exceed %d bytes", MaxIdentifierLength)}
	}
	if !identifierPattern.MatchString(value) {
		return "", &ValidationError{Field: field, Message: "contains unsupported characters"}
	}
	return value, nil
}
