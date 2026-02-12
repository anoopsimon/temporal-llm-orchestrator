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
	cd docs && BASE_PATH= NODE_ENV= npm run dev

.PHONY: docs-build
docs-build:
	cd docs && BASE_PATH= NODE_ENV= npm run build

.PHONY: docs-start
docs-start:
	cd docs && BASE_PATH= NODE_ENV= npm run start

.PHONY: eval-braintrust
eval-braintrust:
	@set -a; [ -f ./.env ] && . ./.env; set +a; \
	test -n "$$BRAINTRUST_API_KEY" || (echo "BRAINTRUST_API_KEY is required in .env"; exit 1); \
	cd evals/braintrust && go run .

.PHONY: eval-braintrust-docker
eval-braintrust-docker:
	COMPOSE_PROFILES=eval docker compose run --rm braintrust-eval
