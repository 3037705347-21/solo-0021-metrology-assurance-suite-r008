package domain

import (
	"errors"
	"testing"
	"time"
)

type testLookup map[string]Standard

func (l testLookup) StandardByID(id string) (Standard, bool) {
	item, ok := l[id]
	return item, ok
}

func testStandard(id, parent string, status StandardStatus, from, through time.Time) Standard {
	item := Standard{
		ID:           id,
		Code:         id,
		Name:         id,
		ParentID:     parent,
		ValidFrom:    from,
		ValidThrough: nil,
		Status:       status,
		Custodian:    "Lin",
		RegisteredAt: from,
	}
	if !through.IsZero() {
		bound := through
		item.ValidThrough = &bound
	}
	return item
}

func TestResolveTraceWalksWorkingParentRoot(t *testing.T) {
	from := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	at := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	lookup := testLookup{
		"root":    testStandard("root", "", StandardActive, from, time.Time{}),
		"parent":  testStandard("parent", "root", StandardActive, from, time.Time{}),
		"working": testStandard("working", "parent", StandardActive, from, time.Time{}),
	}
	chain, err := ResolveTrace(lookup, "working", at)
	if err != nil {
		t.Fatalf("ResolveTrace returned error: %v", err)
	}
	got := []string{chain[0].ID, chain[1].ID, chain[2].ID}
	want := []string{"working", "parent", "root"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("chain[%d] = %q, want %q (full chain %v)", index, got[index], want[index], got)
		}
	}
}

func TestResolveTraceRejectsCycle(t *testing.T) {
	from := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	at := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	// a -> b -> c -> a: registration cannot create this through the service, but
	// ResolveTrace must still refuse to follow it.
	lookup := testLookup{
		"a": testStandard("a", "b", StandardActive, from, time.Time{}),
		"b": testStandard("b", "c", StandardActive, from, time.Time{}),
		"c": testStandard("c", "a", StandardActive, from, time.Time{}),
	}
	if _, err := ResolveTrace(lookup, "a", at); !errors.Is(err, ErrTraceCycle) {
		t.Fatalf("cycle error = %v, want ErrTraceCycle", err)
	}
}

func TestResolveTraceRejectsMissingParent(t *testing.T) {
	from := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	at := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	lookup := testLookup{
		"working": testStandard("working", "gone", StandardActive, from, time.Time{}),
	}
	if _, err := ResolveTrace(lookup, "working", at); !errors.Is(err, ErrTraceBroken) {
		t.Fatalf("broken chain error = %v, want ErrTraceBroken", err)
	}
}

func TestResolveTraceRejectsMissingStart(t *testing.T) {
	at := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	if _, err := ResolveTrace(testLookup{}, "unknown", at); !errors.Is(err, ErrTraceBroken) {
		t.Fatalf("missing start error = %v, want ErrTraceBroken", err)
	}
}

func TestResolveTraceRejectsDeactivatedAndExpired(t *testing.T) {
	from := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	through := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	at := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)

	deactivated := testLookup{
		"root": testStandard("root", "", StandardDeactivated, from, time.Time{}),
	}
	if _, err := ResolveTrace(deactivated, "root", at); !errors.Is(err, ErrStandardInactive) {
		t.Fatalf("deactivated error = %v, want ErrStandardInactive", err)
	}

	expired := testLookup{
		"root": testStandard("root", "", StandardActive, from, through),
	}
	if _, err := ResolveTrace(expired, "root", at); !errors.Is(err, ErrStandardExpired) {
		t.Fatalf("expired error = %v, want ErrStandardExpired", err)
	}

	// The upper boundary is inclusive: exactly at valid_through it is usable.
	if _, err := ResolveTrace(expired, "root", through); err != nil {
		t.Fatalf("standard at valid_through boundary should be usable, got %v", err)
	}
}

func TestFreezeTraceAssignsRolesAndIsImmutable(t *testing.T) {
	from := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	at := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	lookup := testLookup{
		"root":    testStandard("root", "", StandardActive, from, time.Time{}),
		"working": testStandard("working", "root", StandardActive, from, time.Time{}),
	}
	chain, err := ResolveTrace(lookup, "working", at)
	if err != nil {
		t.Fatalf("ResolveTrace: %v", err)
	}
	links, err := FreezeTrace(chain)
	if err != nil {
		t.Fatalf("FreezeTrace: %v", err)
	}
	if links[0].Role != TraceRoleWorking || links[1].Role != TraceRoleRoot {
		t.Fatalf("roles = %q, %q; want working, root", links[0].Role, links[1].Role)
	}
	if links[0].ParentID != "root" || links[1].ParentID != "" {
		t.Fatalf("parent links not frozen: %+v", links)
	}
}

func TestFreezeTraceRejectsEmptyChain(t *testing.T) {
	if _, err := FreezeTrace(nil); !errors.Is(err, ErrTraceBroken) {
		t.Fatalf("empty freeze error = %v, want ErrTraceBroken", err)
	}
}

func TestStandardUsableAtRespectsIntervalAndStatus(t *testing.T) {
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	through := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	active := testStandard("s", "", StandardActive, from, through)
	if !active.UsableAt(from) {
		t.Error("standard should be usable at valid_from")
	}
	if !active.UsableAt(through) {
		t.Error("standard should be usable at valid_through")
	}
	if active.UsableAt(from.Add(-time.Second)) {
		t.Error("standard should not be usable before valid_from")
	}
	if active.UsableAt(through.Add(time.Second)) {
		t.Error("standard should not be usable after valid_through")
	}
	active.Status = StandardDeactivated
	if active.UsableAt(from.Add(time.Hour)) {
		t.Error("deactivated standard must not be usable inside its interval")
	}
}

func TestDeactivateIsOneWay(t *testing.T) {
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	at := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	item := testStandard("s", "", StandardActive, from, time.Time{})
	if err := item.Deactivate(at); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	if item.Status != StandardDeactivated || item.DeactivatedAt == nil {
		t.Fatalf("standard not deactivated: %+v", item)
	}
	if err := item.Deactivate(at); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("second deactivate error = %v, want ErrStateConflict", err)
	}
}

func TestNewStandardRejectsInvertedInterval(t *testing.T) {
	from := time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC)
	through := from.Add(-time.Hour)
	_, err := NewStandard("s", "S", "name", "", from, &through, "Lin", from)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("inverted interval error = %v, want ErrInvalidInput", err)
	}
}
