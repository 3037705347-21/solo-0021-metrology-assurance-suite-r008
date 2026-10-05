# Metrology Assurance Suite

## Project Goal

Metrology Assurance Suite is a local HTTP service that keeps a traceable record
of laboratory measurement assurance work. It registers devices and calibration
standards, opens a verification case for one device against a usable standard,
records technician measurements, requests a repeat measurement when tolerance
is exceeded, and seals the final evidence record after the traceability chain
to the root basis is verified.

The service is intentionally self-contained. It uses an in-memory repository
with atomic copy-on-write mutations and does not require a database or an
external service.

## Users

- Laboratory steward: registers devices and standards and opens verification
  cases.
- Measurement technician: records observed values for an open case.
- Quality verifier: reviews accepted measurements and seals the case.

## Core Entities

- Device: a laboratory asset with a unique asset tag, model, room, steward, and
  lifecycle state.
- Standard: a registered reference standard with a unique code, an optional
  superior basis, a validity interval, a steward, and a deactivation status.
  A standard with no superior basis is a root basis.
- CaseBasis: the immutable snapshot of the traceability chain resolved when a
  case opened, from the associated standard up to the root basis.
- VerificationCase: one bounded assurance activity for a device, with a nominal
  value, tolerance, current state, version, assigned steward, and its case
  basis.
- Measurement: an observed value, computed deviation, acceptance decision,
  technician, and timestamp.
- EvidenceSeal: the immutable closing record for a reviewed case, including the
  selected accepted measurement and the traceability chain verified at seal
  time.
- AuditEvent: an append-only trace of meaningful case, device, and standard
  changes.

## Workflows

### 1. Register Device

The steward sends `POST /devices` with an asset tag, model, room, and steward.
The service validates the request, normalizes the asset tag, rejects duplicate
tags, and creates an active device.

### 2. Register Standard

The steward sends `POST /standards` with a code, an optional superior basis id,
a validity interval, and a steward. The service normalizes the code, rejects
duplicate codes, requires a bounded interval with `valid_from` before
`valid_until`, and creates an active standard. The superior basis may reference
a standard that is registered later; chain completeness is verified whenever
the standard is used, not at registration.

### 3. Amend Standard Basis

The steward sends `POST /standards/{id}/amend` with a superior basis id and a
validity interval. Only an active standard can be amended. An amendment never
rewrites the basis snapshot of already formed cases; it only affects chain
verification resolved after the amendment.

### 4. Deactivate Standard

The steward sends `POST /standards/{id}/deactivate`. Only an active standard
can be deactivated, and deactivation is terminal. A deactivated standard breaks
the chain for new cases, new measurements, and sealing, but historical records
keep the facts they captured.

### 5. Open Verification Case

The steward sends `POST /cases` with a device ID, a standard ID, nominal value,
tolerance, and owner. The service verifies that the device exists, is active,
and has no unfinished case, then resolves the standard's chain to the root
basis: every hop must be registered, active, and inside its validity interval,
and the chain must be acyclic. A valid request creates a case in
`awaiting_measurement` with the resolved chain frozen as its case basis.

### 6. Record Measurement

A technician sends `POST /cases/{id}/measurements`. The case must be
`awaiting_measurement` and the case's standard chain must still resolve to a
usable root basis; a standard that expired, was deactivated, or whose chain
became broken or cyclic rejects the measurement. The service computes the
signed deviation from the nominal value. An in-tolerance measurement moves the
case to `review_ready`; an out-of-tolerance measurement moves it to
`recheck_required`.

### 7. Reopen Verification Case

The steward sends `POST /cases/{id}/reopen` after an out-of-tolerance result.
Only `recheck_required` cases may move back to `awaiting_measurement`. The
service records the actor and reason in the audit trail.

### 8. Seal Verification

The verifier sends `POST /cases/{id}/seal` after an accepted measurement. Only
`review_ready` cases may be sealed, and only while the case's standard chain
still resolves to a usable root basis. The service selects the accepted
measurement with the smallest absolute deviation, creates exactly one evidence
seal carrying the chain verified at seal time, moves the case to `sealed`, and
rejects later mutations.

## State And Rules

Device state:

- `active -> retired`
- `retired` is terminal.

Standard state:

- `active -> deactivated`
- `deactivated` is terminal.

VerificationCase state:

- `awaiting_measurement -> review_ready`
- `awaiting_measurement -> recheck_required`
- `recheck_required -> awaiting_measurement`
- `review_ready -> sealed`
- `sealed` is terminal.

Required invariants:

- Asset tags are unique after case-insensitive normalization.
- Standard codes are unique after case-insensitive normalization.
- A standard validity interval is bounded and `valid_from` precedes
  `valid_until`.
- A device has at most one unfinished verification case.
- A case is associated with a standard whose chain resolved to a usable root
  basis at open time; the resolved case basis is immutable afterwards.
- Measurements are accepted only while a case is awaiting measurement and the
  case's standard chain is currently usable.
- A tolerance must be positive.
- A case can have at most one evidence seal.
- Sealing requires the case's standard chain to currently resolve to a usable
  root basis; the verified chain is recorded on the seal.
- A sealed case cannot accept measurements, reopen, or seal again, and its
  recorded basis and seal chain always read as the facts at that time.
- Every mutation that changes a case also appends an audit event in the same
  repository commit.

## Modules

- `cmd/metrology-service`: process entrypoint and graceful HTTP shutdown.
- `cmd/workflowcheck`: bounded smoke checks that call the public HTTP API.
- `internal/httpapi`: routing, request decoding, and stable error mapping.
- `internal/assurance`: workflow orchestration and atomic decisions.
- `internal/domain`: entities, validation, state transitions, chain resolution,
  and errors.
- `internal/repository`: mutex-protected in-memory persistence with copy-on-write
  commits.
- `internal/contracts`: request and response contracts shared by the HTTP layer.

## Public Interfaces

- `GET /healthz`
- `POST /devices`
- `GET /devices/{id}`
- `POST /standards`
- `GET /standards/{id}`
- `POST /standards/{id}/amend`
- `POST /standards/{id}/deactivate`
- `POST /cases`
- `GET /cases/{id}`
- `POST /cases/{id}/measurements`
- `POST /cases/{id}/reopen`
- `POST /cases/{id}/seal`
- `GET /cases/{id}/events`

## Validation Plan

The baseline build command is `go build ./...`. Each declared workflow has a
bounded production smoke command under `cmd/workflowcheck`. The command starts
an HTTP test server in the same process, calls the public routes, and verifies
both success and failure behavior. The seal check also races two concurrent
seal requests to prove that the repository mutation lock permits exactly one
seal. The calibration-chain check proves that expired, deactivated, dangling,
or cyclic chains block new cases, new measurements, and sealing while formed
and sealed records keep their historical facts.

Tests are intentionally deferred for this initialization baseline. The later
engineering-task stage will add unit and integration tests, including red/green
verification for the workflows and their failure paths.
