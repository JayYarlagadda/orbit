package postgres

import (
	"github.com/JayYarlagadda/orbit/internal/command"
)

// StorePolicy configures retry scheduling and submit admission limits.
type StorePolicy struct {
	Retry     command.RetryPolicy
	Admission command.AdmissionLimits
}

func defaultStorePolicy() StorePolicy {
	return StorePolicy{
		Retry:     command.DefaultRetryPolicy(),
		Admission: command.DefaultAdmissionLimits(),
	}
}

func (p StorePolicy) withDefaults() StorePolicy {
	if p.Retry == (command.RetryPolicy{}) {
		p.Retry = command.DefaultRetryPolicy()
	}
	if p.Admission == (command.AdmissionLimits{}) {
		p.Admission = command.DefaultAdmissionLimits()
	}
	return p
}

const (
	retryBudgetExhaustedReason = "retry budget exhausted"
	leaseExpiredReason         = "lease expired"
)

const outstandingStatesSQL = `('QUEUED', 'RETRY_WAIT', 'LEASED', 'IN_FLIGHT')`
