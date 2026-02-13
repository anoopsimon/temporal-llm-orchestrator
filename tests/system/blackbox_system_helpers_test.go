//go:build system

package system_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/lib/pq"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"

	"temporal-llm-orchestrator/internal/domain"
	appTemporal "temporal-llm-orchestrator/internal/temporal"
)

type uploadResponse struct {
	DocumentID string                `json:"document_id"`
	WorkflowID string                `json:"workflow_id"`
	Status     domain.DocumentStatus `json:"status"`
}

type statusResponse struct {
	DocumentID string                `json:"document_id"`
	Status     domain.DocumentStatus `json:"status"`
	DocType    domain.DocType        `json:"doc_type"`
}

type resultResponse struct {
	DocumentID string                `json:"document_id"`
	Status     domain.DocumentStatus `json:"status"`
	DocType    domain.DocType        `json:"doc_type"`
	Confidence float64               `json:"confidence"`
	Result     map[string]any        `json:"result"`
}

type activityTrace struct {
	ScheduledOrder []string
	CompletedOrder []string
	Inputs         map[string]any
	Outputs        map[string]any
}

type systemTestConfig struct {
	PostgresDSN       string
	TemporalAddress   string
	TemporalNamespace string
	TemporalTaskQueue string
	APIBaseURL        string
	APIHealthPath     string
	APIReadyPath      string
	MinioReadyURL     string
	UploadFixturePath string

	RequiredComposeServices []string
	ExpectedActivityOrder   []string

	PreflightTimeout          time.Duration
	WorkerPollerTimeout       time.Duration
	WorkflowCompletionTimeout time.Duration
	WorkflowPollInterval      time.Duration
}

var defaultSystemTestConfig = systemTestConfig{
	PostgresDSN:       "postgres://postgres:postgres@localhost:5432/intake?sslmode=disable",
	TemporalAddress:   "localhost:7233",
	TemporalNamespace: "default",
	TemporalTaskQueue: "document-intake-task-queue",
	APIBaseURL:        "http://localhost:8080",
	APIHealthPath:     "/healthz",
	APIReadyPath:      "/readyz",
	MinioReadyURL:     "http://localhost:9000/minio/health/ready",
	UploadFixturePath: "testdata/payslip.txt",
	RequiredComposeServices: []string{
		"app-postgres",
		"temporal-postgres",
		"temporal",
		"minio",
		"api",
		"worker",
	},
	ExpectedActivityOrder: []string{
		"StoreDocumentActivity",
		"DetectDocTypeActivity",
		"ExtractFieldsWithOpenAIActivity",
		"ValidateFieldsActivity",
		"PersistResultActivity",
	},
	PreflightTimeout:          8 * time.Second,
	WorkerPollerTimeout:       12 * time.Second,
	WorkflowCompletionTimeout: 90 * time.Second,
	WorkflowPollInterval:      time.Second,
}

func waitForPostgres(dsn string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		db, err := sql.Open("postgres", dsn)
		if err == nil {
			pingErr := db.Ping()
			_ = db.Close()
			if pingErr == nil {
				return nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("postgres not ready within %s", timeout)
}

func waitForTemporal(hostPort string, namespace string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := client.Dial(client.Options{
			HostPort:  hostPort,
			Namespace: namespace,
		})
		if err == nil {
			c.Close()
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("temporal not ready within %s", timeout)
}

func waitForHTTPStatus(url string, expectedStatus int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	httpClient := &http.Client{Timeout: 5 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := httpClient.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == expectedStatus {
				return nil
			}
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("endpoint %s did not return %d in %s", url, expectedStatus, timeout)
}

func applyMigration(repoRoot string, dsn string) error {
	migrationPath := filepath.Join(repoRoot, "db", "migrations", "001_init.sql")
	sqlText, err := os.ReadFile(migrationPath)
	if err != nil {
		return err
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec(string(sqlText))
	return err
}

func loadSystemTestConfig() systemTestConfig {
	cfg := defaultSystemTestConfig
	cfg.RequiredComposeServices = append([]string(nil), defaultSystemTestConfig.RequiredComposeServices...)
	cfg.ExpectedActivityOrder = append([]string(nil), defaultSystemTestConfig.ExpectedActivityOrder...)

	cfg.PostgresDSN = getenv("SYSTEM_TEST_POSTGRES_DSN", cfg.PostgresDSN)
	cfg.TemporalAddress = getenv("SYSTEM_TEST_TEMPORAL_ADDRESS", cfg.TemporalAddress)
	cfg.TemporalNamespace = getenv("SYSTEM_TEST_TEMPORAL_NAMESPACE", cfg.TemporalNamespace)
	cfg.TemporalTaskQueue = getenv("SYSTEM_TEST_TEMPORAL_TASK_QUEUE", cfg.TemporalTaskQueue)
	cfg.APIBaseURL = getenv("SYSTEM_TEST_API_URL", cfg.APIBaseURL)
	cfg.APIHealthPath = getenv("SYSTEM_TEST_API_HEALTH_PATH", cfg.APIHealthPath)
	cfg.APIReadyPath = getenv("SYSTEM_TEST_API_READY_PATH", cfg.APIReadyPath)
	cfg.MinioReadyURL = getenv("SYSTEM_TEST_MINIO_READY_URL", cfg.MinioReadyURL)
	cfg.UploadFixturePath = getenv("SYSTEM_TEST_UPLOAD_FIXTURE", cfg.UploadFixturePath)
	cfg.PreflightTimeout = getenvDuration("SYSTEM_TEST_PREFLIGHT_TIMEOUT", cfg.PreflightTimeout)
	cfg.WorkerPollerTimeout = getenvDuration("SYSTEM_TEST_WORKER_POLLER_TIMEOUT", cfg.WorkerPollerTimeout)
	cfg.WorkflowCompletionTimeout = getenvDuration("SYSTEM_TEST_WORKFLOW_TIMEOUT", cfg.WorkflowCompletionTimeout)
	cfg.WorkflowPollInterval = getenvDuration("SYSTEM_TEST_WORKFLOW_POLL_INTERVAL", cfg.WorkflowPollInterval)

	return cfg
}

func waitForWorkerPoller(hostPort string, namespace string, taskQueue string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := client.Dial(client.Options{
			HostPort:  hostPort,
			Namespace: namespace,
		})
		if err == nil {
			resp, descErr := c.DescribeTaskQueue(context.Background(), taskQueue, enumspb.TASK_QUEUE_TYPE_ACTIVITY)
			c.Close()
			if descErr == nil && len(resp.Pollers) > 0 {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("no worker poller found for task queue %q within %s", taskQueue, timeout)
}

func uploadFile(apiBaseURL string, filePath string) (uploadResponse, error) {
	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		return uploadResponse{}, err
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return uploadResponse{}, err
	}
	if _, err := part.Write(fileBytes); err != nil {
		return uploadResponse{}, err
	}
	if err := writer.Close(); err != nil {
		return uploadResponse{}, err
	}

	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(apiBaseURL, "/")+"/v1/documents", &body)
	if err != nil {
		return uploadResponse{}, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return uploadResponse{}, err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return uploadResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return uploadResponse{}, fmt.Errorf("upload failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	var out uploadResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		return uploadResponse{}, err
	}
	return out, nil
}

func getStatus(apiBaseURL string, documentID string) (statusResponse, error) {
	url := strings.TrimRight(apiBaseURL, "/") + "/v1/documents/" + documentID + "/status"
	return doGETJSON[statusResponse](url)
}

func getResult(apiBaseURL string, documentID string) (resultResponse, error) {
	url := strings.TrimRight(apiBaseURL, "/") + "/v1/documents/" + documentID + "/result"
	return doGETJSON[resultResponse](url)
}

func doGETJSON[T any](url string) (T, error) {
	var zero T
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return zero, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, fmt.Errorf("request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	var out T
	if err := json.Unmarshal(payload, &out); err != nil {
		return zero, err
	}
	return out, nil
}

func collectActivityTrace(ctx context.Context, temporalClient client.Client, workflowID string) (activityTrace, error) {
	trace := activityTrace{
		Inputs:  make(map[string]any),
		Outputs: make(map[string]any),
	}
	dc := converter.GetDefaultDataConverter()
	scheduledByEventID := make(map[int64]string)

	iter := temporalClient.GetWorkflowHistory(ctx, workflowID, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		if err != nil {
			return activityTrace{}, err
		}

		if scheduled := event.GetActivityTaskScheduledEventAttributes(); scheduled != nil {
			name := scheduled.GetActivityType().GetName()
			trace.ScheduledOrder = append(trace.ScheduledOrder, name)
			scheduledByEventID[event.GetEventId()] = name

			input, err := decodeActivityInput(dc, name, scheduled.GetInput())
			if err != nil {
				return activityTrace{}, err
			}
			trace.Inputs[name] = input
			continue
		}

		if completed := event.GetActivityTaskCompletedEventAttributes(); completed != nil {
			name := scheduledByEventID[completed.GetScheduledEventId()]
			trace.CompletedOrder = append(trace.CompletedOrder, name)

			output, err := decodeActivityOutput(dc, name, completed.GetResult())
			if err != nil {
				return activityTrace{}, err
			}
			trace.Outputs[name] = output
		}
	}
	return trace, nil
}

func collectWorkflowSignalNames(ctx context.Context, temporalClient client.Client, workflowID string) ([]string, error) {
	var signalNames []string
	iter := temporalClient.GetWorkflowHistory(ctx, workflowID, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		if err != nil {
			return nil, err
		}
		if signaled := event.GetWorkflowExecutionSignaledEventAttributes(); signaled != nil {
			signalNames = append(signalNames, signaled.GetSignalName())
		}
	}
	return signalNames, nil
}

func decodeActivityInput(dc converter.DataConverter, name string, payloads *commonpb.Payloads) (any, error) {
	if payloads == nil {
		return nil, nil
	}

	switch name {
	case "StoreDocumentActivity":
		var in appTemporal.StoreDocumentInput
		if err := dc.FromPayloads(payloads, &in); err != nil {
			return nil, err
		}
		return in, nil
	case "DetectDocTypeActivity":
		var in appTemporal.DetectDocTypeInput
		if err := dc.FromPayloads(payloads, &in); err != nil {
			return nil, err
		}
		return in, nil
	case "ExtractFieldsWithOpenAIActivity":
		var in appTemporal.ExtractFieldsInput
		if err := dc.FromPayloads(payloads, &in); err != nil {
			return nil, err
		}
		return in, nil
	case "ValidateFieldsActivity":
		var in appTemporal.ValidateFieldsInput
		if err := dc.FromPayloads(payloads, &in); err != nil {
			return nil, err
		}
		return in, nil
	case "PersistResultActivity":
		var in appTemporal.PersistResultInput
		if err := dc.FromPayloads(payloads, &in); err != nil {
			return nil, err
		}
		return in, nil
	default:
		var generic map[string]any
		if err := dc.FromPayloads(payloads, &generic); err != nil {
			return nil, err
		}
		return generic, nil
	}
}

func decodeActivityOutput(dc converter.DataConverter, name string, payloads *commonpb.Payloads) (any, error) {
	if payloads == nil || len(payloads.Payloads) == 0 {
		return struct{}{}, nil
	}

	switch name {
	case "StoreDocumentActivity":
		var out appTemporal.StoreDocumentOutput
		if err := dc.FromPayloads(payloads, &out); err != nil {
			return nil, err
		}
		return out, nil
	case "DetectDocTypeActivity":
		var out appTemporal.DetectDocTypeOutput
		if err := dc.FromPayloads(payloads, &out); err != nil {
			return nil, err
		}
		return out, nil
	case "ExtractFieldsWithOpenAIActivity":
		var out appTemporal.ExtractFieldsOutput
		if err := dc.FromPayloads(payloads, &out); err != nil {
			return nil, err
		}
		return out, nil
	case "ValidateFieldsActivity":
		var out appTemporal.ValidateFieldsOutput
		if err := dc.FromPayloads(payloads, &out); err != nil {
			return nil, err
		}
		return out, nil
	default:
		var generic map[string]any
		if err := dc.FromPayloads(payloads, &generic); err != nil {
			return nil, err
		}
		return generic, nil
	}
}

func fetchStringRows(db *sql.DB, query string, args ...any) ([]string, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func runCommand(workdir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func requireComposeServicesRunning(repoRoot string, services []string) error {
	out, err := runCommand(repoRoot, "docker", "compose", "ps", "--services", "--status", "running")
	if err != nil {
		return fmt.Errorf("failed to inspect docker compose services: %w (output: %s)", err, strings.TrimSpace(out))
	}

	running := make(map[string]struct{})
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		running[name] = struct{}{}
	}

	var missing []string
	for _, svc := range services {
		if _, ok := running[svc]; !ok {
			missing = append(missing, svc)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("required compose services are not running: %s (run `docker compose up -d %s`)", strings.Join(missing, ", "), strings.Join(services, " "))
	}
	return nil
}

func getenv(key string, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func getenvDuration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("go.mod not found from current directory")
}
