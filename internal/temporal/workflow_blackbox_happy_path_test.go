package temporal

import (
	"context"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"

	"temporal-llm-orchestrator/internal/domain"
)

type activityTrace struct {
	mu sync.Mutex

	startedOrder   []string
	completedOrder []string

	storeIn     *StoreDocumentInput
	storeOut    *StoreDocumentOutput
	detectIn    *DetectDocTypeInput
	detectOut   *DetectDocTypeOutput
	extractIn   *ExtractFieldsInput
	extractOut  *ExtractFieldsOutput
	validateIn  *ValidateFieldsInput
	validateOut *ValidateFieldsOutput
	persistIn   *PersistResultInput

	correctCalls     int
	queueReviewCalls int
	rejectCalls      int
}

func (t *activityTrace) recordStarted(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.startedOrder = append(t.startedOrder, name)
}

func (t *activityTrace) recordCompleted(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.completedOrder = append(t.completedOrder, name)
}

var _ = Describe("DocumentIntakeWorkflow blackbox happy path", func() {
	It("uploads a document payload, runs workflow activities, and completes with expected output", func() {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()

		store := newFakeStore()
		llm := &stubLLM{responses: []string{
			`{"employee_name":"Jane Doe","employer_name":"ACME Payroll","pay_period_start":"2025-01-01","pay_period_end":"2025-01-15","gross_pay":2000,"net_pay":1500,"tax_withheld":500,"confidence":0.93}`,
		}}
		acts := &Activities{
			Store:          store,
			Blob:           &fakeBlob{},
			LLM:            llm,
			OpenAIModel:    "gpt-4o-mini",
			OpenAITimeout:  5 * time.Second,
			OpenAIMaxRetry: 1,
		}

		trace := &activityTrace{}

		env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, args converter.EncodedValues) {
			trace.recordStarted(info.ActivityType.Name)

			switch info.ActivityType.Name {
			case "StoreDocumentActivity":
				var in StoreDocumentInput
				_ = args.Get(&in)
				trace.mu.Lock()
				trace.storeIn = &in
				trace.mu.Unlock()
			case "DetectDocTypeActivity":
				var in DetectDocTypeInput
				_ = args.Get(&in)
				trace.mu.Lock()
				trace.detectIn = &in
				trace.mu.Unlock()
			case "ExtractFieldsWithOpenAIActivity":
				var in ExtractFieldsInput
				_ = args.Get(&in)
				trace.mu.Lock()
				trace.extractIn = &in
				trace.mu.Unlock()
			case "ValidateFieldsActivity":
				var in ValidateFieldsInput
				_ = args.Get(&in)
				trace.mu.Lock()
				trace.validateIn = &in
				trace.mu.Unlock()
			case "PersistResultActivity":
				var in PersistResultInput
				_ = args.Get(&in)
				trace.mu.Lock()
				trace.persistIn = &in
				trace.mu.Unlock()
			case "CorrectFieldsWithOpenAIActivity":
				trace.mu.Lock()
				trace.correctCalls++
				trace.mu.Unlock()
			case "QueueReviewActivity":
				trace.mu.Lock()
				trace.queueReviewCalls++
				trace.mu.Unlock()
			case "RejectDocumentActivity":
				trace.mu.Lock()
				trace.rejectCalls++
				trace.mu.Unlock()
			}
		})

		env.SetOnActivityCompletedListener(func(info *activity.Info, result converter.EncodedValue, _ error) {
			trace.recordCompleted(info.ActivityType.Name)

			switch info.ActivityType.Name {
			case "StoreDocumentActivity":
				var out StoreDocumentOutput
				_ = result.Get(&out)
				trace.mu.Lock()
				trace.storeOut = &out
				trace.mu.Unlock()
			case "DetectDocTypeActivity":
				var out DetectDocTypeOutput
				_ = result.Get(&out)
				trace.mu.Lock()
				trace.detectOut = &out
				trace.mu.Unlock()
			case "ExtractFieldsWithOpenAIActivity":
				var out ExtractFieldsOutput
				_ = result.Get(&out)
				trace.mu.Lock()
				trace.extractOut = &out
				trace.mu.Unlock()
			case "ValidateFieldsActivity":
				var out ValidateFieldsOutput
				_ = result.Get(&out)
				trace.mu.Lock()
				trace.validateOut = &out
				trace.mu.Unlock()
			}
		})

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

		documentID := "doc-happy-blackbox-1"
		filename := "payslip_happy_path.txt"
		uploadedContent := []byte("Payslip for Jane Doe. Gross Pay 2000. Net Pay 1500. Tax withheld 500. Pay period 2025-01-01 to 2025-01-15.")

		By("simulating document upload payload passed into workflow start")
		input := WorkflowInput{
			DocumentID: documentID,
			Filename:   filename,
			Content:    uploadedContent,
		}

		By("triggering the workflow execution")
		env.ExecuteWorkflow(DocumentIntakeWorkflow, input)

		By("validating workflow completes successfully")
		Expect(env.IsWorkflowCompleted()).To(BeTrue())
		Expect(env.GetWorkflowError()).ToNot(HaveOccurred())

		var wfResult WorkflowResult
		Expect(env.GetWorkflowResult(&wfResult)).To(Succeed())
		Expect(wfResult.DocumentID).To(Equal(documentID))
		Expect(wfResult.Status).To(Equal(domain.StatusCompleted))

		By("validating each activity input and output for happy path")
		Expect(trace.startedOrder).To(Equal([]string{
			"StoreDocumentActivity",
			"DetectDocTypeActivity",
			"ExtractFieldsWithOpenAIActivity",
			"ValidateFieldsActivity",
			"PersistResultActivity",
		}))
		Expect(trace.completedOrder).To(Equal([]string{
			"StoreDocumentActivity",
			"DetectDocTypeActivity",
			"ExtractFieldsWithOpenAIActivity",
			"ValidateFieldsActivity",
			"PersistResultActivity",
		}))

		Expect(trace.storeIn).ToNot(BeNil())
		Expect(trace.storeIn.DocumentID).To(Equal(documentID))
		Expect(trace.storeIn.Filename).To(Equal(filename))
		Expect(trace.storeIn.Content).To(Equal(uploadedContent))

		Expect(trace.storeOut).ToNot(BeNil())
		Expect(trace.storeOut.ObjectKey).To(Equal(documentID + "/" + filename))
		Expect(trace.storeOut.DocumentText).To(Equal(string(uploadedContent)))

		Expect(trace.detectIn).ToNot(BeNil())
		Expect(trace.detectIn.DocumentID).To(Equal(documentID))
		Expect(trace.detectIn.Filename).To(Equal(filename))
		Expect(trace.detectIn.DocumentText).To(Equal(string(uploadedContent)))

		Expect(trace.detectOut).ToNot(BeNil())
		Expect(trace.detectOut.DocType).To(Equal(domain.DocTypePayslip))

		Expect(trace.extractIn).ToNot(BeNil())
		Expect(trace.extractIn.DocumentID).To(Equal(documentID))
		Expect(trace.extractIn.DocType).To(Equal(domain.DocTypePayslip))
		Expect(trace.extractIn.DocumentText).To(Equal(string(uploadedContent)))

		Expect(trace.extractOut).ToNot(BeNil())
		Expect(trace.extractOut.Confidence).To(BeNumerically("~", 0.93, 0.0001))
		Expect(string(trace.extractOut.ExtractionJSON)).To(MatchJSON(`{
			"employee_name":"Jane Doe",
			"employer_name":"ACME Payroll",
			"pay_period_start":"2025-01-01",
			"pay_period_end":"2025-01-15",
			"gross_pay":2000,
			"net_pay":1500,
			"tax_withheld":500,
			"confidence":0.93
		}`))

		Expect(trace.validateIn).ToNot(BeNil())
		Expect(trace.validateIn.DocType).To(Equal(domain.DocTypePayslip))
		Expect(string(trace.validateIn.ExtractionJSON)).To(MatchJSON(string(trace.extractOut.ExtractionJSON)))

		Expect(trace.validateOut).ToNot(BeNil())
		Expect(trace.validateOut.FailedRules).To(BeEmpty())
		Expect(trace.validateOut.Confidence).To(BeNumerically("~", 0.93, 0.0001))

		Expect(trace.persistIn).ToNot(BeNil())
		Expect(trace.persistIn.DocumentID).To(Equal(documentID))
		Expect(trace.persistIn.Confidence).To(BeNumerically("~", 0.93, 0.0001))
		Expect(string(trace.persistIn.FinalJSON)).To(MatchJSON(string(trace.extractOut.ExtractionJSON)))

		Expect(trace.correctCalls).To(Equal(0))
		Expect(trace.queueReviewCalls).To(Equal(0))
		Expect(trace.rejectCalls).To(Equal(0))

		By("validating persisted side effects from activities and workflow")
		store.mu.Lock()
		rec, ok := store.docs[documentID]
		modelPhases := append([]string(nil), store.modelPhases[documentID]...)
		auditStates := append([]domain.AuditState(nil), store.audit[documentID]...)
		reviewItem, inReview := store.reviews[documentID]
		store.mu.Unlock()

		Expect(ok).To(BeTrue())
		Expect(rec.Status).To(Equal(domain.StatusCompleted))
		Expect(rec.Confidence).To(BeNumerically("~", 0.93, 0.0001))
		Expect(string(rec.FinalJSON)).To(MatchJSON(string(trace.extractOut.ExtractionJSON)))
		Expect(modelPhases).To(Equal([]string{modelOutputPhaseBase1}))
		Expect(auditStates).To(Equal([]domain.AuditState{
			domain.AuditStored,
			domain.AuditClassified,
			domain.AuditExtracted,
			domain.AuditCompleted,
		}))
		Expect(inReview).To(BeTrue())
		Expect(reviewItem.Status).To(Equal("COMPLETED"))
	})
})
