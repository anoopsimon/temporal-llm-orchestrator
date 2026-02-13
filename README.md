# AI Document Intake and Decision Workflow
[![System Blackbox Test](https://github.com/anoopsimon/temporal-llm-orchestrator/actions/workflows/system-blackbox.yml/badge.svg)](https://github.com/anoopsimon/temporal-llm-orchestrator/actions/workflows/system-blackbox.yml)

Build Status: green
Document Status: Up to date

This repository implements an AI document intake pipeline with Go, Temporal, OpenAI, Postgres, and MinIO.

## Stack

- Go 1.22+
- Temporal Server + Temporal Go SDK
- Postgres for results, audit, and review queue
- MinIO for document storage
- OpenAI API for extraction and correction
- Chi HTTP API

## Repository Layout

```
.
├── cmd/
│   ├── api/main.go
│   └── worker/main.go
├── internal/
│   ├── api/                         # routes, handlers, middleware
│   ├── config/                      # env configuration
│   ├── domain/                      # schema models, states, validation
│   ├── openai/                      # prompts, client wrapper, strict parsing
│   ├── storage/                     # Postgres and MinIO adapters
│   └── temporal/                    # workflows, activities, signals
├── db/migrations/001_init.sql
├── docker/temporal/dynamicconfig/
├── docs/                            # docs site
├── evals/braintrust/                # Go Braintrust eval harness
├── scripts/                         # helper scripts
├── testdata/                        # sample documents
├── .github/workflows/docs-gh-pages.yml
├── docker-compose.yml
├── Makefile
├── README.md
├── go.mod
└── go.sum
```

## Prompt Sets and Execution Order

The workflow runs in 3 phases, in this exact order:

1. Phase A: Extract (`BASE_SYSTEM`, `BASE_USER_TEMPLATE`)
   - AI reads the document and extracts structured fields.
2. Phase B: Fix Format (`REPAIR_SYSTEM`, `REPAIR_USER_TEMPLATE`)
   - If A output is invalid JSON or schema-mismatched, AI rewrites it into valid JSON.
3. Phase C: Fix Values (`CORRECT_SYSTEM`, `CORRECT_USER_TEMPLATE`)
   - If JSON is valid but business rules fail or confidence is below `0.75`, AI gets one correction pass.

If output is still not reliable after A -> B -> C, the workflow routes to human review (`NEEDS_REVIEW`) instead of guessing.

The orchestration ladder is implemented in `internal/temporal/workflows.go` and `internal/temporal/activities.go` with hard limits:

- Base Extraction attempt 2 max
- Repair attempt 1 max
- Correction attempt 1 max

## API Endpoints

- `POST /v1/documents` multipart upload field `file`
- `GET /v1/documents/{documentId}/status`
- `GET /v1/documents/{documentId}/result`
- `POST /v1/documents/{documentId}/review`
- `GET /v1/reviews/pending`
- `GET /healthz`
- `GET /readyz`

## Review Signal Contract

`POST /v1/documents/{documentId}/review` body:

```json
{
  "decision": "approve",
  "corrections": {"...": "..."},
  "reviewer": "user@company.com",
  "reason": "optional"
}
```

Valid `decision` values:

- `approve`
- `reject`
- `correct`

## Local Run with Docker Compose

Set `OPENAI_API_KEY` in your shell, then run:

```bash
cp .env.example .env
# edit .env and set OPENAI_API_KEY
make compose-up
```

Services:

- API: `http://localhost:8080`
- Temporal gRPC: `localhost:7233`
- Temporal UI: `http://localhost:8233`
- MinIO API: `http://localhost:9000`
- MinIO Console: `http://localhost:9001`
- App Postgres: `localhost:5432`

Stop and clean:

```bash
make compose-down
```

Docker Compose loads `.env` automatically and `api`/`worker` also reference it via `env_file`.
In CI, injected environment variables can override `.env` values.

## Local Run without Docker Compose

Prerequisites:

- Postgres with schema from `db/migrations/001_init.sql`
- Temporal server on `localhost:7233`
- MinIO bucket and credentials

Environment variables:

- `POSTGRES_DSN`
- `TEMPORAL_ADDRESS`
- `TEMPORAL_NAMESPACE`
- `TEMPORAL_TASK_QUEUE`
- `MINIO_ENDPOINT`
- `MINIO_ACCESS_KEY`
- `MINIO_SECRET_KEY`
- `MINIO_BUCKET`
- `MINIO_USE_SSL`
- `OPENAI_API_KEY`
- `OPENAI_MODEL` (optional, default `gpt-4o-mini`)
- `OPENAI_TIMEOUT_SEC` (optional, default `30`)

Run API and worker:

```bash
make run-worker
make run-api
```

## Testing

Unit tests cover:

- Validation rules
- Prompt template rendering
- Strict JSON parsing into structs
- Repair path where invalid JSON is corrected

Integration test covers:

- Full Temporal workflow path
- NEEDS_REVIEW state
- Human review signal `approve` to complete

System blackbox test covers:

- Real HTTP file upload to API
- Real Temporal worker execution (no in-memory activity registration)
- Workflow history verification via Temporal SDK client

Run tests:

```bash
make test
```

Run the full blackbox system test:

```bash
make test-blackbox
```

`test-blackbox` assumes the compose stack is already running, including `api` and `worker`, and fails fast if not.

Run one end to end workflow from shell:

```bash
make run-workflow
# or
./scripts/run-workflow.sh --file testdata/invoice.txt
```

## LLM Evals with Braintrust

This repository includes an eval harness in `evals/braintrust/` for professional quality checks on extraction behavior.

It scores:

- completion status
- document type match
- schema conformance
- field-level accuracy
- validation rule compliance
- confidence threshold
- review avoidance

Setup and run:

```bash
# set BRAINTRUST_API_KEY in .env
make eval-braintrust
```

Docker run (recommended):

```bash
# set BRAINTRUST_API_KEY in .env
make start
make eval-braintrust-docker
```

`eval-braintrust` runs the Go evaluator in `evals/braintrust/` on your host.
`eval-braintrust-docker` runs the same Go evaluator inside Docker.

Environment controls:

- `EVAL_API_URL` default `http://localhost:8080`
- `EVAL_CASES_PATH` default `evals/braintrust/cases.json`
- `EVAL_AUTO_APPROVE_REVIEW` default `false`
- `EVAL_POLL_TIMEOUT_SEC` default `180`
- `BRAINTRUST_PROJECT` default `temporal-llm-orchestrator`

## Documentation Site (GitHub Pages)

This repository includes a full Nextra documentation site in `docs/`.

Local docs commands:

```bash
make docs-install
make docs-dev
make docs-build
make docs-start
```

GitHub Pages deployment:

- Workflow file: `.github/workflows/docs-gh-pages.yml`
- Trigger: push to `main` or `master`, and manual dispatch
- Output: static export from `docs/out`

## Notes

- Workflow logic is deterministic.
- All network calls are contained in activities.
- OpenAI calls use exponential backoff retries in activity code.
- Activity behavior is idempotent by `documentId` using persisted state checks.
