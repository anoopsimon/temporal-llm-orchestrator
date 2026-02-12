#!/usr/bin/env bash
set -euo pipefail

API_URL="http://localhost:8080"
FILE_PATH="testdata/payslip.txt"
AUTO_APPROVE_REVIEW="true"
REVIEWER="local-dev"
POLL_INTERVAL_SEC="2"
POLL_TIMEOUT_SEC="180"
START_COMPOSE="false"
READY_TIMEOUT_SEC="180"

usage() {
  cat <<'USAGE'
Usage:
  scripts/run-workflow.sh [options]

Options:
  -f, --file <path>             File to upload (default: testdata/payslip.txt)
  -a, --api-url <url>           API base URL (default: http://localhost:8080)
  --no-auto-approve             Do not auto-approve NEEDS_REVIEW
  -r, --reviewer <name>         Reviewer name for auto-approve (default: local-dev)
  -i, --poll-interval <sec>     Poll interval in seconds (default: 2)
  -t, --timeout <sec>           Max wait time in seconds (default: 180)
  --ready-timeout <sec>         Max wait for API readiness (default: 180)
  --start-compose               Run 'docker compose up -d --build' first
  -h, --help                    Show this help

Examples:
  scripts/run-workflow.sh
  scripts/run-workflow.sh --file testdata/invoice.txt
  scripts/run-workflow.sh --start-compose
USAGE
}

extract_json_string_field() {
  local json="$1"
  local key="$2"
  printf '%s' "$json" | sed -nE "s/.*\"${key}\"[[:space:]]*:[[:space:]]*\"([^\"]*)\".*/\\1/p" | head -n1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -f|--file)
      FILE_PATH="${2:-}"
      shift 2
      ;;
    -a|--api-url)
      API_URL="${2:-}"
      shift 2
      ;;
    --no-auto-approve)
      AUTO_APPROVE_REVIEW="false"
      shift
      ;;
    -r|--reviewer)
      REVIEWER="${2:-}"
      shift 2
      ;;
    -i|--poll-interval)
      POLL_INTERVAL_SEC="${2:-}"
      shift 2
      ;;
    -t|--timeout)
      POLL_TIMEOUT_SEC="${2:-}"
      shift 2
      ;;
    --ready-timeout)
      READY_TIMEOUT_SEC="${2:-}"
      shift 2
      ;;
    --start-compose)
      START_COMPOSE="true"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1"
      usage
      exit 1
      ;;
  esac
done

if [[ ! -f "$FILE_PATH" ]]; then
  echo "File not found: $FILE_PATH"
  exit 1
fi

if [[ "$START_COMPOSE" == "true" ]]; then
  echo "Starting services via docker compose..."
  docker compose up -d --build
fi

echo "Waiting for API readiness at ${API_URL}/healthz ..."
ready_start_ts="$(date +%s)"
while true; do
  if curl -fsS "${API_URL}/healthz" >/dev/null 2>&1; then
    break
  fi

  now_ts="$(date +%s)"
  ready_elapsed="$((now_ts - ready_start_ts))"
  if (( ready_elapsed > READY_TIMEOUT_SEC )); then
    echo "API did not become ready within ${READY_TIMEOUT_SEC}s."
    echo "Try: docker compose logs --tail=120 api"
    exit 1
  fi
  sleep 2
done
echo "API is ready."

echo "Uploading file: $FILE_PATH"
upload_response="$(curl -sS -X POST "${API_URL}/v1/documents" -F "file=@${FILE_PATH}")"
document_id="$(extract_json_string_field "$upload_response" "document_id")"

if [[ -z "$document_id" ]]; then
  echo "Failed to parse document_id from upload response:"
  echo "$upload_response"
  exit 1
fi

echo "Started workflow for document_id: $document_id"
echo

start_ts="$(date +%s)"
last_status=""
review_sent="false"

while true; do
  status_response="$(curl -sS "${API_URL}/v1/documents/${document_id}/status")"
  status="$(extract_json_string_field "$status_response" "status")"

  if [[ -z "$status" ]]; then
    echo "Could not parse status. Raw response:"
    echo "$status_response"
    exit 1
  fi

  if [[ "$status" != "$last_status" ]]; then
    echo "Status: $status"
    last_status="$status"
  fi

  if [[ "$status" == "NEEDS_REVIEW" && "$AUTO_APPROVE_REVIEW" == "true" && "$review_sent" == "false" ]]; then
    echo "Sending auto-approve review signal..."
    review_response="$(curl -sS -X POST "${API_URL}/v1/documents/${document_id}/review" \
      -H "Content-Type: application/json" \
      -d "{\"decision\":\"approve\",\"reviewer\":\"${REVIEWER}\"}")"
    echo "Review response: $review_response"
    review_sent="true"
  fi

  if [[ "$status" == "COMPLETED" || "$status" == "REJECTED" || "$status" == "FAILED" ]]; then
    break
  fi

  now_ts="$(date +%s)"
  elapsed="$((now_ts - start_ts))"
  if (( elapsed > POLL_TIMEOUT_SEC )); then
    echo "Timed out after ${POLL_TIMEOUT_SEC}s waiting for completion."
    exit 1
  fi

  sleep "$POLL_INTERVAL_SEC"
done

echo
echo "Final status:"
curl -sS "${API_URL}/v1/documents/${document_id}/status"
echo
echo
echo "Final result:"
curl -sS "${API_URL}/v1/documents/${document_id}/result"
echo
