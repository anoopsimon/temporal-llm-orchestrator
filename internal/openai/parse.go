package openai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"temporal-llm-orchestrator/internal/domain"
)

var payslipAllowedKeys = map[string]struct{}{
	"employee_name":    {},
	"employer_name":    {},
	"pay_period_start": {},
	"pay_period_end":   {},
	"gross_pay":        {},
	"net_pay":          {},
	"tax_withheld":     {},
	"superannuation":   {},
	"confidence":       {},
}

var invoiceAllowedKeys = map[string]struct{}{
	"supplier_name":  {},
	"invoice_number": {},
	"invoice_date":   {},
	"due_date":       {},
	"total_amount":   {},
	"gst_amount":     {},
	"confidence":     {},
}

func ParseAndNormalize(docType domain.DocType, raw string) ([]byte, float64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, 0, fmt.Errorf("empty model output")
	}

	switch docType {
	case domain.DocTypePayslip:
		if err := validateKeys(trimmed, payslipAllowedKeys, []string{
			"employee_name", "employer_name", "pay_period_start", "pay_period_end", "gross_pay", "net_pay", "tax_withheld", "confidence",
		}); err != nil {
			return nil, 0, err
		}
		var v domain.PayslipExtraction
		if err := strictDecode([]byte(trimmed), &v); err != nil {
			return nil, 0, err
		}
		out, err := json.Marshal(v)
		if err != nil {
			return nil, 0, err
		}
		return out, v.Confidence, nil
	case domain.DocTypeInvoice:
		if err := validateKeys(trimmed, invoiceAllowedKeys, []string{
			"supplier_name", "invoice_number", "invoice_date", "total_amount", "confidence",
		}); err != nil {
			return nil, 0, err
		}
		var v domain.InvoiceExtraction
		if err := strictDecode([]byte(trimmed), &v); err != nil {
			return nil, 0, err
		}
		out, err := json.Marshal(v)
		if err != nil {
			return nil, 0, err
		}
		return out, v.Confidence, nil
	default:
		return nil, 0, fmt.Errorf("unsupported doc type %q", docType)
	}
}

func ValidateByRules(docType domain.DocType, payload []byte) (domain.ValidationResult, error) {
	switch docType {
	case domain.DocTypePayslip:
		var v domain.PayslipExtraction
		if err := strictDecode(payload, &v); err != nil {
			return domain.ValidationResult{}, err
		}
		return domain.ValidatePayslip(v), nil
	case domain.DocTypeInvoice:
		var v domain.InvoiceExtraction
		if err := strictDecode(payload, &v); err != nil {
			return domain.ValidationResult{}, err
		}
		return domain.ValidateInvoice(v), nil
	default:
		return domain.ValidationResult{}, fmt.Errorf("unsupported doc type %q", docType)
	}
}

func strictDecode(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("unexpected trailing data")
	}
	return nil
}

func validateKeys(raw string, allowed map[string]struct{}, required []string) error {
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &rawMap); err != nil {
		return err
	}
	for k := range rawMap {
		if _, ok := allowed[k]; !ok {
			keys := sortedKeys(allowed)
			return fmt.Errorf("unknown key %q, allowed: %v", k, keys)
		}
	}
	for _, req := range required {
		if _, ok := rawMap[req]; !ok {
			return fmt.Errorf("missing required key %q", req)
		}
	}
	return nil
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
