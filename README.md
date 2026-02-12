# AI Document Intake and Decision Workflow

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
/
  cmd/api/main.go
  cmd/worker/main.go
  internal/api/routes.go
  internal/api/handlers.go
  internal/api/middleware.go
  internal/temporal/workflows.go
  internal/temporal/activities.go
  internal/temporal/signals.go
  internal/openai/client.go
  internal/openai/prompts.go
  internal/openai/parse.go
  internal/storage/minio.go
  internal/storage/postgres.go
  internal/domain/models.go
  internal/domain/validation.go
  internal/domain/state.go
  internal/config/config.go
  db/migrations/001_init.sql
  docker/temporal/dynamicconfig/
  scripts/
  testdata/
  docker-compose.yml
  Makefile
  README.md
  go.mod
  go.sum
```

## Prompt Sets and Execution Order

The workflow uses these constants exactly in this order:

1. Prompt Set A: Base Extraction (`BASE_SYSTEM`, `BASE_USER_TEMPLATE`)
2. Prompt Set B: JSON Repair (`REPAIR_SYSTEM`, `REPAIR_USER_TEMPLATE`) when A parse or schema fails
3. Prompt Set C: Rule Correction (`CORRECT_SYSTEM`, `CORRECT_USER_TEMPLATE`) when validation fails or confidence is below `0.75`

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

Run tests:

```bash
make test
```

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
make eval-braintrust-install
# set BRAINTRUST_API_KEY in .env
make eval-braintrust
```

Docker-only run (no host Python):

```bash
# set BRAINTRUST_API_KEY in .env
make start
make eval-braintrust-docker
```

Environment controls:

- `EVAL_API_URL` default `http://localhost:8080`
- `EVAL_CASES_PATH` default `evals/braintrust/cases.json`
- `EVAL_AUTO_APPROVE_REVIEW` default `false`
- `EVAL_POLL_TIMEOUT_SEC` default `180`

## Documentation Site (GitHub Pages)

This repository includes a full Nextra documentation site in `docs/`.

Local docs commands:

```bash
make docs-install
make docs-dev
make docs-build
```

GitHub Pages deployment:

- Workflow file: `.github/workflows/docs-gh-pages.yml`
- Trigger: push to `main` when `docs/**` changes
- Output: static export from `docs/out`

## Notes

- Workflow logic is deterministic.
- All network calls are contained in activities.
- OpenAI calls use exponential backoff retries in activity code.
- Activity behavior is idempotent by `documentId` using persisted state checks.
