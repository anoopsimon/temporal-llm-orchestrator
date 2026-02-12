package domain

import "encoding/json"

type DocType string

const (
	DocTypePayslip DocType = "payslip"
	DocTypeInvoice DocType = "invoice"
	DocTypeUnknown DocType = "unknown"
)

const PayslipJSONSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": [
    "employee_name",
    "employer_name",
    "pay_period_start",
    "pay_period_end",
    "gross_pay",
    "net_pay",
    "tax_withheld",
    "confidence"
  ],
  "properties": {
    "employee_name": {"type": ["string", "null"]},
    "employer_name": {"type": ["string", "null"]},
    "pay_period_start": {"type": ["string", "null"]},
    "pay_period_end": {"type": ["string", "null"]},
    "gross_pay": {"type": "number"},
    "net_pay": {"type": "number"},
    "tax_withheld": {"type": "number"},
    "superannuation": {"type": "number"},
    "confidence": {"type": "number", "minimum": 0, "maximum": 1}
  }
}`

const InvoiceJSONSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": [
    "supplier_name",
    "invoice_number",
    "invoice_date",
    "total_amount",
    "confidence"
  ],
  "properties": {
    "supplier_name": {"type": ["string", "null"]},
    "invoice_number": {"type": ["string", "null"]},
    "invoice_date": {"type": ["string", "null"]},
    "due_date": {"type": ["string", "null"]},
    "total_amount": {"type": "number"},
    "gst_amount": {"type": "number"},
    "confidence": {"type": "number", "minimum": 0, "maximum": 1}
  }
}`

type PayslipExtraction struct {
	EmployeeName   *string  `json:"employee_name"`
	EmployerName   *string  `json:"employer_name"`
	PayPeriodStart *string  `json:"pay_period_start"`
	PayPeriodEnd   *string  `json:"pay_period_end"`
	GrossPay       float64  `json:"gross_pay"`
	NetPay         float64  `json:"net_pay"`
	TaxWithheld    float64  `json:"tax_withheld"`
	Superannuation *float64 `json:"superannuation,omitempty"`
	Confidence     float64  `json:"confidence"`
}

type InvoiceExtraction struct {
	SupplierName  *string  `json:"supplier_name"`
	InvoiceNumber *string  `json:"invoice_number"`
	InvoiceDate   *string  `json:"invoice_date"`
	DueDate       *string  `json:"due_date,omitempty"`
	TotalAmount   float64  `json:"total_amount"`
	GSTAmount     *float64 `json:"gst_amount,omitempty"`
	Confidence    float64  `json:"confidence"`
}

type DocumentRecord struct {
	ID             string         `json:"id"`
	Filename       string         `json:"filename"`
	ObjectKey      string         `json:"object_key"`
	RawText        string         `json:"raw_text"`
	DocType        DocType        `json:"doc_type"`
	Status         DocumentStatus `json:"status"`
	CurrentJSON    []byte         `json:"current_json,omitempty"`
	FinalJSON      []byte         `json:"final_json,omitempty"`
	Confidence     float64        `json:"confidence"`
	RejectedReason *string        `json:"rejected_reason,omitempty"`
}

type ReviewQueueItem struct {
	DocumentID  string          `json:"document_id"`
	FailedRules []string        `json:"failed_rules"`
	CurrentJSON json.RawMessage `json:"current_json"`
	Status      string          `json:"status"`
}

type ReviewDecision struct {
	Decision    ReviewDecisionType `json:"decision"`
	Corrections json.RawMessage    `json:"corrections,omitempty"`
	Reviewer    string             `json:"reviewer,omitempty"`
	Reason      string             `json:"reason,omitempty"`
}

type ValidationResult struct {
	FailedRules []string `json:"failed_rules"`
	Confidence  float64  `json:"confidence"`
}

func SchemaForDocType(docType DocType) string {
	switch docType {
	case DocTypePayslip:
		return PayslipJSONSchema
	case DocTypeInvoice:
		return InvoiceJSONSchema
	default:
		return InvoiceJSONSchema
	}
}
