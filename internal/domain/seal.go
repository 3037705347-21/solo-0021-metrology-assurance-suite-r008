package domain

import (
	"fmt"
	"strings"
	"time"
)

type EvidenceSeal struct {
	ID            string      `json:"id"`
	CaseID        string      `json:"case_id"`
	DeviceID      string      `json:"device_id"`
	MeasurementID string      `json:"measurement_id"`
	Reviewer      string      `json:"reviewer"`
	Summary       string      `json:"summary"`
	Trace         []TraceLink `json:"trace"`
	SealedAt      time.Time   `json:"sealed_at"`
}

// SealParams carries the immutable facts frozen into a seal, including the
// complete calibration chain as it stood at sealing time.
type SealParams struct {
	ID            string
	CaseID        string
	DeviceID      string
	MeasurementID string
	Reviewer      string
	Summary       string
	Trace         []TraceLink
	At            time.Time
}

func NewEvidenceSeal(params SealParams) (EvidenceSeal, error) {
	trace := make([]TraceLink, len(params.Trace))
	copy(trace, params.Trace)
	item := EvidenceSeal{
		ID:            strings.TrimSpace(params.ID),
		CaseID:        strings.TrimSpace(params.CaseID),
		DeviceID:      strings.TrimSpace(params.DeviceID),
		MeasurementID: strings.TrimSpace(params.MeasurementID),
		Reviewer:      strings.TrimSpace(params.Reviewer),
		Summary:       strings.TrimSpace(params.Summary),
		Trace:         trace,
		SealedAt:      params.At.UTC(),
	}
	if err := item.Validate(); err != nil {
		return EvidenceSeal{}, err
	}
	return item, nil
}

func (s EvidenceSeal) Validate() error {
	if s.ID == "" || s.CaseID == "" || s.DeviceID == "" || s.MeasurementID == "" || s.Reviewer == "" || s.Summary == "" {
		return fmt.Errorf("%w: seal id, case, device, measurement, reviewer, and summary are required", ErrInvalidInput)
	}
	if len(s.Trace) == 0 {
		return fmt.Errorf("%w: a complete calibration trace to a root basis is required", ErrTraceBroken)
	}
	if s.SealedAt.IsZero() {
		return fmt.Errorf("%w: seal time is required", ErrInvalidInput)
	}
	return nil
}
