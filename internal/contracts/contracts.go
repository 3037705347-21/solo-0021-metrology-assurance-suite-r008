package contracts

import (
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/domain"
)

type RegisterDeviceRequest struct {
	AssetTag string `json:"asset_tag"`
	Model    string `json:"model"`
	Room     string `json:"room"`
	Steward  string `json:"steward"`
}

type RegisterStandardRequest struct {
	Code       string    `json:"code"`
	SuperiorID string    `json:"superior_id"`
	ValidFrom  time.Time `json:"valid_from"`
	ValidUntil time.Time `json:"valid_until"`
	Steward    string    `json:"steward"`
}

type AmendStandardRequest struct {
	SuperiorID string    `json:"superior_id"`
	ValidFrom  time.Time `json:"valid_from"`
	ValidUntil time.Time `json:"valid_until"`
	Actor      string    `json:"actor"`
}

type DeactivateStandardRequest struct {
	Actor  string `json:"actor"`
	Reason string `json:"reason"`
}

type OpenCaseRequest struct {
	DeviceID   string  `json:"device_id"`
	StandardID string  `json:"standard_id"`
	Nominal    float64 `json:"nominal"`
	Tolerance  float64 `json:"tolerance"`
	Steward    string  `json:"steward"`
}

type RecordMeasurementRequest struct {
	Observed   float64 `json:"observed"`
	Technician string  `json:"technician"`
}

type ReopenCaseRequest struct {
	Actor  string `json:"actor"`
	Reason string `json:"reason"`
}

type SealCaseRequest struct {
	Reviewer string `json:"reviewer"`
	Summary  string `json:"summary"`
}

type DeviceResponse struct {
	Device domain.Device `json:"device"`
}

type StandardResponse struct {
	Standard domain.Standard `json:"standard"`
}

type CaseResponse struct {
	Case         domain.VerificationCase `json:"case"`
	Measurements []domain.Measurement    `json:"measurements"`
	Seal         *domain.EvidenceSeal    `json:"seal,omitempty"`
	Events       []domain.AuditEvent     `json:"events"`
}

type EventsResponse struct {
	Events []domain.AuditEvent `json:"events"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
