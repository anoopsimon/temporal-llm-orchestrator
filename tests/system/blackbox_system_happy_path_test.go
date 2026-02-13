//go:build system

package system_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.temporal.io/sdk/client"

	"temporal-llm-orchestrator/internal/domain"
	appTemporal "temporal-llm-orchestrator/internal/temporal"
)

var _ = Describe("System blackbox happy path", Ordered, func() {
	var repoRoot string
	var cfg systemTestConfig

	BeforeAll(func() {
		if os.Getenv("RUN_BLACKBOX_SYSTEM_TEST") != "1" {
			Skip("set RUN_BLACKBOX_SYSTEM_TEST=1 to run real blackbox system test")
		}

		cfg = loadSystemTestConfig()

		var err error
		repoRoot, err = findRepoRoot()
		Expect(err).ToNot(HaveOccurred())

		By("verifying required docker compose services (including worker) are already running")
		Expect(requireComposeServicesRunning(repoRoot, cfg.RequiredComposeServices)).To(Succeed())

		By("failing fast if infrastructure is unreachable")
		Expect(waitForPostgres(cfg.PostgresDSN, cfg.PreflightTimeout)).To(Succeed())
		Expect(waitForTemporal(cfg.TemporalAddress, cfg.TemporalNamespace, cfg.PreflightTimeout)).To(Succeed())
		Expect(waitForHTTPStatus(cfg.MinioReadyURL, 200, cfg.PreflightTimeout)).To(Succeed())
		Expect(waitForHTTPStatus(strings.TrimRight(cfg.APIBaseURL, "/")+cfg.APIHealthPath, 200, cfg.PreflightTimeout)).To(Succeed())
		Expect(waitForHTTPStatus(strings.TrimRight(cfg.APIBaseURL, "/")+cfg.APIReadyPath, 200, cfg.PreflightTimeout)).To(Succeed())
		Expect(waitForWorkerPoller(cfg.TemporalAddress, cfg.TemporalNamespace, cfg.TemporalTaskQueue, cfg.WorkerPollerTimeout)).To(Succeed())
		Expect(applyMigration(repoRoot, cfg.PostgresDSN)).To(Succeed())
	})

	It("uploads a real file over HTTP and completes the workflow via a real worker", func() {
		apiBaseURL := strings.TrimRight(cfg.APIBaseURL, "/")

		By("uploading a document exactly like a user")
		filePath := filepath.Join(repoRoot, cfg.UploadFixturePath)
		uploadedFile, err := os.ReadFile(filePath)
		Expect(err).ToNot(HaveOccurred())

		upload, err := uploadFile(apiBaseURL, filePath)
		Expect(err).ToNot(HaveOccurred())
		Expect(upload.DocumentID).ToNot(BeEmpty())
		Expect(upload.WorkflowID).ToNot(BeEmpty())
		Expect(upload.Status).To(Equal(domain.StatusReceived))

		By("polling workflow status until completion")
		var lastStatus statusResponse
		Eventually(func() domain.DocumentStatus {
			var statusErr error
			lastStatus, statusErr = getStatus(apiBaseURL, upload.DocumentID)
			Expect(statusErr).ToNot(HaveOccurred())
			Expect(lastStatus.Status).ToNot(Equal(domain.StatusFailed))
			Expect(lastStatus.Status).ToNot(Equal(domain.StatusRejected))
			return lastStatus.Status
		}, cfg.WorkflowCompletionTimeout, cfg.WorkflowPollInterval).Should(Equal(domain.StatusCompleted))
		Expect(lastStatus.DocType).To(Equal(domain.DocTypePayslip))

		By("checking final workflow result payload")
		result, err := getResult(apiBaseURL, upload.DocumentID)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.Status).To(Equal(domain.StatusCompleted))
		Expect(result.DocType).To(Equal(domain.DocTypePayslip))
		Expect(result.Confidence).To(BeNumerically(">=", 0.0))
		Expect(result.Confidence).To(BeNumerically("<=", 1.0))
		Expect(result.Result).To(HaveKey("employee_name"))
		Expect(result.Result).To(HaveKey("employer_name"))
		Expect(result.Result).To(HaveKey("pay_period_start"))
		Expect(result.Result).To(HaveKey("pay_period_end"))
		Expect(result.Result).To(HaveKeyWithValue("gross_pay", BeNumerically(">", 0)))
		Expect(result.Result).To(HaveKeyWithValue("net_pay", BeNumerically(">", 0)))
		Expect(result.Result).To(HaveKeyWithValue("tax_withheld", BeNumerically(">=", 0)))
		Expect(result.Result).To(HaveKeyWithValue("confidence", BeNumerically(">=", 0)))
		Expect(result.Result).To(HaveKeyWithValue("confidence", BeNumerically("<=", 1)))

		By("validating activity inputs and outputs from Temporal workflow history")
		temporalClient, err := client.Dial(client.Options{
			HostPort:  cfg.TemporalAddress,
			Namespace: cfg.TemporalNamespace,
		})
		Expect(err).ToNot(HaveOccurred())
		defer temporalClient.Close()

		trace, err := collectActivityTrace(context.Background(), temporalClient, upload.WorkflowID)
		Expect(err).ToNot(HaveOccurred())

		Expect(trace.ScheduledOrder).To(Equal(cfg.ExpectedActivityOrder))
		Expect(trace.CompletedOrder).To(Equal(cfg.ExpectedActivityOrder))

		storeIn := trace.Inputs["StoreDocumentActivity"].(appTemporal.StoreDocumentInput)
		Expect(storeIn.DocumentID).To(Equal(upload.DocumentID))
		Expect(storeIn.Filename).To(Equal(filepath.Base(filePath)))
		Expect(storeIn.Content).To(Equal(uploadedFile))

		storeOut := trace.Outputs["StoreDocumentActivity"].(appTemporal.StoreDocumentOutput)
		Expect(storeOut.ObjectKey).To(Equal(upload.DocumentID + "/" + filepath.Base(filePath)))
		Expect(storeOut.DocumentText).To(Equal(string(uploadedFile)))

		detectIn := trace.Inputs["DetectDocTypeActivity"].(appTemporal.DetectDocTypeInput)
		Expect(detectIn.DocumentID).To(Equal(upload.DocumentID))
		Expect(detectIn.Filename).To(Equal(filepath.Base(filePath)))
		Expect(detectIn.DocumentText).To(Equal(string(uploadedFile)))

		detectOut := trace.Outputs["DetectDocTypeActivity"].(appTemporal.DetectDocTypeOutput)
		Expect(detectOut.DocType).To(Equal(domain.DocTypePayslip))

		extractIn := trace.Inputs["ExtractFieldsWithOpenAIActivity"].(appTemporal.ExtractFieldsInput)
		Expect(extractIn.DocumentID).To(Equal(upload.DocumentID))
		Expect(extractIn.DocType).To(Equal(domain.DocTypePayslip))
		Expect(extractIn.DocumentText).To(Equal(string(uploadedFile)))

		extractOut := trace.Outputs["ExtractFieldsWithOpenAIActivity"].(appTemporal.ExtractFieldsOutput)
		Expect(extractOut.Confidence).To(BeNumerically(">", 0.0))
		Expect(len(extractOut.ExtractionJSON)).To(BeNumerically(">", 0))
		Expect(string(extractOut.ExtractionJSON)).To(MatchJSON(string(extractOut.ExtractionJSON)))

		validateIn := trace.Inputs["ValidateFieldsActivity"].(appTemporal.ValidateFieldsInput)
		Expect(validateIn.DocType).To(Equal(domain.DocTypePayslip))
		Expect(string(validateIn.ExtractionJSON)).To(MatchJSON(string(extractOut.ExtractionJSON)))

		validateOut := trace.Outputs["ValidateFieldsActivity"].(appTemporal.ValidateFieldsOutput)
		Expect(validateOut.FailedRules).To(BeEmpty())
		Expect(validateOut.Confidence).To(BeNumerically(">", 0.0))

		persistIn := trace.Inputs["PersistResultActivity"].(appTemporal.PersistResultInput)
		Expect(persistIn.DocumentID).To(Equal(upload.DocumentID))
		Expect(persistIn.Confidence).To(BeNumerically(">", 0.0))
		Expect(string(persistIn.FinalJSON)).To(MatchJSON(string(extractOut.ExtractionJSON)))

		By("verifying audit and model output records in Postgres")
		db, err := sql.Open("postgres", cfg.PostgresDSN)
		Expect(err).ToNot(HaveOccurred())
		defer db.Close()

		Expect(db.Ping()).To(Succeed())

		auditStates, err := fetchStringRows(db, `SELECT state FROM audit_log WHERE document_id = $1 ORDER BY id`, upload.DocumentID)
		Expect(err).ToNot(HaveOccurred())
		Expect(auditStates).To(ContainElement("STORED"))
		Expect(auditStates).To(ContainElement("CLASSIFIED"))
		Expect(auditStates).To(ContainElement("EXTRACTED"))
		Expect(auditStates).To(ContainElement("COMPLETED"))

		phases, err := fetchStringRows(db, `SELECT phase FROM extraction_attempts WHERE document_id = $1 ORDER BY id`, upload.DocumentID)
		Expect(err).ToNot(HaveOccurred())
		Expect(phases).ToNot(BeEmpty())
		Expect(phases[0]).To(Equal("BASE_ATTEMPT_1"))
	})
})
