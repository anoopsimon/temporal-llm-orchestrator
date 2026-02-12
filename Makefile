SHELL := /bin/bash

export GOCACHE ?= /tmp/gocache
export GOMODCACHE ?= /tmp/gomodcache

.PHONY: test

test:
	go test ./...

.PHONY: run-api
run-api:
	POSTGRES_DSN="$${POSTGRES_DSN:-postgres://postgres:postgres@localhost:5432/intake?sslmode=disable}" \
	TEMPORAL_ADDRESS="$${TEMPORAL_ADDRESS:-localhost:7233}" \
	TEMPORAL_NAMESPACE="$${TEMPORAL_NAMESPACE:-default}" \
	TEMPORAL_TASK_QUEUE="$${TEMPORAL_TASK_QUEUE:-document-intake-task-queue}" \
	MINIO_ENDPOINT="$${MINIO_ENDPOINT:-localhost:9000}" \
	MINIO_ACCESS_KEY="$${MINIO_ACCESS_KEY:-minioadmin}" \
	MINIO_SECRET_KEY="$${MINIO_SECRET_KEY:-minioadmin}" \
	MINIO_BUCKET="$${MINIO_BUCKET:-documents}" \
	MINIO_USE_SSL="$${MINIO_USE_SSL:-false}" \
	go run ./cmd/api

.PHONY: run-worker
run-worker:
	POSTGRES_DSN="$${POSTGRES_DSN:-postgres://postgres:postgres@localhost:5432/intake?sslmode=disable}" \
	TEMPORAL_ADDRESS="$${TEMPORAL_ADDRESS:-localhost:7233}" \
	TEMPORAL_NAMESPACE="$${TEMPORAL_NAMESPACE:-default}" \
	TEMPORAL_TASK_QUEUE="$${TEMPORAL_TASK_QUEUE:-document-intake-task-queue}" \
	MINIO_ENDPOINT="$${MINIO_ENDPOINT:-localhost:9000}" \
	MINIO_ACCESS_KEY="$${MINIO_ACCESS_KEY:-minioadmin}" \
	MINIO_SECRET_KEY="$${MINIO_SECRET_KEY:-minioadmin}" \
	MINIO_BUCKET="$${MINIO_BUCKET:-documents}" \
	MINIO_USE_SSL="$${MINIO_USE_SSL:-false}" \
	go run ./cmd/worker

.PHONY: compose-up
compose-up:
	docker compose up -d --build

.PHONY: compose-down
compose-down:
	docker compose down -v

.PHONY: start
start:
	docker compose up -d --build

.PHONY: stop
stop:
	docker compose down -v

.PHONY: restart
restart: stop start

.PHONY: status
status:
	docker compose ps

.PHONY: logs
logs:
	docker compose logs -f api worker

.PHONY: migrate
migrate:
	./scripts/migrate.sh

.PHONY: run-workflow
run-workflow:
	./scripts/run-workflow.sh --file "$${FILE:-testdata/payslip.txt}"

.PHONY: docs-install
docs-install:
	cd docs && npm install

.PHONY: docs-dev
docs-dev:
	cd docs && BASE_PATH= npm run dev

.PHONY: docs-build
docs-build:
	cd docs && BASE_PATH= npm run build

.PHONY: eval-braintrust-install
eval-braintrust-install:
	python3 -m venv .venv-braintrust
	. .venv-braintrust/bin/activate && pip install --upgrade pip && pip install -r evals/braintrust/requirements.txt

.PHONY: eval-braintrust
eval-braintrust:
	@test -x ".venv-braintrust/bin/braintrust" || (echo "Run 'make eval-braintrust-install' first"; exit 1)
	@set -a; [ -f ./.env ] && . ./.env; set +a; \
	test -n "$$BRAINTRUST_API_KEY" || (echo "BRAINTRUST_API_KEY is required in .env"; exit 1); \
	. .venv-braintrust/bin/activate && braintrust eval evals/braintrust/document_extraction_eval.py

.PHONY: eval-braintrust-docker
eval-braintrust-docker:
	docker compose run --rm --profile eval braintrust-eval
