package temporal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"temporal-llm-orchestrator/internal/domain"
)

func TestDocumentIntakeWorkflow_NeedsReviewApprove(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()

	store := newFakeStore()
	llm := &stubLLM{responses: []string{
		`{"employee_name":"Jane","employer_name":"ACME","pay_period_start":"2025-01-01","pay_period_end":"2025-01-15","gross_pay":2000,"net_pay":1500,"tax_withheld":500,"confidence":0.7}`,
		`{"employee_name":"Jane","employer_name":"ACME","pay_period_start":"2025-01-01","pay_period_end":"2025-01-15","gross_pay":2000,"net_pay":1500,"tax_withheld":500,"confidence":0.7}`,
	}}

	acts := &Activities{
		Store:          store,
		Blob:           &fakeBlob{},
		LLM:            llm,
		OpenAIModel:    "gpt-4o-mini",
		OpenAITimeout:  5 * time.Second,
		OpenAIMaxRetry: 1,
	}

	env.RegisterWorkflow(DocumentIntakeWorkflow)
	env.RegisterActivity(acts.StoreDocumentActivity)
	env.RegisterActivity(acts.DetectDocTypeActivity)
	env.RegisterActivity(acts.ExtractFieldsWithOpenAIActivity)
	env.RegisterActivity(acts.ValidateFieldsActivity)
	env.RegisterActivity(acts.CorrectFieldsWithOpenAIActivity)
	env.RegisterActivity(acts.QueueReviewActivity)
	env.RegisterActivity(acts.ResolveReviewActivity)
	env.RegisterActivity(acts.ApplyReviewerCorrectionActivity)
	env.RegisterActivity(acts.PersistResultActivity)
	env.RegisterActivity(acts.RejectDocumentActivity)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ReviewDecisionSignalName, ReviewDecisionSignal{
			Decision: domain.ReviewDecisionApprove,
			Reviewer: "qa",
		})
	}, time.Second)

	env.ExecuteWorkflow(DocumentIntakeWorkflow, WorkflowInput{
		DocumentID: "doc-approve-1",
		Filename:   "payslip.txt",
		Content:    []byte("Payslip gross pay net pay pay period"),
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result WorkflowResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, domain.StatusCompleted, result.Status)

	rec, ok := store.docs["doc-approve-1"]
	require.True(t, ok)
	require.Equal(t, domain.StatusCompleted, rec.Status)
	require.Greater(t, len(rec.FinalJSON), 0)
}
