# Braintrust Evals

This folder contains production-grade LLM evals for document extraction quality.

## What this evaluates

- endpoint completion status (`COMPLETED`)
- document type correctness
- schema conformance (required keys, no unknown keys)
- field-level accuracy against golden data
- business rule validation (payslip and invoice rules)
- confidence threshold (`>= 0.75`)
- review avoidance (penalizes `NEEDS_REVIEW`)

## Prerequisites

1. Running local stack (`make start`)
2. Valid OpenAI key in `.env` used by API and worker
3. Braintrust account key in root `.env`: `BRAINTRUST_API_KEY`

## Run in Docker (recommended)

```bash
# set BRAINTRUST_API_KEY in .env
make start
make eval-braintrust-docker
```

This runs the eval in a dedicated `golang:1.25` container and calls your API at `http://api:8080` on the Docker network.

## Run on host

```bash
# set BRAINTRUST_API_KEY in .env
make eval-braintrust
```

Optional runtime controls:

- `EVAL_API_URL` default `http://localhost:8080`
- `EVAL_CASES_PATH` default `evals/braintrust/cases.json`
- `EVAL_AUTO_APPROVE_REVIEW` default `false`
- `EVAL_POLL_TIMEOUT_SEC` default `180`
- `BRAINTRUST_PROJECT` default `temporal-llm-orchestrator`

## Notes

- Evaluator entrypoint is `evals/braintrust/main.go` and uses `github.com/braintrustdata/braintrust-sdk-go/eval`.
- If you set `EVAL_AUTO_APPROVE_REVIEW=true`, cases that route to review will be auto-approved to reach terminal output.
- Keep `EVAL_AUTO_APPROVE_REVIEW=false` for strict quality gates in CI.
