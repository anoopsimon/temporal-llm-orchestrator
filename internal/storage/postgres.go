package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/lib/pq"

	"temporal-llm-orchestrator/internal/domain"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(dsn string) (*PostgresStore, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) Close() error {
	return s.db.Close()
}

func (s *PostgresStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *PostgresStore) CreateReceivedDocument(ctx context.Context, documentID, filename string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO documents (id, filename, status, doc_type)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO NOTHING
	`, documentID, filename, domain.StatusReceived, domain.DocTypeUnknown)
	return err
}

func (s *PostgresStore) SetDocumentObjectKey(ctx context.Context, documentID, objectKey string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE documents
		SET object_key = $2, updated_at = NOW()
		WHERE id = $1
	`, documentID, objectKey)
	return err
}

func (s *PostgresStore) UpsertDocument(ctx context.Context, rec domain.DocumentRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO documents (id, filename, object_key, raw_text, doc_type, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET
			filename = EXCLUDED.filename,
			object_key = CASE WHEN documents.object_key IS NULL OR documents.object_key = '' THEN EXCLUDED.object_key ELSE documents.object_key END,
			raw_text = CASE WHEN documents.raw_text IS NULL OR documents.raw_text = '' THEN EXCLUDED.raw_text ELSE documents.raw_text END,
			doc_type = CASE WHEN documents.doc_type = $7 THEN EXCLUDED.doc_type ELSE documents.doc_type END,
			status = EXCLUDED.status,
			updated_at = NOW()
	`, rec.ID, rec.Filename, rec.ObjectKey, rec.RawText, rec.DocType, rec.Status, domain.DocTypeUnknown)
	return err
}

func (s *PostgresStore) GetDocument(ctx context.Context, documentID string) (domain.DocumentRecord, error) {
	var rec domain.DocumentRecord
	var currentJSON []byte
	var finalJSON []byte
	var rejectedReason sql.NullString
	row := s.db.QueryRowContext(ctx, `
		SELECT id, filename, COALESCE(object_key, ''), COALESCE(raw_text, ''), doc_type, status,
		       current_json, final_json, COALESCE(confidence, 0), rejected_reason
		FROM documents
		WHERE id = $1
	`, documentID)
	if err := row.Scan(
		&rec.ID,
		&rec.Filename,
		&rec.ObjectKey,
		&rec.RawText,
		&rec.DocType,
		&rec.Status,
		&currentJSON,
		&finalJSON,
		&rec.Confidence,
		&rejectedReason,
	); err != nil {
		return domain.DocumentRecord{}, err
	}
	rec.CurrentJSON = currentJSON
	rec.FinalJSON = finalJSON
	if rejectedReason.Valid {
		rec.RejectedReason = &rejectedReason.String
	}
	return rec, nil
}

func (s *PostgresStore) UpdateDocumentClassification(ctx context.Context, documentID string, docType domain.DocType) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE documents
		SET doc_type = $2, status = $3, updated_at = NOW()
		WHERE id = $1
	`, documentID, docType, domain.StatusClassified)
	return err
}

func (s *PostgresStore) InsertAudit(ctx context.Context, documentID string, state domain.AuditState, detail any) error {
	var payload []byte
	switch v := detail.(type) {
	case nil:
		payload = []byte("{}")
	case []byte:
		payload = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		payload = b
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_log (document_id, state, detail)
		VALUES ($1, $2, $3::jsonb)
	`, documentID, state, string(payload))
	return err
}

func (s *PostgresStore) SaveModelOutput(ctx context.Context, documentID string, phase string, output string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO extraction_attempts (document_id, phase, output)
		VALUES ($1, $2, $3)
	`, documentID, phase, output)
	return err
}

func (s *PostgresStore) SaveCurrentExtraction(ctx context.Context, documentID string, payload []byte, confidence float64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE documents
		SET current_json = $2::jsonb,
		    confidence = $3,
		    status = $4,
		    updated_at = NOW()
		WHERE id = $1
	`, documentID, string(payload), confidence, domain.StatusExtracted)
	return err
}

func (s *PostgresStore) GetCurrentExtraction(ctx context.Context, documentID string) ([]byte, float64, error) {
	var payload []byte
	var confidence float64
	row := s.db.QueryRowContext(ctx, `
		SELECT current_json, COALESCE(confidence, 0)
		FROM documents
		WHERE id = $1
	`, documentID)
	if err := row.Scan(&payload, &confidence); err != nil {
		return nil, 0, err
	}
	return payload, confidence, nil
}

func (s *PostgresStore) QueueReview(ctx context.Context, documentID string, failedRules []string, currentJSON []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO review_queue (document_id, failed_rules, current_json, status)
		VALUES ($1, $2, $3::jsonb, 'PENDING')
		ON CONFLICT (document_id) DO UPDATE SET
			failed_rules = EXCLUDED.failed_rules,
			current_json = EXCLUDED.current_json,
			status = 'PENDING',
			updated_at = NOW()
	`, documentID, pq.Array(failedRules), string(currentJSON))
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE documents
		SET status = $2, updated_at = NOW()
		WHERE id = $1
	`, documentID, domain.StatusNeedsReview)
	if err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *PostgresStore) ResolveReview(ctx context.Context, documentID string, decision string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE review_queue
		SET status = $2, updated_at = NOW()
		WHERE document_id = $1
	`, documentID, decision)
	return err
}

func (s *PostgresStore) SaveFinalResult(ctx context.Context, documentID string, payload []byte, confidence float64, status domain.DocumentStatus, rejectedReason *string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE documents
		SET final_json = CASE WHEN $2 = '' THEN final_json ELSE $2::jsonb END,
		    confidence = $3,
		    status = $4,
		    rejected_reason = $5,
		    updated_at = NOW()
		WHERE id = $1
	`, documentID, string(payload), confidence, status, rejectedReason)
	return err
}

func (s *PostgresStore) GetDocumentStatus(ctx context.Context, documentID string) (domain.DocumentStatus, domain.DocType, error) {
	var status domain.DocumentStatus
	var docType domain.DocType
	row := s.db.QueryRowContext(ctx, `SELECT status, doc_type FROM documents WHERE id = $1`, documentID)
	if err := row.Scan(&status, &docType); err != nil {
		return "", "", err
	}
	return status, docType, nil
}

func (s *PostgresStore) GetDocumentResult(ctx context.Context, documentID string) (domain.DocumentRecord, error) {
	rec, err := s.GetDocument(ctx, documentID)
	if err != nil {
		return domain.DocumentRecord{}, err
	}
	return rec, nil
}

func (s *PostgresStore) ListPendingReviews(ctx context.Context) ([]domain.ReviewQueueItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT document_id, failed_rules, current_json, status
		FROM review_queue
		WHERE status = 'PENDING'
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]domain.ReviewQueueItem, 0)
	for rows.Next() {
		var item domain.ReviewQueueItem
		var failedRules []string
		if err := rows.Scan(&item.DocumentID, pq.Array(&failedRules), &item.CurrentJSON, &item.Status); err != nil {
			return nil, err
		}
		item.FailedRules = failedRules
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *PostgresStore) CountDocuments(ctx context.Context) (int64, error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents`)
	var count int64
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("count documents: %w", err)
	}
	return count, nil
}
