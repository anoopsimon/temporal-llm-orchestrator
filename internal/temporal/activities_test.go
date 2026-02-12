package temporal

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"temporal-llm-orchestrator/internal/domain"
	"temporal-llm-orchestrator/internal/openai"
)

type fakeStore struct {
	mu          sync.Mutex
	docs        map[string]domain.DocumentRecord
	modelPhases map[string][]string
	reviews     map[string]domain.ReviewQueueItem
	audit       map[string][]domain.AuditState
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		docs:        make(map[string]domain.DocumentRecord),
		modelPhases: make(map[string][]string),
		reviews:     make(map[string]domain.ReviewQueueItem),
		audit:       make(map[string][]domain.AuditState),
	}
}

func (f *fakeStore) UpsertDocument(_ context.Context, rec domain.DocumentRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing, ok := f.docs[rec.ID]
	if ok {
		if existing.ObjectKey != "" {
			rec.ObjectKey = existing.ObjectKey
		}
		if existing.RawText != "" {
			rec.RawText = existing.RawText
		}
	}
	f.docs[rec.ID] = rec
	return nil
}

func (f *fakeStore) GetDocument(_ context.Context, documentID string) (domain.DocumentRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.docs[documentID]
	if !ok {
		return domain.DocumentRecord{}, sql.ErrNoRows
	}
	return rec, nil
}

func (f *fakeStore) UpdateDocumentClassification(_ context.Context, documentID string, docType domain.DocType) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.docs[documentID]
	rec.DocType = docType
	rec.Status = domain.StatusClassified
	f.docs[documentID] = rec
	return nil
}

func (f *fakeStore) InsertAudit(_ context.Context, documentID string, state domain.AuditState, _ any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audit[documentID] = append(f.audit[documentID], state)
	return nil
}

func (f *fakeStore) SaveModelOutput(_ context.Context, documentID string, phase string, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.modelPhases[documentID] = append(f.modelPhases[documentID], phase)
	return nil
}

func (f *fakeStore) SaveCurrentExtraction(_ context.Context, documentID string, payload []byte, confidence float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.docs[documentID]
	rec.ID = documentID
	rec.CurrentJSON = payload
	rec.Confidence = confidence
	rec.Status = domain.StatusExtracted
	f.docs[documentID] = rec
	return nil
}

func (f *fakeStore) GetCurrentExtraction(_ context.Context, documentID string) ([]byte, float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.docs[documentID]
	if !ok || len(rec.CurrentJSON) == 0 {
		return nil, 0, sql.ErrNoRows
	}
	return rec.CurrentJSON, rec.Confidence, nil
}

func (f *fakeStore) QueueReview(_ context.Context, documentID string, failedRules []string, currentJSON []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.docs[documentID]
	rec.Status = domain.StatusNeedsReview
	f.docs[documentID] = rec
	f.reviews[documentID] = domain.ReviewQueueItem{DocumentID: documentID, FailedRules: failedRules, CurrentJSON: currentJSON, Status: "PENDING"}
	return nil
}

func (f *fakeStore) ResolveReview(_ context.Context, documentID string, decision string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	item := f.reviews[documentID]
	item.DocumentID = documentID
	item.Status = decision
	f.reviews[documentID] = item
	return nil
}

func (f *fakeStore) SaveFinalResult(_ context.Context, documentID string, payload []byte, confidence float64, status domain.DocumentStatus, rejectedReason *string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.docs[documentID]
	rec.ID = documentID
	rec.FinalJSON = payload
	rec.Confidence = confidence
	rec.Status = status
	rec.RejectedReason = rejectedReason
	f.docs[documentID] = rec
	return nil
}

type fakeBlob struct{}

func (f *fakeBlob) PutDocument(_ context.Context, documentID, filename string, _ []byte) (string, error) {
	return documentID + "/" + filename, nil
}

type stubLLM struct {
	mu        sync.Mutex
	responses []string
	errs      []error
	calls     []openai.CompletionRequest
}

func (s *stubLLM) CompleteJSON(_ context.Context, req openai.CompletionRequest) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, req)
	idx := len(s.calls) - 1
	if idx < len(s.errs) && s.errs[idx] != nil {
		return "", s.errs[idx]
	}
	if idx < len(s.responses) {
		return s.responses[idx], nil
	}
	return "{}", nil
}

func TestExtractFieldsWithRepairPath(t *testing.T) {
	store := newFakeStore()
	store.docs["doc-1"] = domain.DocumentRecord{ID: "doc-1"}

	llm := &stubLLM{responses: []string{
		`{"employee_name":"Jane"`,
		`{"employee_name":"Jane","employer_name":"ACME","pay_period_start":"2025-01-01","pay_period_end":"2025-01-15","gross_pay":2000,"net_pay":1500,"tax_withheld":500,"confidence":0.9}`,
	}}
	acts := &Activities{
		Store:          store,
		Blob:           &fakeBlob{},
		LLM:            llm,
		OpenAIModel:    "gpt-4o-mini",
		OpenAITimeout:  10 * time.Second,
		OpenAIMaxRetry: 1,
	}

	out, err := acts.ExtractFieldsWithOpenAIActivity(context.Background(), ExtractFieldsInput{
		DocumentID:   "doc-1",
		DocType:      domain.DocTypePayslip,
		DocumentText: "Payslip gross pay and net pay",
	})
	require.NoError(t, err)
	require.Greater(t, len(out.ExtractionJSON), 0)
	require.Equal(t, 0.9, out.Confidence)
	require.Len(t, llm.calls, 2)
	require.Equal(t, []string{modelOutputPhaseBase1, modelOutputPhaseRepair1}, store.modelPhases["doc-1"])
}
