package assurance

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/contracts"
	"example.com/solo-0021-metrology-assurance-suite/internal/domain"
	"example.com/solo-0021-metrology-assurance-suite/internal/repository"
)

type fixture struct {
	service *Service
	clock   time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	clock := time.Date(2024, 6, 1, 9, 0, 0, 0, time.UTC)
	repo := repository.New()
	service := NewService(repo)
	f := &fixture{service: service, clock: clock}
	// Indirect through the fixture so advance() moves the service clock too.
	service.now = func() time.Time { return f.clock }
	return f
}

func (f *fixture) advance(d time.Duration) { f.clock = f.clock.Add(d) }

func (f *fixture) device(t *testing.T, tag string) string {
	t.Helper()
	device, err := f.service.RegisterDevice(context.Background(), contracts.RegisterDeviceRequest{
		AssetTag: tag, Model: "Bench Meter", Room: "Room A", Steward: "Lin",
	})
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	return device.ID
}

// rootStandard registers an open-ended root basis valid from the fixed clock.
func (f *fixture) rootStandard(t *testing.T, code string, validThrough time.Time) string {
	t.Helper()
	from := f.clock.Add(-time.Hour).UTC().Format(time.RFC3339)
	body := contracts.RegisterStandardRequest{
		Code: code, Name: "Standard " + code, ValidFrom: from, Custodian: "Lin",
	}
	if !validThrough.IsZero() {
		body.ValidThrough = validThrough.UTC().Format(time.RFC3339)
	}
	standard, err := f.service.RegisterStandard(context.Background(), body)
	if err != nil {
		t.Fatalf("RegisterStandard root: %v", err)
	}
	return standard.ID
}

func (f *fixture) childStandard(t *testing.T, code, parent string) string {
	t.Helper()
	standard, err := f.service.RegisterStandard(context.Background(), contracts.RegisterStandardRequest{
		Code:      code,
		Name:      "Standard " + code,
		ParentID:  parent,
		ValidFrom: f.clock.Add(-time.Hour).UTC().Format(time.RFC3339),
		Custodian: "Lin",
	})
	if err != nil {
		t.Fatalf("RegisterStandard child: %v", err)
	}
	return standard.ID
}

func (f *fixture) openCase(t *testing.T, deviceID, standardID string) contracts.CaseResponse {
	t.Helper()
	view, err := f.service.OpenCase(context.Background(), contracts.OpenCaseRequest{
		DeviceID: deviceID, StandardID: standardID, Nominal: 25, Tolerance: 0.5, Steward: "Qiao",
	})
	if err != nil {
		t.Fatalf("OpenCase: %v", err)
	}
	return view
}

func expectErrorIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func TestRegisterStandardRejectsDuplicateAndBrokenParent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.service.RegisterStandard(ctx, contracts.RegisterStandardRequest{
		Code: "STD-1", Name: "Root", ValidFrom: f.clock.Format(time.RFC3339), Custodian: "Lin",
	}); err != nil {
		t.Fatalf("RegisterStandard: %v", err)
	}
	_, err := f.service.RegisterStandard(ctx, contracts.RegisterStandardRequest{
		Code: "std-1", Name: "Duplicate", ValidFrom: f.clock.Format(time.RFC3339), Custodian: "Lin",
	})
	expectErrorIs(t, err, domain.ErrDuplicateStandard)

	_, err = f.service.RegisterStandard(ctx, contracts.RegisterStandardRequest{
		Code: "STD-2", Name: "Orphan", ParentID: "standard-9999",
		ValidFrom: f.clock.Format(time.RFC3339), Custodian: "Lin",
	})
	expectErrorIs(t, err, domain.ErrTraceBroken)
}

func TestOpenCaseRequiresUsableStandard(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	deviceID := f.device(t, "MET-1")

	// Unknown standard is not found.
	_, err := f.service.OpenCase(ctx, contracts.OpenCaseRequest{
		DeviceID: "device-0001", StandardID: "standard-9999",
		Nominal: 25, Tolerance: 0.5, Steward: "Qiao",
	})
	expectErrorIs(t, err, domain.ErrNotFound)

	// Standard valid only in the past cannot back a new case.
	expired, err := f.service.RegisterStandard(ctx, contracts.RegisterStandardRequest{
		Code: "STD-OLD", Name: "Expired",
		ValidFrom:    f.clock.Add(-2 * time.Hour).UTC().Format(time.RFC3339),
		ValidThrough: f.clock.Add(-time.Hour).UTC().Format(time.RFC3339),
		Custodian:    "Lin",
	})
	if err != nil {
		t.Fatalf("RegisterStandard: %v", err)
	}
	_, err = f.service.OpenCase(ctx, contracts.OpenCaseRequest{
		DeviceID: deviceID, StandardID: expired.ID,
		Nominal: 25, Tolerance: 0.5, Steward: "Qiao",
	})
	expectErrorIs(t, err, domain.ErrStandardExpired)
}

func TestOpenCaseFreezesStandardIdentity(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	root := f.rootStandard(t, "STD-ROOT", time.Time{})
	working := f.childStandard(t, "STD-WORK", root)
	deviceID := f.device(t, "MET-1")
	view := f.openCase(t, deviceID, working)
	if view.Case.StandardID != working || view.Case.StandardCode != "STD-WORK" {
		t.Fatalf("case standard identity = %s/%s", view.Case.StandardID, view.Case.StandardCode)
	}
	if !view.Trace.Valid || len(view.Trace.Links) != 2 {
		t.Fatalf("opened case trace = %+v", view.Trace)
	}
	// Deactivating the standard must not rewrite the already-formed case basis.
	if _, err := f.service.DeactivateStandard(ctx, working, contracts.DeactivateStandardRequest{Actor: "Lin"}); err != nil {
		t.Fatalf("DeactivateStandard: %v", err)
	}
	read, err := f.service.Case(ctx, view.Case.ID)
	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	if read.Case.StandardID != working || read.Case.StandardCode != "STD-WORK" {
		t.Fatalf("case basis rewritten: %s/%s", read.Case.StandardID, read.Case.StandardCode)
	}
}

func TestMeasurementBlockedWhenStandardLapses(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	standardID := f.rootStandard(t, "STD-1", time.Time{})
	deviceID := f.device(t, "MET-1")
	view := f.openCase(t, deviceID, standardID)

	if _, err := f.service.DeactivateStandard(ctx, standardID, contracts.DeactivateStandardRequest{Actor: "Lin"}); err != nil {
		t.Fatalf("DeactivateStandard: %v", err)
	}
	_, err := f.service.RecordMeasurement(ctx, view.Case.ID, contracts.RecordMeasurementRequest{
		Observed: 25.1, Technician: "Rui",
	})
	expectErrorIs(t, err, domain.ErrStandardInactive)

	// No measurement was committed and the case is still awaiting measurement.
	read, _ := f.service.Case(ctx, view.Case.ID)
	if len(read.Measurements) != 0 || read.Case.State != domain.CaseAwaitingMeasurement {
		t.Fatalf("blocked measurement mutated the case: %+v", read.Case)
	}
}

func TestSealBlockedWhenParentStandardLapses(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	root := f.rootStandard(t, "STD-ROOT", time.Time{})
	working := f.childStandard(t, "STD-WORK", root)
	deviceID := f.device(t, "MET-1")
	view := f.openCase(t, deviceID, working)
	if _, err := f.service.RecordMeasurement(ctx, view.Case.ID, contracts.RecordMeasurementRequest{
		Observed: 25.2, Technician: "Rui",
	}); err != nil {
		t.Fatalf("RecordMeasurement: %v", err)
	}

	// Withdraw the higher-level basis; the chain is no longer complete.
	if _, err := f.service.DeactivateStandard(ctx, root, contracts.DeactivateStandardRequest{Actor: "Lin"}); err != nil {
		t.Fatalf("DeactivateStandard: %v", err)
	}
	_, err := f.service.SealCase(ctx, view.Case.ID, contracts.SealCaseRequest{
		Reviewer: "Ma", Summary: "must fail",
	})
	expectErrorIs(t, err, domain.ErrStandardInactive)

	if _, exists := readSeal(t, f, view.Case.ID); exists {
		t.Fatal("a seal was created despite the broken chain")
	}
}

func TestSealFreezesTraceAndSurvivesLaterWithdrawal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	root := f.rootStandard(t, "STD-ROOT", time.Time{})
	parent := f.childStandard(t, "STD-REF", root)
	working := f.childStandard(t, "STD-WORK", parent)
	deviceID := f.device(t, "MET-1")
	view := f.openCase(t, deviceID, working)
	if _, err := f.service.RecordMeasurement(ctx, view.Case.ID, contracts.RecordMeasurementRequest{
		Observed: 25.2, Technician: "Rui",
	}); err != nil {
		t.Fatalf("RecordMeasurement: %v", err)
	}
	sealed, err := f.service.SealCase(ctx, view.Case.ID, contracts.SealCaseRequest{
		Reviewer: "Ma", Summary: "complete chain",
	})
	if err != nil {
		t.Fatalf("SealCase: %v", err)
	}
	if sealed.Seal == nil || len(sealed.Seal.Trace) != 3 {
		t.Fatalf("seal did not freeze the three-link chain: %+v", sealed.Seal)
	}

	// Withdraw every link after sealing. Historical evidence must still read by
	// the facts frozen at seal time.
	for _, id := range []string{working, parent, root} {
		if _, err := f.service.DeactivateStandard(ctx, id, contracts.DeactivateStandardRequest{Actor: "Lin"}); err != nil {
			t.Fatalf("DeactivateStandard %s: %v", id, err)
		}
	}
	read, err := f.service.Case(ctx, view.Case.ID)
	if err != nil {
		t.Fatalf("Case after withdrawal: %v", err)
	}
	if read.Case.State != domain.CaseSealed {
		t.Fatalf("sealed case state changed to %q", read.Case.State)
	}
	if !read.Trace.Valid || len(read.Trace.Links) != 3 {
		t.Fatalf("historical trace altered after withdrawal: %+v", read.Trace)
	}
	for _, link := range read.Trace.Links {
		if link.Status != domain.StandardActive {
			t.Fatalf("frozen link %s rewritten to status %q", link.Code, link.Status)
		}
	}

	// A sealed case still rejects further measurements.
	_, err = f.service.RecordMeasurement(ctx, view.Case.ID, contracts.RecordMeasurementRequest{
		Observed: 25.0, Technician: "Rui",
	})
	expectErrorIs(t, err, domain.ErrStateConflict)
}

func TestExpiredIntervalBlocksMeasurementByClock(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// Valid for one hour from the fixed clock.
	standardID := f.rootStandard(t, "STD-WINDOW", f.clock.Add(time.Hour))
	deviceID := f.device(t, "MET-1")
	view := f.openCase(t, deviceID, standardID)

	f.advance(30 * time.Minute)
	if _, err := f.service.RecordMeasurement(ctx, view.Case.ID, contracts.RecordMeasurementRequest{
		Observed: 26.5, Technician: "Rui",
	}); err != nil {
		t.Fatalf("measurement inside window should be accepted: %v", err)
	}
	view, _ = f.service.Case(ctx, view.Case.ID)
	if view.Case.State != domain.CaseRecheckRequired {
		t.Fatalf("state = %q, want recheck_required", view.Case.State)
	}
	if _, err := f.service.ReopenCase(ctx, view.Case.ID, contracts.ReopenCaseRequest{
		Actor: "Lin", Reason: "retry",
	}); err != nil {
		t.Fatalf("ReopenCase: %v", err)
	}

	// After the window closes a repeat measurement is refused.
	f.advance(2 * time.Hour)
	_, err := f.service.RecordMeasurement(ctx, view.Case.ID, contracts.RecordMeasurementRequest{
		Observed: 25.1, Technician: "Rui",
	})
	expectErrorIs(t, err, domain.ErrStandardExpired)
}

func readSeal(t *testing.T, f *fixture, caseID string) (domain.EvidenceSeal, bool) {
	t.Helper()
	read, err := f.service.Case(context.Background(), caseID)
	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	if read.Seal == nil {
		return domain.EvidenceSeal{}, false
	}
	return *read.Seal, true
}
