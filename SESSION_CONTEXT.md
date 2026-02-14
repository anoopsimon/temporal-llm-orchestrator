# Session Context Handoff

Use this file to quickly resume work after restart.

## Resume Prompt

```text
You are continuing work on temporal-llm-orchestrator.
Please read SESSION_CONTEXT.md first, then continue from the current repo state.
Focus on event-driven intake (MinIO -> event-handler -> Temporal), system blackbox behavior, docs/diagram clarity, and data visibility improvements.
If needed, propose and implement next step: an API summary endpoint for processed outcomes.
```

## What The System Does (Current)

- Intake API accepts multipart uploads (`POST /v1/documents`).
- API writes uploaded file bytes to MinIO bucket (`documents`) and stores object key in Postgres.
- MinIO emits `ObjectCreated` events.
- `event-handler` listens to MinIO events and starts Temporal workflow `DocumentIntakeWorkflow`.
- Temporal worker executes activities (store, classify, extract/repair/correct, validate, review loop, persist/reject).
- Review decisions are submitted via API (`POST /v1/documents/{id}/review`) and sent as Temporal signals.

## Important Architecture Decision

- Workflow start is asynchronous and event-driven from MinIO notifications.
- API upload endpoint no longer directly starts workflows.

## Processed Data Outcome

- Processed/extracted results are written to Postgres:
  - `documents.final_json` (final output)
  - `documents.current_json` (latest extraction state)
  - `documents.status`, `documents.doc_type`, `documents.confidence`
- Additional trace tables:
  - `audit_log` (state transitions and details)
  - `extraction_attempts` (raw model outputs per phase)
  - `review_queue` (pending and resolved human review items)

## Current Input Scope

- Supported: UTF-8 text uploads.
- Not supported: scanned/image/PDF OCR ingestion.
- API now rejects non-text uploads with `415 Unsupported Media Type`.

## Tests/Fixtures Added

- Blackbox system test uses real API + worker + event-handler + Temporal history checks.
- Blackbox now also validates non-text rejection (`415`) for:
  - `testdata/scanned_payslip.pdf`
  - `testdata/scanned_invoice.png`
- Added realistic text fixtures:
  - `testdata/email_payslip.txt`
  - `testdata/portal_invoice.txt`
- Added generated invoice files for manual testing:
  - `testdata/invoice_sample_filled.pdf`
  - `testdata/invoice_sample_filled.png`

## Docs/Diagram Work Completed

- Replaced Mermaid architecture with custom SVG:
  - `docs/public/diagrams/intake-architecture.svg`
  - source: `docs/diagrams/intake-architecture.drawio`
- Added pictorial end-to-end flow diagram:
  - `docs/public/diagrams/intake-flow-sequence.svg`
  - source: `docs/diagrams/intake-flow-sequence.drawio`
- Updated flow labels to explicitly mark AI/LLM steps.
- Cleaned diagram layout to reduce overlap and fixed diamond connector anchors to touch edges.
- Embedded flow diagram in `README.md` and `docs/pages/storytelling-flow.mdx`.

## VS Code DB Quick Connect

- Checked-in SQLTools template:
  - `.vscode/settings.json`
  - `.vscode/extensions.json`
- Connection profile:
  - host `127.0.0.1`, port `5432`, db `intake`, user `postgres`, password prompt enabled.

## Useful Commands

```bash
make compose-up
go test ./internal/api ./internal/temporal
go test ./tests/system -tags=system -run TestBlackboxSystem -count=1
npm --prefix docs run build
```

## Likely Next Improvement

- Add `GET /v1/summary` to provide a single operational view:
  - counts by status
  - completed/rejected totals
  - average confidence
  - recent document outcomes
  - top failed rules from review queue/audit detail
