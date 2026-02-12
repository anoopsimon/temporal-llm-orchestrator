package openai

import (
	"testing"

	"temporal-llm-orchestrator/internal/domain"
)

func TestParseAndNormalizePayslipStrict(t *testing.T) {
	raw := `{"employee_name":"Jane","employer_name":"Acme","pay_period_start":"2025-01-01","pay_period_end":"2025-01-15","gross_pay":2000,"net_pay":1500,"tax_withheld":500,"confidence":0.9}`
	out, conf, err := ParseAndNormalize(domain.DocTypePayslip, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) == 0 {
		t.Fatalf("expected normalized output")
	}
	if conf != 0.9 {
		t.Fatalf("unexpected confidence: %v", conf)
	}
}

func TestParseAndNormalizeRejectsExtraKeys(t *testing.T) {
	raw := `{"supplier_name":"S","invoice_number":"1","invoice_date":"2025-01-01","total_amount":10,"confidence":0.9,"unexpected":1}`
	_, _, err := ParseAndNormalize(domain.DocTypeInvoice, raw)
	if err == nil {
		t.Fatalf("expected error for unknown key")
	}
}

func TestValidateByRules(t *testing.T) {
	raw := []byte(`{"supplier_name":"S","invoice_number":"1","invoice_date":"2025-01-01","total_amount":10,"confidence":0.9}`)
	res, err := ValidateByRules(domain.DocTypeInvoice, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.FailedRules) != 0 {
		t.Fatalf("expected zero failed rules, got %v", res.FailedRules)
	}
}
