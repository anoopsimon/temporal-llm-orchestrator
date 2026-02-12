package domain

type DocumentStatus string

const (
	StatusReceived    DocumentStatus = "RECEIVED"
	StatusStored      DocumentStatus = "STORED"
	StatusClassified  DocumentStatus = "CLASSIFIED"
	StatusExtracted   DocumentStatus = "EXTRACTED"
	StatusNeedsReview DocumentStatus = "NEEDS_REVIEW"
	StatusRejected    DocumentStatus = "REJECTED"
	StatusCompleted   DocumentStatus = "COMPLETED"
	StatusFailed      DocumentStatus = "FAILED"
)

type AuditState string

const (
	AuditStored      AuditState = "STORED"
	AuditClassified  AuditState = "CLASSIFIED"
	AuditExtracted   AuditState = "EXTRACTED"
	AuditNeedsReview AuditState = "NEEDS_REVIEW"
	AuditCompleted   AuditState = "COMPLETED"
	AuditRejected    AuditState = "REJECTED"
)

type ReviewDecisionType string

const (
	ReviewDecisionApprove ReviewDecisionType = "approve"
	ReviewDecisionReject  ReviewDecisionType = "reject"
	ReviewDecisionCorrect ReviewDecisionType = "correct"
)
