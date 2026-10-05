package domain

import (
	"errors"
	"fmt"
	"time"
)

type TraceRole string

const (
	TraceRoleWorking TraceRole = "working"
	TraceRoleParent  TraceRole = "parent"
	TraceRoleRoot    TraceRole = "root"
)

// TraceLink is one frozen hop of a calibration chain, ordered from the
// working standard (the standard used by the case) up to the root basis.
type TraceLink struct {
	StandardID   string         `json:"standard_id"`
	Code         string         `json:"code"`
	Name         string         `json:"name"`
	ParentID     string         `json:"parent_id,omitempty"`
	Role         TraceRole      `json:"role"`
	ValidFrom    time.Time      `json:"valid_from"`
	ValidThrough *time.Time     `json:"valid_through,omitempty"`
	Status       StandardStatus `json:"status"`
}

var (
	// ErrTraceBroken means a standard names a higher-level standard that no
	// longer exists in the register.
	ErrTraceBroken = errors.New("calibration chain is broken")
	// ErrTraceCycle means parent references form a loop instead of a chain.
	ErrTraceCycle = errors.New("calibration chain contains a cycle")
	// ErrStandardInactive means a standard on the chain has been deactivated.
	ErrStandardInactive = errors.New("standard is deactivated")
	// ErrStandardExpired means a standard on the chain is outside its validity
	// interval at the checked instant.
	ErrStandardExpired = errors.New("standard is outside its validity interval")
)

// StandardLookup resolves a standard by ID. Both the live mutation set and the
// read-only repository snapshot satisfy it.
type StandardLookup interface {
	StandardByID(id string) (Standard, bool)
}

// ResolveTrace walks the parent chain starting at standardID and returns the
// standards in chain order (working standard first, root basis last). At at
// every hop it checks existence, cycle absence, and usability at the given
// instant, so a call that succeeds certifies a complete, valid chain to a root.
func ResolveTrace(lookup StandardLookup, standardID string, at time.Time) ([]Standard, error) {
	chain := make([]Standard, 0, 4)
	visited := make(map[string]struct{}, 4)
	currentID := standardID
	for {
		if currentID == "" {
			return nil, fmt.Errorf("%w: empty chain node", ErrTraceBroken)
		}
		if _, seen := visited[currentID]; seen {
			return nil, fmt.Errorf("%w: standard %s reached again", ErrTraceCycle, currentID)
		}
		item, exists := lookup.StandardByID(currentID)
		if !exists {
			return nil, fmt.Errorf("%w: standard %s is missing", ErrTraceBroken, currentID)
		}
		visited[item.ID] = struct{}{}
		if item.Status != StandardActive {
			return nil, fmt.Errorf("%w: %s", ErrStandardInactive, item.Code)
		}
		if !item.UsableAt(at) {
			return nil, fmt.Errorf("%w: %s", ErrStandardExpired, item.Code)
		}
		chain = append(chain, item)
		if item.ParentID == "" {
			return chain, nil
		}
		currentID = item.ParentID
	}
}

// FreezeTrace converts a resolved chain into an immutable link sequence for an
// evidence seal. The resolved chain is trusted: it exists, is acyclic, and was
// usable when resolved.
func FreezeTrace(chain []Standard) ([]TraceLink, error) {
	if len(chain) == 0 {
		return nil, fmt.Errorf("%w: trace must contain at least one standard", ErrTraceBroken)
	}
	links := make([]TraceLink, 0, len(chain))
	for index, item := range chain {
		role := TraceRoleParent
		switch {
		case index == len(chain)-1:
			role = TraceRoleRoot
		case index == 0:
			role = TraceRoleWorking
		}
		links = append(links, TraceLink{
			StandardID:   item.ID,
			Code:         item.Code,
			Name:         item.Name,
			ParentID:     item.ParentID,
			Role:         role,
			ValidFrom:    item.ValidFrom,
			ValidThrough: cloneTimePointer(item.ValidThrough),
			Status:       item.Status,
		})
	}
	return links, nil
}

// TraceReason maps a chain failure to a stable, machine-readable reason code
// embedded in case views.
func TraceReason(err error) string {
	switch {
	case errors.Is(err, ErrTraceBroken):
		return "trace_broken"
	case errors.Is(err, ErrTraceCycle):
		return "trace_cycle"
	case errors.Is(err, ErrStandardInactive):
		return "standard_inactive"
	case errors.Is(err, ErrStandardExpired):
		return "standard_expired"
	default:
		return "trace_unavailable"
	}
}
