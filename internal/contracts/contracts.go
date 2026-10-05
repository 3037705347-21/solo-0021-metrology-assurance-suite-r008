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
	Code         string `json:"code"`
	Name         string `json:"name"`
	ParentID     string `json:"parent_id,omitempty"`
	ValidFrom    string `json:"valid_from"`
	ValidThrough string `json:"valid_through,omitempty"`
	Custodian    string `json:"custodian"`
}

type DeactivateStandardRequest struct {
	Actor string `json:"actor"`
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

// TraceLinkView is the readable projection of one calibration chain hop. The
// state field is the standard's state at the time the view is built; for a
// sealed case the whole trace is the frozen chain recorded in the evidence
// seal.
type TraceLinkView struct {
	StandardID   string                `json:"standard_id"`
	Code         string                `json:"code"`
	Name         string                `json:"name"`
	ParentID     string                `json:"parent_id,omitempty"`
	Role         domain.TraceRole      `json:"role"`
	ValidFrom    time.Time             `json:"valid_from"`
	ValidThrough *time.Time            `json:"valid_through,omitempty"`
	Status       domain.StandardStatus `json:"status"`
}

type TraceView struct {
	Links []TraceLinkView `json:"links"`
	// Valid is true when a complete chain to a root basis exists and is usable
	// right now. Sealed cases always carry their frozen trace with Valid=true.
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}

type CaseResponse struct {
	Case         domain.VerificationCase `json:"case"`
	Measurements []domain.Measurement    `json:"measurements"`
	Seal         *domain.EvidenceSeal    `json:"seal,omitempty"`
	Trace        TraceView               `json:"trace"`
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
