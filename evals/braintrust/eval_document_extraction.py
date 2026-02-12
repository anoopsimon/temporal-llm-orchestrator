#!/usr/bin/env python3
from __future__ import annotations

import datetime as dt
import json
import os
import time
from pathlib import Path
from typing import Any

import requests
from braintrust import Eval

ROOT_DIR = Path(__file__).resolve().parents[2]
DEFAULT_CASES_PATH = Path(__file__).resolve().with_name("cases.json")
API_URL = os.getenv("EVAL_API_URL", "http://localhost:8080").rstrip("/")
AUTO_APPROVE_REVIEW = os.getenv("EVAL_AUTO_APPROVE_REVIEW", "false").lower() == "true"
POLL_INTERVAL_SEC = float(os.getenv("EVAL_POLL_INTERVAL_SEC", "2"))
POLL_TIMEOUT_SEC = float(os.getenv("EVAL_POLL_TIMEOUT_SEC", "180"))
REQUEST_TIMEOUT_SEC = float(os.getenv("EVAL_REQUEST_TIMEOUT_SEC", "20"))

TERMINAL_STATES = {"COMPLETED", "REJECTED", "FAILED"}

SCHEMA_SPEC = {
    "payslip": {
        "required": {
            "employee_name",
            "employer_name",
            "pay_period_start",
            "pay_period_end",
            "gross_pay",
            "net_pay",
            "tax_withheld",
            "confidence",
        },
        "optional": {"superannuation"},
    },
    "invoice": {
        "required": {
            "supplier_name",
            "invoice_number",
            "invoice_date",
            "total_amount",
            "confidence",
        },
        "optional": {"due_date", "gst_amount"},
    },
}


def _load_cases() -> list[dict[str, Any]]:
    cases_path = Path(os.getenv("EVAL_CASES_PATH", str(DEFAULT_CASES_PATH)))
    with cases_path.open("r", encoding="utf-8") as handle:
        rows = json.load(handle)
    if not isinstance(rows, list) or not rows:
        raise ValueError(f"cases file must contain a non-empty list: {cases_path}")
    return rows


def _api_get(path: str) -> dict[str, Any]:
    response = requests.get(
        f"{API_URL}{path}",
        timeout=REQUEST_TIMEOUT_SEC,
    )
    response.raise_for_status()
    return response.json()


def _api_post(path: str, *, json_payload: dict[str, Any] | None = None, files: Any = None) -> dict[str, Any]:
    response = requests.post(
        f"{API_URL}{path}",
        timeout=REQUEST_TIMEOUT_SEC,
        json=json_payload,
        files=files,
    )
    response.raise_for_status()
    return response.json()


def _wait_for_terminal_state(document_id: str) -> dict[str, Any]:
    start = time.time()
    review_sent = False

    while True:
        status_payload = _api_get(f"/v1/documents/{document_id}/status")
        status = status_payload.get("status", "")

        if status == "NEEDS_REVIEW" and AUTO_APPROVE_REVIEW and not review_sent:
            _api_post(
                f"/v1/documents/{document_id}/review",
                json_payload={"decision": "approve", "reviewer": "braintrust-eval"},
            )
            review_sent = True

        if status in TERMINAL_STATES:
            result_payload = _api_get(f"/v1/documents/{document_id}/result")
            result_payload["review_required"] = review_sent or status == "NEEDS_REVIEW"
            return result_payload

        if time.time() - start > POLL_TIMEOUT_SEC:
            raise TimeoutError(f"timed out waiting for document {document_id}")

        time.sleep(POLL_INTERVAL_SEC)


def _run_case(case_input: dict[str, Any]) -> dict[str, Any]:
    file_path = ROOT_DIR / case_input["file_path"]
    if not file_path.exists():
        raise FileNotFoundError(f"missing fixture file: {file_path}")

    with file_path.open("rb") as handle:
        upload = _api_post(
            "/v1/documents",
            files={"file": (file_path.name, handle, "text/plain")},
        )

    document_id = upload.get("document_id")
    if not document_id:
        raise RuntimeError(f"upload did not return document_id: {upload}")

    result_payload = _wait_for_terminal_state(document_id)

    return {
        "document_id": document_id,
        "status": result_payload.get("status"),
        "doc_type": result_payload.get("doc_type"),
        "confidence": result_payload.get("confidence"),
        "result": result_payload.get("result") or {},
        "rejected_reason": result_payload.get("rejected_reason"),
        "review_required": bool(result_payload.get("review_required")),
    }


def _is_number(value: Any) -> bool:
    return isinstance(value, (int, float)) and not isinstance(value, bool)


def _numbers_close(a: Any, b: Any, tol: float = 0.01) -> bool:
    if not _is_number(a) or not _is_number(b):
        return False
    return abs(float(a) - float(b)) <= tol


def _normalize_string(value: Any) -> str:
    if value is None:
        return ""
    return str(value).strip().lower()


def _values_match(expected: Any, actual: Any) -> bool:
    if expected is None:
        return actual is None
    if _is_number(expected):
        return _numbers_close(expected, actual)
    return _normalize_string(expected) == _normalize_string(actual)


def _parse_iso_date(value: Any) -> bool:
    if value is None:
        return False
    try:
        dt.date.fromisoformat(str(value))
        return True
    except ValueError:
        return False


def _extract_context(*args: Any, **kwargs: Any) -> tuple[dict[str, Any], dict[str, Any], dict[str, Any]]:
    input_obj = kwargs.get("input")
    output_obj = kwargs.get("output")
    expected_obj = kwargs.get("expected")

    if len(args) == 3:
        input_obj, output_obj, expected_obj = args
    elif len(args) == 2 and output_obj is None and expected_obj is None:
        output_obj, expected_obj = args
    elif len(args) == 1 and output_obj is None:
        output_obj = args[0]

    return (
        input_obj if isinstance(input_obj, dict) else {},
        output_obj if isinstance(output_obj, dict) else {},
        expected_obj if isinstance(expected_obj, dict) else {},
    )


def score_status(*args: Any, **kwargs: Any) -> float:
    _, output_obj, expected_obj = _extract_context(*args, **kwargs)
    expected_status = expected_obj.get("status", "COMPLETED")
    return 1.0 if output_obj.get("status") == expected_status else 0.0


def score_doc_type(*args: Any, **kwargs: Any) -> float:
    _, output_obj, expected_obj = _extract_context(*args, **kwargs)
    expected_doc_type = expected_obj.get("doc_type")
    if not expected_doc_type:
        return 0.0
    return 1.0 if output_obj.get("doc_type") == expected_doc_type else 0.0


def score_schema_conformance(*args: Any, **kwargs: Any) -> float:
    input_obj, output_obj, _ = _extract_context(*args, **kwargs)
    doc_type = output_obj.get("doc_type") or input_obj.get("doc_type")
    schema_spec = SCHEMA_SPEC.get(doc_type, {})
    required = schema_spec.get("required", set())
    optional = schema_spec.get("optional", set())

    result_payload = output_obj.get("result")
    if not isinstance(result_payload, dict):
        return 0.0

    actual_keys = set(result_payload.keys())
    allowed_keys = required | optional

    missing_required = required - actual_keys
    extra_keys = actual_keys - allowed_keys

    if missing_required or extra_keys:
        return 0.0

    return 1.0


def score_field_accuracy(*args: Any, **kwargs: Any) -> float:
    _, output_obj, expected_obj = _extract_context(*args, **kwargs)
    expected_result = expected_obj.get("result", {})
    actual_result = output_obj.get("result", {})

    if not isinstance(expected_result, dict) or not expected_result:
        return 0.0
    if not isinstance(actual_result, dict):
        return 0.0

    total = len(expected_result)
    matched = 0
    for key, expected_value in expected_result.items():
        actual_value = actual_result.get(key)
        if _values_match(expected_value, actual_value):
            matched += 1

    return matched / total


def _validate_payslip(result_payload: dict[str, Any]) -> bool:
    gross_pay = result_payload.get("gross_pay")
    net_pay = result_payload.get("net_pay")
    tax_withheld = result_payload.get("tax_withheld")
    superannuation = result_payload.get("superannuation")

    if not all(_is_number(v) for v in [gross_pay, net_pay, tax_withheld]):
        return False
    if float(gross_pay) < 0 or float(net_pay) < 0 or float(tax_withheld) < 0:
        return False
    if superannuation is not None and (not _is_number(superannuation) or float(superannuation) < 0):
        return False
    if float(gross_pay) < float(net_pay):
        return False

    start_date = result_payload.get("pay_period_start")
    end_date = result_payload.get("pay_period_end")
    if not _parse_iso_date(start_date) or not _parse_iso_date(end_date):
        return False
    if dt.date.fromisoformat(str(start_date)) > dt.date.fromisoformat(str(end_date)):
        return False

    confidence = result_payload.get("confidence")
    if not _is_number(confidence):
        return False
    if not (0 <= float(confidence) <= 1):
        return False

    return True


def _validate_invoice(result_payload: dict[str, Any]) -> bool:
    total_amount = result_payload.get("total_amount")
    gst_amount = result_payload.get("gst_amount")

    if not _is_number(total_amount):
        return False
    if float(total_amount) <= 0:
        return False
    if gst_amount is not None and (not _is_number(gst_amount) or float(gst_amount) < 0):
        return False

    invoice_date = result_payload.get("invoice_date")
    if not _parse_iso_date(invoice_date):
        return False

    due_date = result_payload.get("due_date")
    if due_date is not None and not _parse_iso_date(due_date):
        return False

    confidence = result_payload.get("confidence")
    if not _is_number(confidence):
        return False
    if not (0 <= float(confidence) <= 1):
        return False

    return True


def score_validation_rules(*args: Any, **kwargs: Any) -> float:
    input_obj, output_obj, _ = _extract_context(*args, **kwargs)
    result_payload = output_obj.get("result")
    if not isinstance(result_payload, dict):
        return 0.0

    doc_type = output_obj.get("doc_type") or input_obj.get("doc_type")
    if doc_type == "payslip":
        return 1.0 if _validate_payslip(result_payload) else 0.0
    if doc_type == "invoice":
        return 1.0 if _validate_invoice(result_payload) else 0.0
    return 0.0


def score_confidence_threshold(*args: Any, **kwargs: Any) -> float:
    _, output_obj, expected_obj = _extract_context(*args, **kwargs)
    confidence = output_obj.get("confidence")
    threshold = expected_obj.get("min_confidence", 0.75)

    if not _is_number(confidence):
        return 0.0
    return 1.0 if float(confidence) >= float(threshold) else 0.0


def score_review_avoidance(*args: Any, **kwargs: Any) -> float:
    _, output_obj, _ = _extract_context(*args, **kwargs)
    return 0.0 if output_obj.get("review_required") else 1.0


def _assert_prerequisites() -> None:
    if not os.getenv("BRAINTRUST_API_KEY"):
        raise EnvironmentError("BRAINTRUST_API_KEY is required")

    health = _api_get("/healthz")
    status = str(health.get("status", "")).lower()
    if status != "ok":
        raise RuntimeError(f"API health check failed: {health}")


def _build_metadata() -> dict[str, Any]:
    return {
        "service": "temporal-llm-orchestrator",
        "api_url": API_URL,
        "auto_approve_review": AUTO_APPROVE_REVIEW,
        "poll_timeout_sec": POLL_TIMEOUT_SEC,
    }


def main() -> None:
    _assert_prerequisites()

    Eval(
        "document-intake-extraction-eval",
        data=_load_cases(),
        task=_run_case,
        scores=[
            score_status,
            score_doc_type,
            score_schema_conformance,
            score_field_accuracy,
            score_validation_rules,
            score_confidence_threshold,
            score_review_avoidance,
        ],
        metadata=_build_metadata(),
    )


if __name__ == "__main__":
    main()
