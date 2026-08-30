package command

import "errors"

const (
	DefaultGlobalAdmissionLimit   = 10_000
	DefaultPerDeviceAdmissionLimit = 256
)

// AdmissionLimits bound durable outstanding work accepted by SubmitCommand.
type AdmissionLimits struct {
	GlobalMax   int
	PerDeviceMax int
}

func DefaultAdmissionLimits() AdmissionLimits {
	return AdmissionLimits{
		GlobalMax:    DefaultGlobalAdmissionLimit,
		PerDeviceMax: DefaultPerDeviceAdmissionLimit,
	}
}

func (l AdmissionLimits) Validate() error {
	if l.GlobalMax < 1 || l.GlobalMax > 1_000_000 {
		return errors.New("global admission limit must be between 1 and 1000000")
	}
	if l.PerDeviceMax < 1 || l.PerDeviceMax > 100_000 {
		return errors.New("per-device admission limit must be between 1 and 100000")
	}
	if l.PerDeviceMax > l.GlobalMax {
		return errors.New("per-device admission limit must not exceed the global limit")
	}
	return nil
}
