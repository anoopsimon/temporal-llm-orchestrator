package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	braintrust "github.com/braintrustdata/braintrust-sdk-go"
	"github.com/braintrustdata/braintrust-sdk-go/eval"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	statusCompleted   = "COMPLETED"
	statusRejected    = "REJECTED"
	statusFailed      = "FAILED"
	statusNeedsReview = "NEEDS_REVIEW"
)

type evalInput struct {
	Name     string `json:"name"`
	DocType  string `json:"doc_type"`
	FilePath string `json:"file_path"`
}

type evalOutput struct {
	DocumentID     string                 `json:"document_id,omitempty"`
	Status         string                 `json:"status,omitempty"`
	DocType        string                 `json:"doc_type,omitempty"`
	Confidence     float64                `json:"confidence,omitempty"`
	Result         map[string]any         `json:"result,omitempty"`
	RejectedReason *string                `json:"rejected_reason,omitempty"`
	ReviewRequired bool                   `json:"review_required,omitempty"`
	MinConfidence  float64                `json:"min_confidence,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

type rawCase struct {
	Input    evalInput  `json:"input"`
	Expected evalOutput `json:"expected"`
}

type config struct {
	APIURL            string
	CasesPath         string
	Project           string
	Experiment        string
	AutoApproveReview bool
	PollInterval      time.Duration
	PollTimeout       time.Duration
	RequestTimeout    time.Duration
	Parallelism       int
}

type evalRunner struct {
	cfg    config
	client *http.Client
}

type uploadResponse struct {
	DocumentID string `json:"document_id"`
}

type statusResponse struct {
	Status  string `json:"status"`
	DocType string `json:"doc_type"`
}

func main() {
	ctx := context.Background()

	cfg, err := loadConfig()
	if err != nil {
		fail(err)
	}

	if strings.TrimSpace(os.Getenv("BRAINTRUST_API_KEY")) == "" {
		fail(errors.New("BRAINTRUST_API_KEY is required"))
	}

	cases, err := loadCases(cfg.CasesPath)
	if err != nil {
		fail(err)
	}

	runner := &evalRunner{
		cfg:    cfg,
		client: &http.Client{},
	}

	if err := runner.healthCheck(ctx); err != nil {
		fail(err)
	}

	tp := sdktrace.NewTracerProvider()
	defer func() {
		_ = tp.Shutdown(context.Background())
	}()

	bt, err := braintrust.New(
		tp,
		braintrust.WithProject(cfg.Project),
		braintrust.WithBlockingLogin(true),
	)
	if err != nil {
		fail(fmt.Errorf("failed to initialize Braintrust: %w", err))
	}

	evaluator := braintrust.NewEvaluator[evalInput, evalOutput](bt)

	result, err := evaluator.Run(ctx, eval.Opts[evalInput, evalOutput]{
		Experiment: cfg.Experiment,
		Dataset:    eval.NewDataset(cases),
		Task:       eval.T(runner.runCase),
		Scorers: []eval.Scorer[evalInput, evalOutput]{
			eval.NewScorer("status", scoreStatus),
			eval.NewScorer("doc_type", scoreDocType),
			eval.NewScorer("schema_conformance", scoreSchemaConformance),
			eval.NewScorer("field_accuracy", scoreFieldAccuracy),
			eval.NewScorer("validation_rules", scoreValidationRules),
			eval.NewScorer("confidence_threshold", scoreConfidenceThreshold),
			eval.NewScorer("review_avoidance", scoreReviewAvoidance),
		},
		Tags: []string{"document-intake", "extraction", "workflow-api"},
		Metadata: map[string]any{
			"service":             "temporal-llm-orchestrator",
			"api_url":             cfg.APIURL,
			"auto_approve_review": cfg.AutoApproveReview,
			"poll_timeout_sec":    int(cfg.PollTimeout.Seconds()),
		},
		Parallelism: cfg.Parallelism,
	})
	if err != nil {
		fail(fmt.Errorf("eval run failed: %w", err))
	}

	if runErr := result.Error(); runErr != nil {
		fail(fmt.Errorf("eval completed with errors: %w", runErr))
	}

	if link, err := result.Permalink(); err == nil && link != "" {
		fmt.Println("Braintrust report:", link)
	}

	fmt.Println(result.String())
}

func loadConfig() (config, error) {
	cfg := config{
		APIURL:            getenv("EVAL_API_URL", "http://localhost:8080"),
		CasesPath:         getenv("EVAL_CASES_PATH", "cases.json"),
		Project:           getenv("BRAINTRUST_PROJECT", "temporal-llm-orchestrator"),
		Experiment:        getenv("EVAL_EXPERIMENT", "document-intake-extraction-eval"),
		AutoApproveReview: getenvBool("EVAL_AUTO_APPROVE_REVIEW", false),
		PollInterval:      time.Duration(getenvInt("EVAL_POLL_INTERVAL_SEC", 2)) * time.Second,
		PollTimeout:       time.Duration(getenvInt("EVAL_POLL_TIMEOUT_SEC", 180)) * time.Second,
		RequestTimeout:    time.Duration(getenvInt("EVAL_REQUEST_TIMEOUT_SEC", 20)) * time.Second,
		Parallelism:       getenvInt("EVAL_PARALLELISM", 1),
	}

	if cfg.PollInterval <= 0 {
		return config{}, errors.New("EVAL_POLL_INTERVAL_SEC must be > 0")
	}
	if cfg.PollTimeout <= 0 {
		return config{}, errors.New("EVAL_POLL_TIMEOUT_SEC must be > 0")
	}
	if cfg.RequestTimeout <= 0 {
		return config{}, errors.New("EVAL_REQUEST_TIMEOUT_SEC must be > 0")
	}
	if cfg.Parallelism <= 0 {
		cfg.Parallelism = 1
	}

	return cfg, nil
}

func loadCases(path string) ([]eval.Case[evalInput, evalOutput], error) {
	resolved, err := resolvePath(path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("failed to read cases file %s: %w", resolved, err)
	}

	var raw []rawCase
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse cases file %s: %w", resolved, err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("cases file is empty: %s", resolved)
	}

	cases := make([]eval.Case[evalInput, evalOutput], 0, len(raw))
	for _, row := range raw {
		cases = append(cases, eval.Case[evalInput, evalOutput]{
			Input:    row.Input,
			Expected: row.Expected,
			Metadata: map[string]any{"name": row.Input.Name, "doc_type": row.Input.DocType, "file_path": row.Input.FilePath},
		})
	}
	return cases, nil
}

func (r *evalRunner) runCase(ctx context.Context, input evalInput) (evalOutput, error) {
	filePath, err := resolvePath(input.FilePath)
	if err != nil {
		return evalOutput{}, err
	}

	documentID, err := r.uploadDocument(ctx, filePath)
	if err != nil {
		return evalOutput{}, err
	}

	deadline := time.Now().Add(r.cfg.PollTimeout)
	reviewSent := false

	for {
		status, err := r.getStatus(ctx, documentID)
		if err != nil {
			return evalOutput{}, err
		}

		s := strings.ToUpper(status.Status)
		if s == statusNeedsReview {
			if r.cfg.AutoApproveReview && !reviewSent {
				if err := r.sendApprove(ctx, documentID); err != nil {
					return evalOutput{}, err
				}
				reviewSent = true
			} else {
				result, err := r.getResult(ctx, documentID)
				if err != nil {
					return evalOutput{}, err
				}
				result.Status = statusNeedsReview
				result.ReviewRequired = true
				if result.DocType == "" {
					result.DocType = status.DocType
				}
				return result, nil
			}
		}

		if s == statusCompleted || s == statusRejected || s == statusFailed {
			result, err := r.getResult(ctx, documentID)
			if err != nil {
				return evalOutput{}, err
			}
			result.ReviewRequired = reviewSent
			if result.Status == "" {
				result.Status = s
			}
			if result.DocType == "" {
				result.DocType = status.DocType
			}
			return result, nil
		}

		if time.Now().After(deadline) {
			return evalOutput{}, fmt.Errorf("timed out waiting for document %s", documentID)
		}

		select {
		case <-ctx.Done():
			return evalOutput{}, ctx.Err()
		case <-time.After(r.cfg.PollInterval):
		}
	}
}

func (r *evalRunner) healthCheck(ctx context.Context) error {
	var resp struct {
		Status string `json:"status"`
	}
	if err := r.doJSON(ctx, http.MethodGet, "/healthz", nil, &resp, ""); err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	if strings.ToLower(resp.Status) != "ok" {
		return fmt.Errorf("health check returned non-ok status: %s", resp.Status)
	}
	return nil
}

func (r *evalRunner) uploadDocument(ctx context.Context, filePath string) (string, error) {
	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return "", fmt.Errorf("failed to create multipart form: %w", err)
	}
	if _, err := part.Write(fileBytes); err != nil {
		return "", fmt.Errorf("failed to write multipart file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("failed to finalize multipart form: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, r.cfg.RequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, strings.TrimRight(r.cfg.APIURL, "/")+"/v1/documents", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("upload failed: %w", err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("upload response read failed: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("upload failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	var out uploadResponse
	if err := json.Unmarshal(payload, &out); err != nil {
		return "", fmt.Errorf("upload response decode failed: %w", err)
	}
	if out.DocumentID == "" {
		return "", fmt.Errorf("upload response missing document_id: %s", string(payload))
	}

	return out.DocumentID, nil
}

func (r *evalRunner) getStatus(ctx context.Context, documentID string) (statusResponse, error) {
	var out statusResponse
	err := r.doJSON(ctx, http.MethodGet, "/v1/documents/"+documentID+"/status", nil, &out, "")
	if err != nil {
		return statusResponse{}, err
	}
	return out, nil
}

func (r *evalRunner) getResult(ctx context.Context, documentID string) (evalOutput, error) {
	var out evalOutput
	err := r.doJSON(ctx, http.MethodGet, "/v1/documents/"+documentID+"/result", nil, &out, "")
	if err != nil {
		return evalOutput{}, err
	}
	return out, nil
}

func (r *evalRunner) sendApprove(ctx context.Context, documentID string) error {
	payload := map[string]any{
		"decision": "approve",
		"reviewer": "braintrust-go-eval",
		"reason":   "auto-approve for eval progression",
	}
	return r.doJSON(ctx, http.MethodPost, "/v1/documents/"+documentID+"/review", payload, nil, "application/json")
}

func (r *evalRunner) doJSON(ctx context.Context, method, path string, in any, out any, contentType string) error {
	reqCtx, cancel := context.WithTimeout(ctx, r.cfg.RequestTimeout)
	defer cancel()

	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(reqCtx, method, strings.TrimRight(r.cfg.APIURL, "/")+path, body)
	if err != nil {
		return err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("request failed: method=%s path=%s status=%d body=%s", method, path, resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	if out != nil {
		if err := json.Unmarshal(payload, out); err != nil {
			return fmt.Errorf("decode failed: %w (payload=%s)", err, string(payload))
		}
	}
	return nil
}

func scoreStatus(_ context.Context, tr eval.TaskResult[evalInput, evalOutput]) (eval.Scores, error) {
	expected := strings.ToUpper(strings.TrimSpace(tr.Expected.Status))
	if expected == "" {
		expected = statusCompleted
	}
	actual := strings.ToUpper(strings.TrimSpace(tr.Output.Status))
	if actual == expected {
		return eval.S(1), nil
	}
	return eval.S(0), nil
}

func scoreDocType(_ context.Context, tr eval.TaskResult[evalInput, evalOutput]) (eval.Scores, error) {
	expected := normalizeString(tr.Expected.DocType)
	if expected == "" {
		expected = normalizeString(tr.Input.DocType)
	}
	actual := normalizeString(tr.Output.DocType)
	if expected != "" && actual == expected {
		return eval.S(1), nil
	}
	return eval.S(0), nil
}

func scoreSchemaConformance(_ context.Context, tr eval.TaskResult[evalInput, evalOutput]) (eval.Scores, error) {
	docType := normalizeString(tr.Output.DocType)
	if docType == "" {
		docType = normalizeString(tr.Input.DocType)
	}

	spec := schemaSpecForDocType(docType)
	if spec == nil {
		return eval.S(0), nil
	}

	if tr.Output.Result == nil {
		return eval.S(0), nil
	}

	actualKeys := make(map[string]struct{}, len(tr.Output.Result))
	for k := range tr.Output.Result {
		actualKeys[k] = struct{}{}
	}

	for k := range spec.Required {
		if _, ok := actualKeys[k]; !ok {
			return eval.S(0), nil
		}
	}

	for k := range actualKeys {
		if _, ok := spec.Required[k]; ok {
			continue
		}
		if _, ok := spec.Optional[k]; ok {
			continue
		}
		return eval.S(0), nil
	}

	return eval.S(1), nil
}

func scoreFieldAccuracy(_ context.Context, tr eval.TaskResult[evalInput, evalOutput]) (eval.Scores, error) {
	expected := tr.Expected.Result
	actual := tr.Output.Result

	if len(expected) == 0 || actual == nil {
		return eval.S(0), nil
	}

	matched := 0
	total := 0
	for key, expectedValue := range expected {
		total++
		actualValue, ok := actual[key]
		if !ok {
			continue
		}
		if valuesMatch(expectedValue, actualValue) {
			matched++
		}
	}

	if total == 0 {
		return eval.S(0), nil
	}

	return eval.S(float64(matched) / float64(total)), nil
}

func scoreValidationRules(_ context.Context, tr eval.TaskResult[evalInput, evalOutput]) (eval.Scores, error) {
	result := tr.Output.Result
	if result == nil {
		return eval.S(0), nil
	}

	docType := normalizeString(tr.Output.DocType)
	if docType == "" {
		docType = normalizeString(tr.Input.DocType)
	}

	ok := false
	switch docType {
	case "payslip":
		ok = validatePayslip(result)
	case "invoice":
		ok = validateInvoice(result)
	default:
		ok = false
	}

	if ok {
		return eval.S(1), nil
	}
	return eval.S(0), nil
}

func scoreConfidenceThreshold(_ context.Context, tr eval.TaskResult[evalInput, evalOutput]) (eval.Scores, error) {
	threshold := tr.Expected.MinConfidence
	if threshold <= 0 {
		threshold = 0.75
	}
	if tr.Output.Confidence >= threshold {
		return eval.S(1), nil
	}
	return eval.S(0), nil
}

func scoreReviewAvoidance(_ context.Context, tr eval.TaskResult[evalInput, evalOutput]) (eval.Scores, error) {
	if tr.Output.ReviewRequired || strings.EqualFold(tr.Output.Status, statusNeedsReview) {
		return eval.S(0), nil
	}
	return eval.S(1), nil
}

type schemaSpec struct {
	Required map[string]struct{}
	Optional map[string]struct{}
}

func schemaSpecForDocType(docType string) *schemaSpec {
	switch docType {
	case "payslip":
		return &schemaSpec{
			Required: toSet([]string{"employee_name", "employer_name", "pay_period_start", "pay_period_end", "gross_pay", "net_pay", "tax_withheld", "confidence"}),
			Optional: toSet([]string{"superannuation"}),
		}
	case "invoice":
		return &schemaSpec{
			Required: toSet([]string{"supplier_name", "invoice_number", "invoice_date", "total_amount", "confidence"}),
			Optional: toSet([]string{"due_date", "gst_amount"}),
		}
	default:
		return nil
	}
}

func toSet(items []string) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, item := range items {
		out[item] = struct{}{}
	}
	return out
}

func validatePayslip(result map[string]any) bool {
	gross, ok1 := asFloat(result["gross_pay"])
	net, ok2 := asFloat(result["net_pay"])
	tax, ok3 := asFloat(result["tax_withheld"])
	confidence, ok4 := asFloat(result["confidence"])
	if !(ok1 && ok2 && ok3 && ok4) {
		return false
	}

	if gross < 0 || net < 0 || tax < 0 {
		return false
	}
	if gross < net {
		return false
	}

	if superRaw, ok := result["superannuation"]; ok && superRaw != nil {
		super, ok := asFloat(superRaw)
		if !ok || super < 0 {
			return false
		}
	}

	start, ok := asString(result["pay_period_start"])
	if !ok {
		return false
	}
	end, ok := asString(result["pay_period_end"])
	if !ok {
		return false
	}

	startDate, err := time.Parse("2006-01-02", start)
	if err != nil {
		return false
	}
	endDate, err := time.Parse("2006-01-02", end)
	if err != nil {
		return false
	}
	if startDate.After(endDate) {
		return false
	}

	if confidence < 0 || confidence > 1 {
		return false
	}

	return true
}

func validateInvoice(result map[string]any) bool {
	total, ok1 := asFloat(result["total_amount"])
	confidence, ok2 := asFloat(result["confidence"])
	if !(ok1 && ok2) {
		return false
	}
	if total <= 0 {
		return false
	}

	if gstRaw, ok := result["gst_amount"]; ok && gstRaw != nil {
		gst, ok := asFloat(gstRaw)
		if !ok || gst < 0 {
			return false
		}
	}

	invoiceDate, ok := asString(result["invoice_date"])
	if !ok {
		return false
	}
	if _, err := time.Parse("2006-01-02", invoiceDate); err != nil {
		return false
	}

	if dueRaw, ok := result["due_date"]; ok && dueRaw != nil {
		dueDate, ok := asString(dueRaw)
		if !ok {
			return false
		}
		if _, err := time.Parse("2006-01-02", dueDate); err != nil {
			return false
		}
	}

	if confidence < 0 || confidence > 1 {
		return false
	}

	return true
}

func valuesMatch(expected, actual any) bool {
	if expected == nil {
		return actual == nil
	}

	ef, eok := asFloat(expected)
	af, aok := asFloat(actual)
	if eok && aok {
		return abs(ef-af) <= 0.01
	}

	return normalizeString(expected) == normalizeString(actual)
}

func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		n, err := t.Float64()
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func asString(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	return s, true
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func normalizeString(v any) string {
	if v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		s = fmt.Sprintf("%v", v)
	}
	return strings.ToLower(strings.TrimSpace(s))
}

func resolvePath(path string) (string, error) {
	if path == "" {
		return "", errors.New("path is empty")
	}
	if filepath.IsAbs(path) {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		return "", fmt.Errorf("path not found: %s", path)
	}

	candidates := []string{
		path,
		filepath.Join("..", "..", path),
	}

	for _, c := range candidates {
		absPath, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if _, err := os.Stat(absPath); err == nil {
			return absPath, nil
		}
	}

	return "", fmt.Errorf("path not found: %s", path)
}

func getenv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	var out int
	if _, err := fmt.Sscanf(v, "%d", &out); err != nil {
		return fallback
	}
	return out
}

func getenvBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return strings.EqualFold(v, "1") || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
