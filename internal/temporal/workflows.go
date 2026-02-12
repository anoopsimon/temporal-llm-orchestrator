package temporal

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"temporal-llm-orchestrator/internal/domain"
)

const DocumentIntakeWorkflowName = "DocumentIntakeWorkflow"

type WorkflowInput struct {
	DocumentID string
	Filename   string
	Content    []byte
}

type WorkflowResult struct {
	DocumentID string
	Status     domain.DocumentStatus
}

func DocumentIntakeWorkflow(ctx workflow.Context, input WorkflowInput) (WorkflowResult, error) {
	defaultAO := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	}
	noRetryAO := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	}

	ctxDefault := workflow.WithActivityOptions(ctx, defaultAO)
	ctxNoRetry := workflow.WithActivityOptions(ctx, noRetryAO)

	var stored StoreDocumentOutput
	if err := workflow.ExecuteActivity(ctxDefault, (*Activities).StoreDocumentActivity, StoreDocumentInput{
		DocumentID: input.DocumentID,
		Filename:   input.Filename,
		Content:    input.Content,
	}).Get(ctx, &stored); err != nil {
		return WorkflowResult{}, err
	}

	var detected DetectDocTypeOutput
	if err := workflow.ExecuteActivity(ctxDefault, (*Activities).DetectDocTypeActivity, DetectDocTypeInput{
		DocumentID:   input.DocumentID,
		Filename:     input.Filename,
		DocumentText: stored.DocumentText,
	}).Get(ctx, &detected); err != nil {
		return WorkflowResult{}, err
	}

	var extracted ExtractFieldsOutput
	if err := workflow.ExecuteActivity(ctxNoRetry, (*Activities).ExtractFieldsWithOpenAIActivity, ExtractFieldsInput{
		DocumentID:   input.DocumentID,
		DocType:      detected.DocType,
		DocumentText: stored.DocumentText,
	}).Get(ctx, &extracted); err != nil {
		return WorkflowResult{}, err
	}

	var validation ValidateFieldsOutput
	if err := workflow.ExecuteActivity(ctxDefault, (*Activities).ValidateFieldsActivity, ValidateFieldsInput{
		DocType:        detected.DocType,
		ExtractionJSON: extracted.ExtractionJSON,
	}).Get(ctx, &validation); err != nil {
		return WorkflowResult{}, err
	}

	if len(validation.FailedRules) > 0 || validation.Confidence < 0.75 {
		var corrected CorrectFieldsOutput
		err := workflow.ExecuteActivity(ctxNoRetry, (*Activities).CorrectFieldsWithOpenAIActivity, CorrectFieldsInput{
			DocumentID:   input.DocumentID,
			DocType:      detected.DocType,
			DocumentText: stored.DocumentText,
			CurrentJSON:  extracted.ExtractionJSON,
			FailedRules:  validation.FailedRules,
		}).Get(ctx, &corrected)
		if err == nil {
			extracted.ExtractionJSON = corrected.CorrectedJSON
			extracted.Confidence = corrected.Confidence
			if err := workflow.ExecuteActivity(ctxDefault, (*Activities).ValidateFieldsActivity, ValidateFieldsInput{
				DocType:        detected.DocType,
				ExtractionJSON: extracted.ExtractionJSON,
			}).Get(ctx, &validation); err != nil {
				return WorkflowResult{}, err
			}
		}
	}

	if len(validation.FailedRules) > 0 || validation.Confidence < 0.75 {
		if err := workflow.ExecuteActivity(ctxDefault, (*Activities).QueueReviewActivity, QueueReviewInput{
			DocumentID:  input.DocumentID,
			FailedRules: validation.FailedRules,
			CurrentJSON: extracted.ExtractionJSON,
		}).Get(ctx, nil); err != nil {
			return WorkflowResult{}, err
		}

		signalChan := workflow.GetSignalChannel(ctx, ReviewDecisionSignalName)
		for {
			var decision ReviewDecisionSignal
			signalChan.Receive(ctx, &decision)

			switch decision.Decision {
			case domain.ReviewDecisionApprove:
				_ = workflow.ExecuteActivity(ctxDefault, (*Activities).ResolveReviewActivity, ResolveReviewInput{
					DocumentID: input.DocumentID,
					Decision:   "APPROVED",
				}).Get(ctx, nil)
				goto persist
			case domain.ReviewDecisionReject:
				if err := workflow.ExecuteActivity(ctxDefault, (*Activities).RejectDocumentActivity, RejectDocumentInput{
					DocumentID: input.DocumentID,
					Reason:     decision.Reason,
				}).Get(ctx, nil); err != nil {
					return WorkflowResult{}, err
				}
				return WorkflowResult{DocumentID: input.DocumentID, Status: domain.StatusRejected}, nil
			case domain.ReviewDecisionCorrect:
				var correctedByReviewer ApplyReviewerCorrectionOutput
				if err := workflow.ExecuteActivity(ctxDefault, (*Activities).ApplyReviewerCorrectionActivity, ApplyReviewerCorrectionInput{
					DocumentID:  input.DocumentID,
					DocType:     detected.DocType,
					Corrections: decision.Corrections,
				}).Get(ctx, &correctedByReviewer); err != nil {
					return WorkflowResult{}, err
				}

				if len(correctedByReviewer.CorrectedJSON) > 0 {
					extracted.ExtractionJSON = correctedByReviewer.CorrectedJSON
					extracted.Confidence = correctedByReviewer.Confidence
					validation.Confidence = correctedByReviewer.Confidence
				}
				validation.FailedRules = correctedByReviewer.FailedRules

				if len(validation.FailedRules) == 0 && validation.Confidence >= 0.75 {
					_ = workflow.ExecuteActivity(ctxDefault, (*Activities).ResolveReviewActivity, ResolveReviewInput{
						DocumentID: input.DocumentID,
						Decision:   "CORRECTED",
					}).Get(ctx, nil)
					goto persist
				}

				if err := workflow.ExecuteActivity(ctxDefault, (*Activities).QueueReviewActivity, QueueReviewInput{
					DocumentID:  input.DocumentID,
					FailedRules: validation.FailedRules,
					CurrentJSON: extracted.ExtractionJSON,
				}).Get(ctx, nil); err != nil {
					return WorkflowResult{}, err
				}
			default:
				continue
			}
		}
	}

persist:
	if err := workflow.ExecuteActivity(ctxDefault, (*Activities).PersistResultActivity, PersistResultInput{
		DocumentID: input.DocumentID,
		FinalJSON:  extracted.ExtractionJSON,
		Confidence: extracted.Confidence,
	}).Get(ctx, nil); err != nil {
		return WorkflowResult{}, err
	}

	return WorkflowResult{DocumentID: input.DocumentID, Status: domain.StatusCompleted}, nil
}
