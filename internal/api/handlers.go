package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/client"

	"temporal-llm-orchestrator/internal/config"
	"temporal-llm-orchestrator/internal/domain"
	"temporal-llm-orchestrator/internal/storage"
	appTemporal "temporal-llm-orchestrator/internal/temporal"
)

type Handler struct {
	cfg            config.Config
	store          *storage.PostgresStore
	blob           uploadBlobStore
	temporalClient client.Client
}

type uploadBlobStore interface {
	PutDocument(ctx context.Context, documentID, filename string, content []byte) (string, error)
}

type statusResponse struct {
	DocumentID string                `json:"document_id"`
	Status     domain.DocumentStatus `json:"status"`
	DocType    domain.DocType        `json:"doc_type"`
}

type resultResponse struct {
	DocumentID     string                `json:"document_id"`
	Status         domain.DocumentStatus `json:"status"`
	DocType        domain.DocType        `json:"doc_type"`
	Confidence     float64               `json:"confidence"`
	Result         json.RawMessage       `json:"result,omitempty"`
	RejectedReason *string               `json:"rejected_reason,omitempty"`
}

type reviewRequest struct {
	Decision    string          `json:"decision"`
	Corrections json.RawMessage `json:"corrections,omitempty"`
	Reviewer    string          `json:"reviewer,omitempty"`
	Reason      string          `json:"reason,omitempty"`
}

func NewHandler(cfg config.Config, store *storage.PostgresStore, blob uploadBlobStore, temporalClient client.Client) *Handler {
	return &Handler{cfg: cfg, store: store, blob: blob, temporalClient: temporalClient}
}

func (h *Handler) UploadDocument(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	if err := r.ParseMultipartForm(h.cfg.AllowedUploadBytes); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid multipart payload"})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "file form field is required"})
		return
	}
	defer file.Close()

	body, err := io.ReadAll(io.LimitReader(file, h.cfg.AllowedUploadBytes+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "failed to read file"})
		return
	}
	if int64(len(body)) > h.cfg.AllowedUploadBytes {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "file exceeds size limit"})
		return
	}

	documentID := uuid.NewString()
	if err := h.store.CreateReceivedDocument(ctx, documentID, header.Filename); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to create document"})
		return
	}

	objectKey, err := h.blob.PutDocument(ctx, documentID, header.Filename, body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to upload file"})
		return
	}
	if err := h.store.SetDocumentObjectKey(ctx, documentID, objectKey); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to record upload"})
		return
	}

	workflowID := h.workflowID(documentID)
	// Upload endpoint persists file bytes to object storage and returns quickly.
	// Workflow start is decoupled: event-handler listens for object-created events and starts Temporal workflow.

	writeJSON(w, http.StatusAccepted, map[string]any{
		"document_id": documentID,
		"workflow_id": workflowID,
		"status":      domain.StatusReceived,
	})
}

func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request, documentID string) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	status, docType, err := h.store.GetDocumentStatus(ctx, documentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "document not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to fetch status"})
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{DocumentID: documentID, Status: status, DocType: docType})
}

func (h *Handler) GetResult(w http.ResponseWriter, r *http.Request, documentID string) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	rec, err := h.store.GetDocumentResult(ctx, documentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "document not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to fetch result"})
		return
	}

	payload := rec.FinalJSON
	if len(payload) == 0 {
		payload = rec.CurrentJSON
	}

	writeJSON(w, http.StatusOK, resultResponse{
		DocumentID:     documentID,
		Status:         rec.Status,
		DocType:        rec.DocType,
		Confidence:     rec.Confidence,
		Result:         payload,
		RejectedReason: rec.RejectedReason,
	})
}

func (h *Handler) SubmitReview(w http.ResponseWriter, r *http.Request, documentID string) {
	var req reviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}

	decision := domain.ReviewDecisionType(req.Decision)
	switch decision {
	case domain.ReviewDecisionApprove, domain.ReviewDecisionReject, domain.ReviewDecisionCorrect:
	default:
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid decision"})
		return
	}

	signal := appTemporal.ReviewDecisionSignal{
		Decision:    decision,
		Corrections: req.Corrections,
		Reviewer:    req.Reviewer,
		Reason:      req.Reason,
	}
	// Review endpoint sends a Temporal signal to an already-running workflow.
	// Signals do not start workflows; UploadDocument starts the workflow.
	if err := h.temporalClient.SignalWorkflow(r.Context(), h.workflowID(documentID), "", appTemporal.ReviewDecisionSignalName, signal); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to signal workflow"})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"document_id": documentID, "status": "review_signal_sent"})
}

func (h *Handler) PendingReviews(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	items, err := h.store.ListPendingReviews(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to fetch pending reviews"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) Readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *Handler) workflowID(documentID string) string {
	return fmt.Sprintf("%s-%s", h.cfg.WorkflowIDPrefix, documentID)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
