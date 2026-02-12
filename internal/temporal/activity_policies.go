package temporal

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	ActivityPolicyStoreDocument           = "store_document"
	ActivityPolicyDetectDocType           = "detect_doc_type"
	ActivityPolicyExtractFieldsWithOpenAI = "extract_fields_with_openai"
	ActivityPolicyValidateFields          = "validate_fields"
	ActivityPolicyCorrectFieldsWithOpenAI = "correct_fields_with_openai"
	ActivityPolicyQueueReview             = "queue_review"
	ActivityPolicyResolveReview           = "resolve_review"
	ActivityPolicyApplyReviewerCorrection = "apply_reviewer_correction"
	ActivityPolicyPersistResult           = "persist_result"
	ActivityPolicyRejectDocument          = "reject_document"
)

type activityPolicy struct {
	StartToCloseTimeout time.Duration
	RetryPolicy         temporal.RetryPolicy
}

var activityPolicies = map[string]activityPolicy{
	ActivityPolicyStoreDocument: {
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	},
	ActivityPolicyDetectDocType: {
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	},
	ActivityPolicyExtractFieldsWithOpenAI: {
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	},
	ActivityPolicyValidateFields: {
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	},
	ActivityPolicyCorrectFieldsWithOpenAI: {
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	},
	ActivityPolicyQueueReview: {
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	},
	ActivityPolicyResolveReview: {
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	},
	ActivityPolicyApplyReviewerCorrection: {
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	},
	ActivityPolicyPersistResult: {
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	},
	ActivityPolicyRejectDocument: {
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: temporal.RetryPolicy{
			InitialInterval:    1 * time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	},
}

func ActivityOptionsFor(policyName string) (workflow.ActivityOptions, error) {
	policy, ok := activityPolicies[policyName]
	if !ok {
		return workflow.ActivityOptions{}, fmt.Errorf("unknown activity policy: %s", policyName)
	}

	retry := policy.RetryPolicy
	return workflow.ActivityOptions{
		StartToCloseTimeout: policy.StartToCloseTimeout,
		RetryPolicy:         &retry,
	}, nil
}

func mustActivityContext(ctx workflow.Context, policyName string) workflow.Context {
	ao, err := ActivityOptionsFor(policyName)
	if err != nil {
		panic(err)
	}
	return workflow.WithActivityOptions(ctx, ao)
}
