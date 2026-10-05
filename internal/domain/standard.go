package domain

import (
	"fmt"
	"strings"
	"time"
)

// MaxChainDepth bounds the walk from a standard to its root basis so a
// pathological registration cannot stall verification.
const MaxChainDepth = 64

type StandardStatus string

const (
	StandardActive      StandardStatus = "active"
	StandardDeactivated StandardStatus = "deactivated"
)

// Standard is a registered reference standard. Its superior basis, validity
// interval, and deactivation status describe where its calibration traces to
// and whether it may currently be referenced.
type Standard struct {
	ID            string         `json:"id"`
	Code          string         `json:"code"`
	SuperiorID    string         `json:"superior_id,omitempty"`
	ValidFrom     time.Time      `json:"valid_from"`
	ValidUntil    time.Time      `json:"valid_until"`
	Status        StandardStatus `json:"status"`
	Steward       string         `json:"steward"`
	RegisteredAt  time.Time      `json:"registered_at"`
	DeactivatedAt *time.Time     `json:"deactivated_at,omitempty"`
}

func NewStandard(id, code, superiorID string, validFrom, validUntil time.Time, steward string, at time.Time) (Standard, error) {
	item := Standard{
		ID:           strings.TrimSpace(id),
		Code:         NormalizeStandardCode(code),
		SuperiorID:   strings.TrimSpace(superiorID),
		ValidFrom:    validFrom.UTC(),
		ValidUntil:   validUntil.UTC(),
		Status:       StandardActive,
		Steward:      strings.TrimSpace(steward),
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
	if s.ID == "" || s.Code == "" || s.Steward == "" {
		return fmt.Errorf("%w: standard id, code, and steward are required", ErrInvalidInput)
	}
	if len(s.Code) > 64 {
		return fmt.Errorf("%w: standard code is too long", ErrInvalidInput)
	}
	if s.ValidFrom.IsZero() || s.ValidUntil.IsZero() || !s.ValidFrom.Before(s.ValidUntil) {
		return fmt.Errorf("%w: standard validity interval must be bounded and valid_from must precede valid_until", ErrInvalidInput)
	}
	if s.RegisteredAt.IsZero() {
		return fmt.Errorf("%w: registration time is required", ErrInvalidInput)
	}
	switch s.Status {
	case StandardActive, StandardDeactivated:
		return nil
	default:
		return fmt.Errorf("%w: unsupported standard status %q", ErrInvalidInput, s.Status)
	}
}

func (s Standard) IsActive() bool {
	return s.Status == StandardActive
}

// AmendBasis re-anchors the superior basis and validity interval of an active
// standard. Formed case bases are snapshots and are never rewritten by an
// amendment; the new link only affects verifications resolved after it.
func (s *Standard) AmendBasis(superiorID string, validFrom, validUntil time.Time) error {
	if s.Status != StandardActive {
		return fmt.Errorf("%w: only an active standard can be amended", ErrStateConflict)
	}
	if validFrom.IsZero() || validUntil.IsZero() || !validFrom.Before(validUntil) {
		return fmt.Errorf("%w: validity interval must be bounded and valid_from must precede valid_until", ErrInvalidInput)
	}
	s.SuperiorID = strings.TrimSpace(superiorID)
	s.ValidFrom = validFrom.UTC()
	s.ValidUntil = validUntil.UTC()
	return nil
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

// BasisLink is one immutable hop of a resolved traceability chain.
type BasisLink struct {
	StandardID string    `json:"standard_id"`
	Code       string    `json:"code"`
	SuperiorID string    `json:"superior_id,omitempty"`
	ValidFrom  time.Time `json:"valid_from"`
	ValidUntil time.Time `json:"valid_until"`
}

func (s Standard) BasisLink() BasisLink {
	return BasisLink{
		StandardID: s.ID,
		Code:       s.Code,
		SuperiorID: s.SuperiorID,
		ValidFrom:  s.ValidFrom,
		ValidUntil: s.ValidUntil,
	}
}

// CaseBasis is the traceability chain snapshot fixed onto a verification case
// when it opens. It never changes afterwards, so historical evidence reads as
// the facts known at that time.
type CaseBasis struct {
	StandardID string      `json:"standard_id"`
	ResolvedAt time.Time   `json:"resolved_at"`
	Links      []BasisLink `json:"links"`
}

func (b CaseBasis) Validate() error {
	if b.StandardID == "" || b.ResolvedAt.IsZero() {
		return fmt.Errorf("%w: case basis standard and resolution time are required", ErrInvalidInput)
	}
	return ValidateChainLinks(b.StandardID, b.Links)
}

// ValidateChainLinks requires a contiguous chain that starts at standardID
// and terminates at a root basis with no superior reference.
func ValidateChainLinks(standardID string, links []BasisLink) error {
	if len(links) == 0 {
		return fmt.Errorf("%w: traceability chain must reach a root basis", ErrInvalidInput)
	}
	if links[0].StandardID != standardID {
		return fmt.Errorf("%w: traceability chain must start at the associated standard", ErrInvalidInput)
	}
	for index, link := range links {
		if link.StandardID == "" || link.Code == "" {
			return fmt.Errorf("%w: chain link standard id and code are required", ErrInvalidInput)
		}
		if link.ValidFrom.IsZero() || link.ValidUntil.IsZero() || !link.ValidFrom.Before(link.ValidUntil) {
			return fmt.Errorf("%w: chain link validity interval is invalid", ErrInvalidInput)
		}
		if index+1 < len(links) && link.SuperiorID != links[index+1].StandardID {
			return fmt.Errorf("%w: chain links must reference the next standard", ErrInvalidInput)
		}
	}
	if links[len(links)-1].SuperiorID != "" {
		return fmt.Errorf("%w: traceability chain must end at a root basis", ErrInvalidInput)
	}
	return nil
}

// ResolveChain walks superior references from startID up to the root basis
// and verifies that every hop currently exists, is active, and is inside its
// validity interval at the given time. A missing reference, a deactivated or
// expired standard, or a cycle fails with ErrChainBroken.
func ResolveChain(startID string, at time.Time, lookup func(string) (Standard, bool)) ([]BasisLink, error) {
	current := strings.TrimSpace(startID)
	if current == "" {
		return nil, fmt.Errorf("%w: standard id is required", ErrInvalidInput)
	}
	moment := at.UTC()
	var links []BasisLink
	visited := make(map[string]bool)
	for {
		if len(links) >= MaxChainDepth {
			return nil, fmt.Errorf("%w: chain exceeds maximum depth %d", ErrChainBroken, MaxChainDepth)
		}
		if visited[current] {
			return nil, fmt.Errorf("%w: chain forms a cycle at standard %s", ErrChainBroken, current)
		}
		visited[current] = true
		standard, exists := lookup(current)
		if !exists {
			return nil, fmt.Errorf("%w: chain reference %s is not registered", ErrChainBroken, current)
		}
		if !standard.IsActive() {
			return nil, fmt.Errorf("%w: standard %s is %s", ErrChainBroken, standard.ID, standard.Status)
		}
		if moment.Before(standard.ValidFrom) || moment.After(standard.ValidUntil) {
			return nil, fmt.Errorf("%w: standard %s is outside its validity interval", ErrChainBroken, standard.ID)
		}
		links = append(links, standard.BasisLink())
		if standard.SuperiorID == "" {
			return links, nil
		}
		current = standard.SuperiorID
	}
}
