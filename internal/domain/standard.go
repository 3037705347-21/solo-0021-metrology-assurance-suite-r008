package domain

import (
	"fmt"
	"strings"
	"time"
)

type StandardStatus string

const (
	StandardActive      StandardStatus = "active"
	StandardDeactivated StandardStatus = "deactivated"
)

// Standard is a measurement standard (reference standard or working
// standard). Its ParentID points at the higher-level standard it was
// calibrated against; an empty ParentID marks a root basis. The interval and
// deactivation state determine when the standard may back new measurements.
type Standard struct {
	ID            string         `json:"id"`
	Code          string         `json:"code"`
	Name          string         `json:"name"`
	ParentID      string         `json:"parent_id,omitempty"`
	ValidFrom     time.Time      `json:"valid_from"`
	ValidThrough  *time.Time     `json:"valid_through,omitempty"`
	Status        StandardStatus `json:"status"`
	Custodian     string         `json:"custodian"`
	RegisteredAt  time.Time      `json:"registered_at"`
	DeactivatedAt *time.Time     `json:"deactivated_at,omitempty"`
}

func NewStandard(id, code, name, parentID string, validFrom time.Time, validThrough *time.Time, custodian string, at time.Time) (Standard, error) {
	item := Standard{
		ID:           strings.TrimSpace(id),
		Code:         NormalizeStandardCode(code),
		Name:         strings.TrimSpace(name),
		ParentID:     strings.TrimSpace(parentID),
		ValidFrom:    validFrom.UTC(),
		ValidThrough: cloneTimePointer(validThrough),
		Status:       StandardActive,
		Custodian:    strings.TrimSpace(custodian),
		RegisteredAt: at.UTC(),
	}
	if err := item.Validate(); err != nil {
		return Standard{}, err
	}
	return item, nil
}

func NormalizeStandardCode(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func (s Standard) Validate() error {
	if s.ID == "" || s.Code == "" || s.Name == "" || s.Custodian == "" {
		return fmt.Errorf("%w: standard id, code, name, and custodian are required", ErrInvalidInput)
	}
	if len(s.Code) > 64 {
		return fmt.Errorf("%w: standard code is too long", ErrInvalidInput)
	}
	if s.ValidFrom.IsZero() {
		return fmt.Errorf("%w: valid_from is required", ErrInvalidInput)
	}
	if s.ValidThrough != nil && !s.ValidThrough.After(s.ValidFrom) {
		return fmt.Errorf("%w: valid_through must be after valid_from", ErrInvalidInput)
	}
	if s.RegisteredAt.IsZero() {
		return fmt.Errorf("%w: registration time is required", ErrInvalidInput)
	}
	switch s.Status {
	case StandardActive, StandardDeactivated:
		return nil
	default:
		return fmt.Errorf("%w: unsupported standard state %q", ErrInvalidInput, s.Status)
	}
}

// UsableAt reports whether the standard may back a measurement taken at the
// given instant: it must be active and inside its validity interval. The
// interval boundaries are inclusive; a nil ValidThrough means open-ended.
func (s Standard) UsableAt(at time.Time) bool {
	stamp := at.UTC()
	if s.Status != StandardActive {
		return false
	}
	if stamp.Before(s.ValidFrom) {
		return false
	}
	if s.ValidThrough != nil && stamp.After(*s.ValidThrough) {
		return false
	}
	return true
}

func (s *Standard) Deactivate(at time.Time) error {
	if s.Status != StandardActive {
		return fmt.Errorf("%w: only an active standard can be deactivated", ErrStateConflict)
	}
	stamp := at.UTC()
	s.Status = StandardDeactivated
	s.DeactivatedAt = &stamp
	return nil
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	stamp := value.UTC()
	return &stamp
}
