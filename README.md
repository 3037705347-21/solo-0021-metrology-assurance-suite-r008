# Metrology Assurance Suite

Metrology Assurance Suite is a Go HTTP service for bounded laboratory
measurement assurance work. It registers devices and measurement standards,
opens verification cases against the standard usable at the time, records
measurements, handles failed tolerance decisions, and seals traceable evidence
records with the complete calibration chain frozen into the seal.

## Run

```bash
go run ./cmd/metrology-service --addr 127.0.0.1:18121
```

The service listens on `127.0.0.1:18121` by default.

## Build And Verify

```bash
go build ./...
go test ./...
go run ./cmd/workflowcheck --workflow register-device
go run ./cmd/workflowcheck --workflow register-standard
go run ./cmd/workflowcheck --workflow open-verification-case
go run ./cmd/workflowcheck --workflow record-measurement
go run ./cmd/workflowcheck --workflow reopen-verification-case
go run ./cmd/workflowcheck --workflow seal-verification
go run ./cmd/workflowcheck --workflow calibration-trace
go run ./cmd/workflowcheck --workflow standard-lifecycle
```

The workflow runner starts a fresh in-process HTTP service and exercises the
public API. It is a production smoke command, not the unit test suite.

## Calibration Traceability

Standards form a calibration chain: each standard may name one higher-level
standard (`parent_id`); a standard with no parent is a root basis. A chain is
valid only when every link, from the working standard up to the root, exists,
is **active**, and is inside its `valid_from .. valid_through` interval at the
instant it is used. Parent references are immutable after registration, so a
chain's topology can never be rewritten; only time (expiry) or explicit
deactivation can invalidate it.

- `POST /cases` opens a case only against a standard whose whole chain to a
  root is currently usable. The case freezes the standard's id and code.
- `POST /cases/{id}/measurements` re-checks the chain, so a standard that
  expired or was withdrawn after opening blocks new measurements.
- `POST /cases/{id}/seal` verifies the chain again and freezes the full
  working → parent → root trace into the evidence seal. A broken, cyclic,
  expired, or deactivated chain prevents sealing.
- Later deactivation or expiry never rewrites formed case evidence: a sealed
  case keeps reading the trace exactly as it was at seal time, while new work
  is refused.

## API

- `GET /healthz`
- `POST /devices`
- `GET /devices/{id}`
- `POST /standards`
- `POST /standards/{id}/deactivate`
- `POST /cases`
- `GET /cases/{id}`
- `POST /cases/{id}/measurements`
- `POST /cases/{id}/reopen`
- `POST /cases/{id}/seal`
- `GET /cases/{id}/events`

A case view carries a `trace` object. For an open case it is the chain resolved
against the current register (`"valid": false` with a `reason` if the basis has
lapsed); for a sealed case it is the immutable chain copied from the evidence
seal. Standard validity times use RFC3339 timestamps.

## Layout

- `cmd/metrology-service`: executable HTTP server.
- `cmd/workflowcheck`: bounded workflow verification commands.
- `internal/httpapi`: route and JSON boundary.
- `internal/assurance`: workflow orchestration.
- `internal/domain`: state rules, chain resolution, and invariants.
- `internal/repository`: copy-on-write in-memory persistence.
- `internal/contracts`: API request and response models.
- `internal/bootstrap`: concrete dependency wiring.

## Inputs And Outputs

Inputs are JSON HTTP requests. Outputs are JSON response envelopes with either a
resource view or a stable error object:

```json
{
  "error": {
    "code": "standard_inactive",
    "message": "standard is deactivated: STD-WORK-1601"
  }
}
```

Traceability failures use the stable codes `trace_broken`, `trace_cycle`,
`standard_inactive`, and `standard_expired`. The service has no environment
variables and no external dependencies.
