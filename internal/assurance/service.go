package assurance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/contracts"
	"example.com/solo-0021-metrology-assurance-suite/internal/domain"
	"example.com/solo-0021-metrology-assurance-suite/internal/repository"
)

type Service struct {
	repository *repository.Repository
	now        func() time.Time
}

func NewService(repo *repository.Repository) *Service {
	return &Service{
		repository: repo,
		now:        time.Now,
	}
}

func (s *Service) RegisterDevice(ctx context.Context, input contracts.RegisterDeviceRequest) (domain.Device, error) {
	var created domain.Device
	tag := domain.NormalizeAssetTag(input.AssetTag)
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		if _, exists := m.DeviceByTag(tag); exists {
			return fmt.Errorf("%w: %s", domain.ErrDuplicateAssetTag, tag)
		}
		item, err := domain.NewDevice(
			m.NextID("device"),
			input.AssetTag,
			input.Model,
			input.Room,
			input.Steward,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := m.PutDevice(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"device",
			item.ID,
			"",
			"device_enrolled",
			item.Steward,
			"device enrolled for assurance work",
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		created = item
		return nil
	})
	return created, err
}

func (s *Service) Device(ctx context.Context, id string) (domain.Device, error) {
	var found domain.Device
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		item, exists := snapshot.Device(strings.TrimSpace(id))
		if !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, id)
		}
		found = item
		return nil
	})
	return found, err
}

func (s *Service) RegisterStandard(ctx context.Context, input contracts.RegisterStandardRequest) (domain.Standard, error) {
	var created domain.Standard
	code := domain.NormalizeStandardCode(input.Code)
	validFrom, validThrough, err := parseStandardInterval(input.ValidFrom, input.ValidThrough)
	if err != nil {
		return created, err
	}
	err = s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		if _, exists := m.StandardByCode(code); exists {
			return fmt.Errorf("%w: %s", domain.ErrDuplicateStandard, code)
		}
		parentID := strings.TrimSpace(input.ParentID)
		if parentID != "" {
			// The higher-level basis and every ancestor above it must be usable
			// now, otherwise a fresh standard would be born on a broken chain.
			if _, err := domain.ResolveTrace(m, parentID, s.now()); err != nil {
				return err
			}
		}
		item, err := domain.NewStandard(
			m.NextID("standard"),
			input.Code,
			input.Name,
			parentID,
			validFrom,
			validThrough,
			input.Custodian,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := m.PutStandard(item); err != nil {
			return err
		}
		detail := "standard registered as a root calibration basis"
		if parentID != "" {
			detail = fmt.Sprintf("standard registered against higher basis %s", parentID)
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"standard",
			item.ID,
			"",
			"standard_registered",
			item.Custodian,
			detail,
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		created = item
		return nil
	})
	return created, err
}

func (s *Service) DeactivateStandard(ctx context.Context, id string, input contracts.DeactivateStandardRequest) (domain.Standard, error) {
	var updated domain.Standard
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		actor := strings.TrimSpace(input.Actor)
		if actor == "" {
			return fmt.Errorf("%w: actor is required", domain.ErrInvalidInput)
		}
		item, exists := m.StandardByID(strings.TrimSpace(id))
		if !exists {
			return fmt.Errorf("%w: standard %s", domain.ErrNotFound, id)
		}
		if err := item.Deactivate(s.now()); err != nil {
			return err
		}
		if err := m.UpdateStandard(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"standard",
			item.ID,
			"",
			"standard_deactivated",
			actor,
			fmt.Sprintf("standard %s removed from service", item.Code),
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		updated = item
		return nil
	})
	return updated, err
}

func parseStandardInterval(from, through string) (time.Time, *time.Time, error) {
	from = strings.TrimSpace(from)
	if from == "" {
		return time.Time{}, nil, fmt.Errorf("%w: valid_from is required", domain.ErrInvalidInput)
	}
	validFrom, err := time.Parse(time.RFC3339, from)
	if err != nil {
		return time.Time{}, nil, fmt.Errorf("%w: valid_from must be RFC3339: %v", domain.ErrInvalidInput, err)
	}
	var validThrough *time.Time
	if trimmed := strings.TrimSpace(through); trimmed != "" {
		parsed, err := time.Parse(time.RFC3339, trimmed)
		if err != nil {
			return time.Time{}, nil, fmt.Errorf("%w: valid_through must be RFC3339: %v", domain.ErrInvalidInput, err)
		}
		parsed = parsed.UTC()
		validThrough = &parsed
	}
	return validFrom.UTC(), validThrough, nil
}

func (s *Service) OpenCase(ctx context.Context, input contracts.OpenCaseRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		deviceID := strings.TrimSpace(input.DeviceID)
		device, exists := m.Device(deviceID)
		if !exists {
			return fmt.Errorf("%w: device %s", domain.ErrNotFound, deviceID)
		}
		if !device.IsActive() {
			return fmt.Errorf("%w: device %s is %s", domain.ErrStateConflict, device.ID, device.Status)
		}
		if active, exists := m.ActiveCaseForDevice(device.ID); exists {
			return fmt.Errorf("%w: case %s is still %s", domain.ErrActiveCaseExists, active.ID, active.State)
		}
		standardID := strings.TrimSpace(input.StandardID)
		if _, exists := m.StandardByID(standardID); !exists {
			return fmt.Errorf("%w: standard %s", domain.ErrNotFound, standardID)
		}
		chain, err := domain.ResolveTrace(m, standardID, s.now())
		if err != nil {
			return err
		}
		standard := chain[0]
		item, err := domain.NewVerificationCase(
			m.NextID("case"),
			device.ID,
			standard.ID,
			standard.Code,
			input.Nominal,
			input.Tolerance,
			input.Steward,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := m.PutCase(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"case",
			item.ID,
			item.ID,
			"case_opened",
			item.Steward,
			fmt.Sprintf("verification case opened against standard %s", standard.Code),
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item, s.now())
		return nil
	})
	return result, err
}

func (s *Service) Case(ctx context.Context, id string) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		item, exists := snapshot.Case(strings.TrimSpace(id))
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		result = caseResponseFromSnapshot(snapshot, item, s.now())
		return nil
	})
	return result, err
}

func (s *Service) RecordMeasurement(ctx context.Context, caseID string, input contracts.RecordMeasurementRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(caseID)
		item, exists := m.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		if item.State != domain.CaseAwaitingMeasurement {
			return fmt.Errorf("%w: case is not awaiting measurement", domain.ErrStateConflict)
		}
		// A standard that lapsed or was deactivated after the case opened blocks
		// any further measurement instead of silently backing new evidence.
		if _, err := domain.ResolveTrace(m, item.StandardID, s.now()); err != nil {
			return err
		}
		measurement, err := domain.NewMeasurement(
			m.NextID("measurement"),
			item.ID,
			input.Observed,
			item.Nominal,
			item.Tolerance,
			input.Technician,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := item.RecordMeasurement(measurement.Accepted, s.now()); err != nil {
			return err
		}
		if err := m.AppendMeasurement(measurement); err != nil {
			return err
		}
		if err := m.UpdateCase(item); err != nil {
			return err
		}
		kind := "measurement_rejected"
		detail := "measurement exceeded tolerance and requires another measurement"
		if measurement.Accepted {
			kind = "measurement_accepted"
			detail = "measurement is within tolerance and ready for review"
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"case",
			item.ID,
			item.ID,
			kind,
			measurement.Technician,
			detail,
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item, s.now())
		return nil
	})
	return result, err
}

func (s *Service) ReopenCase(ctx context.Context, caseID string, input contracts.ReopenCaseRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(caseID)
		item, exists := m.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		if err := item.Reopen(s.now()); err != nil {
			return err
		}
		if err := m.UpdateCase(item); err != nil {
			return err
		}
		actor := strings.TrimSpace(input.Actor)
		reason := strings.TrimSpace(input.Reason)
		if actor == "" || reason == "" {
			return fmt.Errorf("%w: actor and reason are required", domain.ErrInvalidInput)
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"case",
			item.ID,
			item.ID,
			"case_reopened",
			actor,
			reason,
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item, s.now())
		return nil
	})
	return result, err
}

func (s *Service) SealCase(ctx context.Context, caseID string, input contracts.SealCaseRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(caseID)
		item, exists := m.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		if _, exists := m.Seal(item.ID); exists {
			return fmt.Errorf("%w: case %s", domain.ErrSealAlreadyCreated, item.ID)
		}
		// Sealing certifies a complete chain to a root basis that is usable at
		// sealing time. An expired, deactivated, broken, or cyclic basis blocks
		// the seal so no un-traceable evidence is frozen.
		chain, err := domain.ResolveTrace(m, item.StandardID, s.now())
		if err != nil {
			return err
		}
		trace, err := domain.FreezeTrace(chain)
		if err != nil {
			return err
		}
		measurement, ok := bestAcceptedMeasurement(m.Measurements(item.ID))
		if !ok {
			return fmt.Errorf("%w: an accepted measurement is required before sealing", domain.ErrStateConflict)
		}
		if err := item.Seal(s.now()); err != nil {
			return err
		}
		seal, err := domain.NewEvidenceSeal(domain.SealParams{
			ID:            m.NextID("seal"),
			CaseID:        item.ID,
			DeviceID:      item.DeviceID,
			MeasurementID: measurement.ID,
			Reviewer:      input.Reviewer,
			Summary:       input.Summary,
			Trace:         trace,
			At:            s.now(),
		})
		if err != nil {
			return err
		}
		if err := m.PutSeal(seal); err != nil {
			return err
		}
		if err := m.UpdateCase(item); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"case",
			item.ID,
			item.ID,
			"case_sealed",
			seal.Reviewer,
			seal.Summary,
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item, s.now())
		return nil
	})
	return result, err
}

func bestAcceptedMeasurement(items []domain.Measurement) (domain.Measurement, bool) {
	var selected domain.Measurement
	found := false
	for _, item := range items {
		if !item.Accepted {
			continue
		}
		if !found || item.AbsoluteDeviation() < selected.AbsoluteDeviation() {
			selected = item
			found = true
		}
	}
	return selected, found
}

func caseResponse(m *repository.Mutation, item domain.VerificationCase, at time.Time) contracts.CaseResponse {
	result := contracts.CaseResponse{
		Case:         item,
		Measurements: m.Measurements(item.ID),
		Trace:        liveTraceView(m, item, at),
		Events:       m.Events(item.ID),
	}
	if seal, exists := m.Seal(item.ID); exists {
		result.Seal = &seal
		result.Trace = frozenTraceView(seal.Trace)
	}
	return result
}

func caseResponseFromSnapshot(snapshot repository.Snapshot, item domain.VerificationCase, at time.Time) contracts.CaseResponse {
	result := contracts.CaseResponse{
		Case:         item,
		Measurements: snapshot.Measurements(item.ID),
		Trace:        liveTraceView(snapshot, item, at),
		Events:       snapshot.Events(item.ID),
	}
	if seal, exists := snapshot.Seal(item.ID); exists {
		result.Seal = &seal
		result.Trace = frozenTraceView(seal.Trace)
	}
	return result
}

func liveTraceView(lookup domain.StandardLookup, item domain.VerificationCase, at time.Time) contracts.TraceView {
	chain, err := domain.ResolveTrace(lookup, item.StandardID, at)
	if err != nil {
		return contracts.TraceView{Valid: false, Reason: domain.TraceReason(err)}
	}
	trace, err := domain.FreezeTrace(chain)
	if err != nil {
		return contracts.TraceView{Valid: false, Reason: domain.TraceReason(err)}
	}
	return frozenTraceView(trace)
}

func frozenTraceView(links []domain.TraceLink) contracts.TraceView {
	views := make([]contracts.TraceLinkView, 0, len(links))
	for _, link := range links {
		views = append(views, contracts.TraceLinkView{
			StandardID:   link.StandardID,
			Code:         link.Code,
			Name:         link.Name,
			ParentID:     link.ParentID,
			Role:         link.Role,
			ValidFrom:    link.ValidFrom,
			ValidThrough: link.ValidThrough,
			Status:       link.Status,
		})
	}
	return contracts.TraceView{Links: views, Valid: true}
}
