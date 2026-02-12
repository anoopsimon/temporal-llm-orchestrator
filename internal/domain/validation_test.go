package domain

import "testing"

func TestValidatePayslipRules(t *testing.T) {
	start := "2025-01-01"
	end := "2025-01-15"
	valid := PayslipExtraction{
		EmployeeName:   strPtr("A"),
		EmployerName:   strPtr("B"),
		PayPeriodStart: &start,
		PayPeriodEnd:   &end,
		GrossPay:       2000,
		NetPay:         1500,
		TaxWithheld:    500,
		Confidence:     0.9,
	}
	res := ValidatePayslip(valid)
	if len(res.FailedRules) != 0 {
		t.Fatalf("expected no failed rules, got %v", res.FailedRules)
	}

	invalid := valid
	invalid.GrossPay = 100
	invalid.NetPay = 150
	invalid.Confidence = 1.2
	res = ValidatePayslip(invalid)
	if len(res.FailedRules) == 0 {
		t.Fatalf("expected failed rules")
	}
}

func TestValidateInvoiceRules(t *testing.T) {
	date := "2025-01-20"
	due := "2025-02-20"
	valid := InvoiceExtraction{
		SupplierName:  strPtr("Supplier"),
		InvoiceNumber: strPtr("INV-1"),
		InvoiceDate:   &date,
		DueDate:       &due,
		TotalAmount:   100,
		Confidence:    0.8,
	}
	res := ValidateInvoice(valid)
	if len(res.FailedRules) != 0 {
		t.Fatalf("expected no failed rules, got %v", res.FailedRules)
	}

	invalid := valid
	invalid.TotalAmount = 0
	badDate := "20-02-2025"
	invalid.InvoiceDate = &badDate
	res = ValidateInvoice(invalid)
	if len(res.FailedRules) == 0 {
		t.Fatalf("expected failed rules")
	}
}

func strPtr(v string) *string {
	return &v
}
