package domain

import (
	"errors"
	"time"
)

const dateLayout = "2006-01-02"

func ValidatePayslip(v PayslipExtraction) ValidationResult {
	failed := make([]string, 0)

	if v.GrossPay < 0 || v.NetPay < 0 || v.TaxWithheld < 0 {
		failed = append(failed, "payslip.amounts_non_negative")
	}
	if v.Superannuation != nil && *v.Superannuation < 0 {
		failed = append(failed, "payslip.superannuation_non_negative")
	}
	if v.GrossPay < v.NetPay {
		failed = append(failed, "payslip.gross_pay_gte_net_pay")
	}
	start, startErr := parseISODate(v.PayPeriodStart)
	end, endErr := parseISODate(v.PayPeriodEnd)
	if startErr != nil || endErr != nil {
		failed = append(failed, "payslip.pay_period_dates_parseable")
	} else if start.After(end) {
		failed = append(failed, "payslip.pay_period_start_lte_end")
	}
	if v.Confidence < 0 || v.Confidence > 1 {
		failed = append(failed, "payslip.confidence_range")
	}

	return ValidationResult{FailedRules: failed, Confidence: v.Confidence}
}

func ValidateInvoice(v InvoiceExtraction) ValidationResult {
	failed := make([]string, 0)

	if v.TotalAmount <= 0 {
		failed = append(failed, "invoice.total_amount_gt_zero")
	}
	if v.TotalAmount < 0 {
		failed = append(failed, "invoice.amounts_non_negative")
	}
	if v.GSTAmount != nil && *v.GSTAmount < 0 {
		failed = append(failed, "invoice.gst_non_negative")
	}
	if _, err := parseISODate(v.InvoiceDate); err != nil {
		failed = append(failed, "invoice.invoice_date_parseable")
	}
	if v.DueDate != nil {
		if _, err := time.Parse(dateLayout, *v.DueDate); err != nil {
			failed = append(failed, "invoice.due_date_parseable")
		}
	}
	if v.Confidence < 0 || v.Confidence > 1 {
		failed = append(failed, "invoice.confidence_range")
	}

	return ValidationResult{FailedRules: failed, Confidence: v.Confidence}
}

func parseISODate(v *string) (time.Time, error) {
	if v == nil {
		return time.Time{}, errors.New("date is null")
	}
	return time.Parse(dateLayout, *v)
}

func ValidationPassed(r ValidationResult) bool {
	return len(r.FailedRules) == 0
}
