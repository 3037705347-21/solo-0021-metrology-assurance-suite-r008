# Metrology Assurance Suite

## Project Goal

Metrology Assurance Suite is a local HTTP service that keeps a traceable record
of laboratory measurement assurance work. It registers devices and measurement
standards, opens a verification case for one device against the standard usable
at the time, records technician measurements, requests a repeat measurement when
tolerance is exceeded, and seals the final evidence record after review with the
complete calibration chain frozen into the seal.

The service is intentionally self-contained. It uses an in-memory repository
with atomic copy-on-write mutations and does not require a database or an
external service.

## Users

- Laboratory steward: registers devices and standards and opens verification cases.
- Measurement technician: records observed values for an open case.
- Quality verifier: reviews accepted measurements and seals the case.

## Core Entities

- Device: a laboratory asset with a unique asset tag, model, room, steward, and
  lifecycle state.
- Standard: a measurement standard or higher-level calibration basis with a
  unique code, custodian, an optional parent standard, a validity interval, and
  an active/deactivated lifecycle state. Parent references form calibration
  chains and are immutable after registration; a standard without a parent is a
  root basis.
- VerificationCase: one bounded assurance activity for a device and the standard
  that backed it, with a nominal value, tolerance, current state, version, and
  assigned steward. The standard id and code are fixed when the case opens.
- Measurement: an observed value, computed deviation, acceptance decision,
  technician, and timestamp.
- EvidenceSeal: the immutable closing record for a reviewed case, including the
  selected accepted measurement and the frozen working-to-root trace.
- TraceLink: one frozen hop of a calibration chain (standard id, code, name,
  parent, role, validity interval, and state as they stood at freeze time).
- AuditEvent: an append-only trace of meaningful case, device, and standard changes.

## Workflows

### 1. Register Device

The steward sends `POST /devices` with an asset tag, model, room, and steward.
The service validates the request, normalizes the asset tag, rejects duplicate
tags, and creates an active device.

### 2. Register Standard

The steward sends `POST /standards` with a code, name, custodian, a validity
interval (`valid_from`, optional open-ended `valid_through`), and an optional
`parent_id` naming the higher-level calibration basis. The code is normalized
and must be unique. When a parent is named, the parent and every ancestor above
it must exist and be usable now; otherwise the new standard is rejected
(`trace_broken`, `standard_inactive`, or `standard_expired`). A standard without
a parent is a root basis. The parent is never editable later, so a formed chain
cannot be rewired into a loop or a different basis.

### 3. Deactivate Standard

The steward sends `POST /standards/{id}/deactivate` to withdraw a standard from
service. Deactivation is terminal for the standard: it blocks all new use, but
it never mutates already-opened cases or already-sealed evidence.

### 4. Open Verification Case

The steward sends `POST /cases` with a device ID, standard ID, nominal value,
tolerance, and owner. The service verifies that the device exists and is active
with no unfinished case, and that the standard's full chain to a root basis
exists and is usable at that instant. A valid request creates a case in
`awaiting_measurement` and freezes the standard's id and code onto the case.

### 5. Record Measurement

A technician sends `POST /cases/{id}/measurements`. The case must be
`awaiting_measurement`, and the standard chain must still be usable now; an
expired or deactivated basis (anywhere on the chain) rejects the measurement.
The service computes the signed deviation from the nominal value. An
in-tolerance measurement moves the case to `review_ready`; an out-of-tolerance
measurement moves it to `recheck_required`.

### 6. Reopen Verification Case

The steward sends `POST /cases/{id}/reopen` after an out-of-tolerance result.
Only `recheck_required` cases may move back to `awaiting_measurement`. The
service records the actor and reason in the audit trail.

### 7. Seal Verification

The verifier sends `POST /cases/{id}/seal` after an accepted measurement. Only
`review_ready` cases may be sealed. Before sealing, the service re-verifies the
case's full calibration chain to a root basis as it stands at sealing time. The
service selects the accepted measurement with the smallest absolute deviation,
freezes the working-to-root trace into the seal, creates exactly one evidence
seal, moves the case to `sealed`, and rejects later mutations. A broken, cyclic,
expired, or deactivated chain blocks sealing. Later standard changes never
rewrite a sealed case: its evidence is read by the facts frozen at seal time.

## State And Rules

Device state:

- `active -> retired`
- `retired` is terminal.

Standard state:

- `active -> deactivated`
- `deactivated` is terminal.
- A standard is usable at an instant only while active and inside its
  `valid_from .. valid_through` interval (boundaries inclusive; no
  `valid_through` means open-ended).
- Parent references are immutable: standards are append-only and a registered
  parent or validity interval is never rewritten.

VerificationCase state:

- `awaiting_measurement -> review_ready`
- `awaiting_measurement -> recheck_required`
- `recheck_required -> awaiting_measurement`
- `review_ready -> sealed`
- `sealed` is terminal.

Required invariants:

- Asset tags are unique after case-insensitive normalization.
- Standard codes are unique after case-insensitive normalization.
- A registered non-root standard must have an existing, usable parent chain at
  registration time; chain walks must terminate at a root without cycles.
- A device has at most one unfinished verification case.
- A case must name a standard, and the standard's complete chain to a root must
  be usable when the case opens, when each measurement is recorded, and when the
  case is sealed.
- The standard id and code fixed on a case are never changed by later standard
  registration, expiry, or deactivation.
- Measurements are accepted only while a case is awaiting measurement.
- A tolerance must be positive.
- A case can have at most one evidence seal.
- A seal must contain a complete working-to-root trace frozen at sealing time.
- A sealed case cannot accept measurements, reopen, or seal again; its frozen
  trace remains readable even after every standard on the chain is withdrawn.
- Every mutation that changes a case, device, or standard also appends an audit
  event in the same repository commit.

## Modules

- `cmd/metrology-service`: process entrypoint and graceful HTTP shutdown.
- `cmd/workflowcheck`: bounded smoke checks that call the public HTTP API.
- `internal/httpapi`: routing, request decoding, and stable error mapping.
- `internal/assurance`: workflow orchestration and atomic decisions.
- `internal/domain`: entities, validation, state transitions, and errors.
- `internal/repository`: mutex-protected in-memory persistence with copy-on-write
  commits.
- `internal/contracts`: request and response contracts shared by the HTTP layer.

## Public Interfaces

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

## Validation Plan

The baseline build command is `go build ./...`, with unit tests under
`go test ./...` covering chain resolution (valid chain, cycle, broken link,
expiry and deactivation boundaries) and the assurance workflows' success and
failure paths. Each declared workflow also has a bounded production smoke
command under `cmd/workflowcheck`. The command starts an HTTP test server in the
same process, calls the public routes, and verifies both success and failure
behavior. The seal check races two concurrent seal requests to prove that the
repository mutation lock permits exactly one seal. The standard-lifecycle check
seals evidence, withdraws the standards afterward, and proves historical
evidence still reads by the facts at seal time while new measurements, cases,
and seals are refused.
