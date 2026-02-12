package temporal

import (
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
	ctxStoreDocument := mustActivityContext(ctx, ActivityPolicyStoreDocument)
	ctxDetectDocType := mustActivityContext(ctx, ActivityPolicyDetectDocType)
	ctxExtractFields := mustActivityContext(ctx, ActivityPolicyExtractFieldsWithOpenAI)
	ctxValidateFields := mustActivityContext(ctx, ActivityPolicyValidateFields)
	ctxCorrectFields := mustActivityContext(ctx, ActivityPolicyCorrectFieldsWithOpenAI)
	ctxQueueReview := mustActivityContext(ctx, ActivityPolicyQueueReview)
	ctxResolveReview := mustActivityContext(ctx, ActivityPolicyResolveReview)
	ctxApplyReviewerCorrection := mustActivityContext(ctx, ActivityPolicyApplyReviewerCorrection)
	ctxPersistResult := mustActivityContext(ctx, ActivityPolicyPersistResult)
	ctxRejectDocument := mustActivityContext(ctx, ActivityPolicyRejectDocument)

	var stored StoreDocumentOutput
	if err := workflow.ExecuteActivity(ctxStoreDocument, (*Activities).StoreDocumentActivity, StoreDocumentInput{
		DocumentID: input.DocumentID,
		Filename:   input.Filename,
		Content:    input.Content,
	}).Get(ctx, &stored); err != nil {
		return WorkflowResult{}, err
	}

	var detected DetectDocTypeOutput
	if err := workflow.ExecuteActivity(ctxDetectDocType, (*Activities).DetectDocTypeActivity, DetectDocTypeInput{
		DocumentID:   input.DocumentID,
		Filename:     input.Filename,
		DocumentText: stored.DocumentText,
	}).Get(ctx, &detected); err != nil {
		return WorkflowResult{}, err
	}

	var extracted ExtractFieldsOutput
	if err := workflow.ExecuteActivity(ctxExtractFields, (*Activities).ExtractFieldsWithOpenAIActivity, ExtractFieldsInput{
		DocumentID:   input.DocumentID,
		DocType:      detected.DocType,
		DocumentText: stored.DocumentText,
	}).Get(ctx, &extracted); err != nil {
		return WorkflowResult{}, err
	}

	var validation ValidateFieldsOutput
	if err := workflow.ExecuteActivity(ctxValidateFields, (*Activities).ValidateFieldsActivity, ValidateFieldsInput{
		DocType:        detected.DocType,
		ExtractionJSON: extracted.ExtractionJSON,
	}).Get(ctx, &validation); err != nil {
		return WorkflowResult{}, err
	}

	if len(validation.FailedRules) > 0 || validation.Confidence < 0.75 {
		var corrected CorrectFieldsOutput
		err := workflow.ExecuteActivity(ctxCorrectFields, (*Activities).CorrectFieldsWithOpenAIActivity, CorrectFieldsInput{
			DocumentID:   input.DocumentID,
			DocType:      detected.DocType,
			DocumentText: stored.DocumentText,
			CurrentJSON:  extracted.ExtractionJSON,
			FailedRules:  validation.FailedRules,
		}).Get(ctx, &corrected)
		if err == nil {
			extracted.ExtractionJSON = corrected.CorrectedJSON
			extracted.Confidence = corrected.Confidence
			if err := workflow.ExecuteActivity(ctxValidateFields, (*Activities).ValidateFieldsActivity, ValidateFieldsInput{
				DocType:        detected.DocType,
				ExtractionJSON: extracted.ExtractionJSON,
			}).Get(ctx, &validation); err != nil {
				return WorkflowResult{}, err
			}
		}
	}

	if len(validation.FailedRules) > 0 || validation.Confidence < 0.75 {
		if err := workflow.ExecuteActivity(ctxQueueReview, (*Activities).QueueReviewActivity, QueueReviewInput{
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
				_ = workflow.ExecuteActivity(ctxResolveReview, (*Activities).ResolveReviewActivity, ResolveReviewInput{
					DocumentID: input.DocumentID,
					Decision:   "APPROVED",
				}).Get(ctx, nil)
				goto persist
			case domain.ReviewDecisionReject:
				if err := workflow.ExecuteActivity(ctxRejectDocument, (*Activities).RejectDocumentActivity, RejectDocumentInput{
					DocumentID: input.DocumentID,
					Reason:     decision.Reason,
				}).Get(ctx, nil); err != nil {
					return WorkflowResult{}, err
				}
				return WorkflowResult{DocumentID: input.DocumentID, Status: domain.StatusRejected}, nil
			case domain.ReviewDecisionCorrect:
				var correctedByReviewer ApplyReviewerCorrectionOutput
				if err := workflow.ExecuteActivity(ctxApplyReviewerCorrection, (*Activities).ApplyReviewerCorrectionActivity, ApplyReviewerCorrectionInput{
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
					_ = workflow.ExecuteActivity(ctxResolveReview, (*Activities).ResolveReviewActivity, ResolveReviewInput{
						DocumentID: input.DocumentID,
						Decision:   "CORRECTED",
					}).Get(ctx, nil)
					goto persist
				}

				if err := workflow.ExecuteActivity(ctxQueueReview, (*Activities).QueueReviewActivity, QueueReviewInput{
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
	if err := workflow.ExecuteActivity(ctxPersistResult, (*Activities).PersistResultActivity, PersistResultInput{
		DocumentID: input.DocumentID,
		FinalJSON:  extracted.ExtractionJSON,
		Confidence: extracted.Confidence,
	}).Get(ctx, nil); err != nil {
		return WorkflowResult{}, err
	}

	return WorkflowResult{DocumentID: input.DocumentID, Status: domain.StatusCompleted}, nil
}
