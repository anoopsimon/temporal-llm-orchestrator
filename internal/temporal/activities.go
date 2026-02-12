package temporal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"temporal-llm-orchestrator/internal/domain"
	"temporal-llm-orchestrator/internal/openai"
)

const (
	modelOutputPhaseBase1    = "BASE_ATTEMPT_1"
	modelOutputPhaseBase2    = "BASE_ATTEMPT_2"
	modelOutputPhaseRepair1  = "REPAIR_ATTEMPT_1"
	modelOutputPhaseCorrect1 = "CORRECT_ATTEMPT_1"
)

type ActivityStore interface {
	UpsertDocument(ctx context.Context, rec domain.DocumentRecord) error
	GetDocument(ctx context.Context, documentID string) (domain.DocumentRecord, error)
	UpdateDocumentClassification(ctx context.Context, documentID string, docType domain.DocType) error
	InsertAudit(ctx context.Context, documentID string, state domain.AuditState, detail any) error
	SaveModelOutput(ctx context.Context, documentID string, phase string, output string) error
	SaveCurrentExtraction(ctx context.Context, documentID string, payload []byte, confidence float64) error
	GetCurrentExtraction(ctx context.Context, documentID string) ([]byte, float64, error)
	QueueReview(ctx context.Context, documentID string, failedRules []string, currentJSON []byte) error
	ResolveReview(ctx context.Context, documentID string, decision string) error
	SaveFinalResult(ctx context.Context, documentID string, payload []byte, confidence float64, status domain.DocumentStatus, rejectedReason *string) error
}

type BlobStore interface {
	PutDocument(ctx context.Context, documentID, filename string, content []byte) (string, error)
}

type Activities struct {
	Store          ActivityStore
	Blob           BlobStore
	LLM            openai.Client
	OpenAIModel    string
	OpenAITimeout  time.Duration
	OpenAIMaxRetry int
}

type StoreDocumentInput struct {
	DocumentID string
	Filename   string
	Content    []byte
}

type StoreDocumentOutput struct {
	ObjectKey    string
	DocumentText string
}

type DetectDocTypeInput struct {
	DocumentID   string
	Filename     string
	DocumentText string
}

type DetectDocTypeOutput struct {
	DocType domain.DocType
}

type ExtractFieldsInput struct {
	DocumentID   string
	DocType      domain.DocType
	DocumentText string
}

type ExtractFieldsOutput struct {
	ExtractionJSON []byte
	Confidence     float64
}

type ValidateFieldsInput struct {
	DocType        domain.DocType
	ExtractionJSON []byte
}

type ValidateFieldsOutput struct {
	FailedRules []string
	Confidence  float64
}

type CorrectFieldsInput struct {
	DocumentID   string
	DocType      domain.DocType
	DocumentText string
	CurrentJSON  []byte
	FailedRules  []string
}

type CorrectFieldsOutput struct {
	CorrectedJSON []byte
	Confidence    float64
}

type QueueReviewInput struct {
	DocumentID  string
	FailedRules []string
	CurrentJSON []byte
}

type ResolveReviewInput struct {
	DocumentID string
	Decision   string
}

type ApplyReviewerCorrectionInput struct {
	DocumentID  string
	DocType     domain.DocType
	Corrections []byte
}

type ApplyReviewerCorrectionOutput struct {
	CorrectedJSON []byte
	Confidence    float64
	FailedRules   []string
}

type PersistResultInput struct {
	DocumentID string
	FinalJSON  []byte
	Confidence float64
}

type RejectDocumentInput struct {
	DocumentID string
	Reason     string
}

func (a *Activities) StoreDocumentActivity(ctx context.Context, input StoreDocumentInput) (StoreDocumentOutput, error) {
	existing, err := a.Store.GetDocument(ctx, input.DocumentID)
	if err == nil && existing.ObjectKey != "" && existing.RawText != "" {
		return StoreDocumentOutput{ObjectKey: existing.ObjectKey, DocumentText: existing.RawText}, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return StoreDocumentOutput{}, err
	}

	objectKey, err := a.Blob.PutDocument(ctx, input.DocumentID, input.Filename, input.Content)
	if err != nil {
		return StoreDocumentOutput{}, err
	}

	docText := string(input.Content)
	rec := domain.DocumentRecord{
		ID:        input.DocumentID,
		Filename:  input.Filename,
		ObjectKey: objectKey,
		RawText:   docText,
		DocType:   domain.DocTypeUnknown,
		Status:    domain.StatusStored,
	}
	if err := a.Store.UpsertDocument(ctx, rec); err != nil {
		return StoreDocumentOutput{}, err
	}
	if err := a.Store.InsertAudit(ctx, input.DocumentID, domain.AuditStored, map[string]any{"object_key": objectKey}); err != nil {
		return StoreDocumentOutput{}, err
	}
	return StoreDocumentOutput{ObjectKey: objectKey, DocumentText: docText}, nil
}

func (a *Activities) DetectDocTypeActivity(ctx context.Context, input DetectDocTypeInput) (DetectDocTypeOutput, error) {
	existing, err := a.Store.GetDocument(ctx, input.DocumentID)
	if err == nil && existing.DocType != "" && existing.DocType != domain.DocTypeUnknown {
		return DetectDocTypeOutput{DocType: existing.DocType}, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return DetectDocTypeOutput{}, err
	}

	docType := detectDocType(input.DocumentText, input.Filename)
	if err := a.Store.UpdateDocumentClassification(ctx, input.DocumentID, docType); err != nil {
		return DetectDocTypeOutput{}, err
	}
	if err := a.Store.InsertAudit(ctx, input.DocumentID, domain.AuditClassified, map[string]any{"doc_type": docType}); err != nil {
		return DetectDocTypeOutput{}, err
	}
	return DetectDocTypeOutput{DocType: docType}, nil
}

func (a *Activities) ExtractFieldsWithOpenAIActivity(ctx context.Context, input ExtractFieldsInput) (ExtractFieldsOutput, error) {
	existing, confidence, err := a.Store.GetCurrentExtraction(ctx, input.DocumentID)
	if err == nil && len(existing) > 0 {
		return ExtractFieldsOutput{ExtractionJSON: existing, Confidence: confidence}, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ExtractFieldsOutput{}, err
	}

	schema := domain.SchemaForDocType(input.DocType)
	basePrompt := openai.BuildBaseUserPrompt(string(input.DocType), schema, input.DocumentText)

	base1, err := a.callOpenAIWithRetry(ctx, openai.BASE_SYSTEM, basePrompt)
	if err != nil {
		return ExtractFieldsOutput{}, err
	}
	_ = a.Store.SaveModelOutput(ctx, input.DocumentID, modelOutputPhaseBase1, base1)

	parsed, conf, parseErr := openai.ParseAndNormalize(input.DocType, base1)
	if parseErr == nil {
		if err := a.Store.SaveCurrentExtraction(ctx, input.DocumentID, parsed, conf); err != nil {
			return ExtractFieldsOutput{}, err
		}
		if err := a.Store.InsertAudit(ctx, input.DocumentID, domain.AuditExtracted, map[string]any{"path": "base_1"}); err != nil {
			return ExtractFieldsOutput{}, err
		}
		return ExtractFieldsOutput{ExtractionJSON: parsed, Confidence: conf}, nil
	}

	repairPrompt := openai.BuildRepairUserPrompt(schema, base1)
	repair1, err := a.callOpenAIWithRetry(ctx, openai.REPAIR_SYSTEM, repairPrompt)
	if err != nil {
		return ExtractFieldsOutput{}, err
	}
	_ = a.Store.SaveModelOutput(ctx, input.DocumentID, modelOutputPhaseRepair1, repair1)

	parsed, conf, parseErr = openai.ParseAndNormalize(input.DocType, repair1)
	if parseErr == nil {
		if err := a.Store.SaveCurrentExtraction(ctx, input.DocumentID, parsed, conf); err != nil {
			return ExtractFieldsOutput{}, err
		}
		if err := a.Store.InsertAudit(ctx, input.DocumentID, domain.AuditExtracted, map[string]any{"path": "repair_1"}); err != nil {
			return ExtractFieldsOutput{}, err
		}
		return ExtractFieldsOutput{ExtractionJSON: parsed, Confidence: conf}, nil
	}

	base2, err := a.callOpenAIWithRetry(ctx, openai.BASE_SYSTEM, basePrompt)
	if err != nil {
		return ExtractFieldsOutput{}, err
	}
	_ = a.Store.SaveModelOutput(ctx, input.DocumentID, modelOutputPhaseBase2, base2)

	parsed, conf, parseErr = openai.ParseAndNormalize(input.DocType, base2)
	if parseErr != nil {
		return ExtractFieldsOutput{}, fmt.Errorf("extraction failed after base1+repair1+base2: %w", parseErr)
	}
	if err := a.Store.SaveCurrentExtraction(ctx, input.DocumentID, parsed, conf); err != nil {
		return ExtractFieldsOutput{}, err
	}
	if err := a.Store.InsertAudit(ctx, input.DocumentID, domain.AuditExtracted, map[string]any{"path": "base_2"}); err != nil {
		return ExtractFieldsOutput{}, err
	}
	return ExtractFieldsOutput{ExtractionJSON: parsed, Confidence: conf}, nil
}

func (a *Activities) ValidateFieldsActivity(ctx context.Context, input ValidateFieldsInput) (ValidateFieldsOutput, error) {
	_ = ctx
	result, err := openai.ValidateByRules(input.DocType, input.ExtractionJSON)
	if err != nil {
		return ValidateFieldsOutput{}, err
	}
	return ValidateFieldsOutput{FailedRules: result.FailedRules, Confidence: result.Confidence}, nil
}

func (a *Activities) CorrectFieldsWithOpenAIActivity(ctx context.Context, input CorrectFieldsInput) (CorrectFieldsOutput, error) {
	schema := domain.SchemaForDocType(input.DocType)
	prompt := openai.BuildCorrectUserPrompt(string(input.DocType), schema, input.DocumentText, string(input.CurrentJSON), input.FailedRules)

	modelOutput, err := a.callOpenAIWithRetry(ctx, openai.CORRECT_SYSTEM, prompt)
	if err != nil {
		return CorrectFieldsOutput{}, err
	}
	_ = a.Store.SaveModelOutput(ctx, input.DocumentID, modelOutputPhaseCorrect1, modelOutput)

	normalized, confidence, err := openai.ParseAndNormalize(input.DocType, modelOutput)
	if err != nil {
		return CorrectFieldsOutput{}, err
	}
	if err := a.Store.SaveCurrentExtraction(ctx, input.DocumentID, normalized, confidence); err != nil {
		return CorrectFieldsOutput{}, err
	}
	return CorrectFieldsOutput{CorrectedJSON: normalized, Confidence: confidence}, nil
}

func (a *Activities) QueueReviewActivity(ctx context.Context, input QueueReviewInput) error {
	if err := a.Store.QueueReview(ctx, input.DocumentID, input.FailedRules, input.CurrentJSON); err != nil {
		return err
	}
	return a.Store.InsertAudit(ctx, input.DocumentID, domain.AuditNeedsReview, map[string]any{"failed_rules": input.FailedRules})
}

func (a *Activities) ResolveReviewActivity(ctx context.Context, input ResolveReviewInput) error {
	return a.Store.ResolveReview(ctx, input.DocumentID, input.Decision)
}

func (a *Activities) ApplyReviewerCorrectionActivity(ctx context.Context, input ApplyReviewerCorrectionInput) (ApplyReviewerCorrectionOutput, error) {
	normalized, confidence, err := openai.ParseAndNormalize(input.DocType, string(input.Corrections))
	if err != nil {
		return ApplyReviewerCorrectionOutput{FailedRules: []string{"reviewer.corrections_invalid_json"}}, nil
	}
	if err := a.Store.SaveCurrentExtraction(ctx, input.DocumentID, normalized, confidence); err != nil {
		return ApplyReviewerCorrectionOutput{}, err
	}
	validation, err := openai.ValidateByRules(input.DocType, normalized)
	if err != nil {
		return ApplyReviewerCorrectionOutput{}, err
	}
	return ApplyReviewerCorrectionOutput{CorrectedJSON: normalized, Confidence: confidence, FailedRules: validation.FailedRules}, nil
}

func (a *Activities) PersistResultActivity(ctx context.Context, input PersistResultInput) error {
	if err := a.Store.SaveFinalResult(ctx, input.DocumentID, input.FinalJSON, input.Confidence, domain.StatusCompleted, nil); err != nil {
		return err
	}
	if err := a.Store.ResolveReview(ctx, input.DocumentID, "COMPLETED"); err != nil {
		_ = err
	}
	return a.Store.InsertAudit(ctx, input.DocumentID, domain.AuditCompleted, map[string]any{"confidence": input.Confidence})
}

func (a *Activities) RejectDocumentActivity(ctx context.Context, input RejectDocumentInput) error {
	reason := input.Reason
	if reason == "" {
		reason = "rejected by reviewer"
	}
	if err := a.Store.SaveFinalResult(ctx, input.DocumentID, nil, 0, domain.StatusRejected, &reason); err != nil {
		return err
	}
	if err := a.Store.ResolveReview(ctx, input.DocumentID, "REJECTED"); err != nil {
		_ = err
	}
	return a.Store.InsertAudit(ctx, input.DocumentID, domain.AuditRejected, map[string]any{"reason": reason})
}

func (a *Activities) callOpenAIWithRetry(ctx context.Context, systemPrompt string, userPrompt string) (string, error) {
	maxRetry := a.OpenAIMaxRetry
	if maxRetry <= 0 {
		maxRetry = 3
	}

	var lastErr error
	for attempt := 1; attempt <= maxRetry; attempt++ {
		out, err := a.LLM.CompleteJSON(ctx, openai.CompletionRequest{
			Model:        a.OpenAIModel,
			SystemPrompt: systemPrompt,
			UserPrompt:   userPrompt,
			Timeout:      a.OpenAITimeout,
		})
		if err == nil {
			return out, nil
		}
		lastErr = err
		if attempt == maxRetry {
			break
		}
		delay := time.Duration(200*(1<<(attempt-1))) * time.Millisecond
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(delay):
		}
	}
	return "", fmt.Errorf("openai retry exhausted: %w", lastErr)
}

func detectDocType(documentText string, filename string) domain.DocType {
	norm := strings.ToLower(documentText + " " + filename)
	if strings.Contains(norm, "gross pay") || strings.Contains(norm, "net pay") || strings.Contains(norm, "pay period") || strings.Contains(norm, "payslip") {
		return domain.DocTypePayslip
	}
	if strings.Contains(norm, "invoice") || strings.Contains(norm, "total amount") || strings.Contains(norm, "supplier") {
		return domain.DocTypeInvoice
	}
	return domain.DocTypeInvoice
}

func prettyJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
