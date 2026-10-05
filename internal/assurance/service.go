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
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		if _, exists := m.StandardByCode(code); exists {
			return fmt.Errorf("%w: %s", domain.ErrDuplicateStandardCode, code)
		}
		item, err := domain.NewStandard(
			m.NextID("standard"),
			input.Code,
			input.SuperiorID,
			input.ValidFrom,
			input.ValidUntil,
			input.Steward,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := m.PutStandard(item); err != nil {
			return err
		}
		detail := "standard registered as a root basis"
		if item.SuperiorID != "" {
			detail = fmt.Sprintf("standard registered under superior basis %s", item.SuperiorID)
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"standard",
			item.ID,
			"",
			"standard_registered",
			item.Steward,
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

func (s *Service) Standard(ctx context.Context, id string) (domain.Standard, error) {
	var found domain.Standard
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		item, exists := snapshot.Standard(strings.TrimSpace(id))
		if !exists {
			return fmt.Errorf("%w: standard %s", domain.ErrNotFound, id)
		}
		found = item
		return nil
	})
	return found, err
}

func (s *Service) AmendStandardBasis(ctx context.Context, id string, input contracts.AmendStandardRequest) (domain.Standard, error) {
	var updated domain.Standard
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		item, exists := m.Standard(strings.TrimSpace(id))
		if !exists {
			return fmt.Errorf("%w: standard %s", domain.ErrNotFound, id)
		}
		if err := item.AmendBasis(input.SuperiorID, input.ValidFrom, input.ValidUntil); err != nil {
			return err
		}
		if err := m.UpdateStandard(item); err != nil {
			return err
		}
		actor := strings.TrimSpace(input.Actor)
		if actor == "" {
			actor = item.Steward
		}
		detail := fmt.Sprintf("standard basis amended: superior %s, validity %s to %s",
			item.SuperiorID,
			item.ValidFrom.Format(time.RFC3339),
			item.ValidUntil.Format(time.RFC3339),
		)
		if item.SuperiorID == "" {
			detail = fmt.Sprintf("standard basis amended: re-anchored as root, validity %s to %s",
				item.ValidFrom.Format(time.RFC3339),
				item.ValidUntil.Format(time.RFC3339),
			)
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"standard",
			item.ID,
			"",
			"standard_amended",
			actor,
			detail,
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

func (s *Service) DeactivateStandard(ctx context.Context, id string, input contracts.DeactivateStandardRequest) (domain.Standard, error) {
	var updated domain.Standard
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		item, exists := m.Standard(strings.TrimSpace(id))
		if !exists {
			return fmt.Errorf("%w: standard %s", domain.ErrNotFound, id)
		}
		if err := item.Deactivate(s.now()); err != nil {
			return err
		}
		if err := m.UpdateStandard(item); err != nil {
			return err
		}
		actor := strings.TrimSpace(input.Actor)
		if actor == "" {
			actor = item.Steward
		}
		detail := strings.TrimSpace(input.Reason)
		if detail == "" {
			detail = "standard deactivated"
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"standard",
			item.ID,
			"",
			"standard_deactivated",
			actor,
			detail,
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
		if standardID != "" {
			if _, exists := m.Standard(standardID); !exists {
				return fmt.Errorf("%w: standard %s", domain.ErrNotFound, standardID)
			}
		}
		links, err := domain.ResolveChain(standardID, s.now(), func(linkID string) (domain.Standard, bool) {
			return m.Standard(linkID)
		})
		if err != nil {
			return err
		}
		basis := domain.CaseBasis{
			StandardID: links[0].StandardID,
			ResolvedAt: s.now(),
			Links:      links,
		}
		item, err := domain.NewVerificationCase(
			m.NextID("case"),
			device.ID,
			input.Nominal,
			input.Tolerance,
			input.Steward,
			basis,
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
			fmt.Sprintf("verification case opened against standard %s", item.Basis.StandardID),
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item)
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
		result = caseResponseFromSnapshot(snapshot, item)
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
		if _, err := domain.ResolveChain(item.Basis.StandardID, s.now(), func(linkID string) (domain.Standard, bool) {
			return m.Standard(linkID)
		}); err != nil {
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
		result = caseResponse(m, item)
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
		result = caseResponse(m, item)
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
		measurement, ok := bestAcceptedMeasurement(m.Measurements(item.ID))
		if !ok {
			return fmt.Errorf("%w: an accepted measurement is required before sealing", domain.ErrStateConflict)
		}
		links, err := domain.ResolveChain(item.Basis.StandardID, s.now(), func(linkID string) (domain.Standard, bool) {
			return m.Standard(linkID)
		})
		if err != nil {
			return err
		}
		if err := item.Seal(s.now()); err != nil {
			return err
		}
		seal, err := domain.NewEvidenceSeal(
			m.NextID("seal"),
			item.ID,
			item.DeviceID,
			measurement.ID,
			input.Reviewer,
			input.Summary,
			links,
			s.now(),
		)
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
		result = caseResponse(m, item)
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

func caseResponse(m *repository.Mutation, item domain.VerificationCase) contracts.CaseResponse {
	result := contracts.CaseResponse{
		Case:         item,
		Measurements: m.Measurements(item.ID),
		Events:       m.Events(item.ID),
	}
	if seal, exists := m.Seal(item.ID); exists {
		result.Seal = &seal
	}
	return result
}

func caseResponseFromSnapshot(snapshot repository.Snapshot, item domain.VerificationCase) contracts.CaseResponse {
	result := contracts.CaseResponse{
		Case:         item,
		Measurements: snapshot.Measurements(item.ID),
		Events:       snapshot.Events(item.ID),
	}
	if seal, exists := snapshot.Seal(item.ID); exists {
		result.Seal = &seal
	}
	return result
}
