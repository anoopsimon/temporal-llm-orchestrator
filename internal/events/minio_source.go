package events

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/minio/minio-go/v7"
)

const objectCreatedEvent = "s3:ObjectCreated:*"

type UploadEvent struct {
	DocumentID string
	Filename   string
	ObjectKey  string
	EventName  string
}

type UploadEventSource interface {
	Run(ctx context.Context, handler func(context.Context, UploadEvent) error) error
}

type MinioUploadEventSource struct {
	client *minio.Client
	bucket string
	prefix string
	suffix string
}

func NewMinioUploadEventSource(client *minio.Client, bucket string, prefix string, suffix string) *MinioUploadEventSource {
	return &MinioUploadEventSource{
		client: client,
		bucket: bucket,
		prefix: prefix,
		suffix: suffix,
	}
}

func (s *MinioUploadEventSource) Run(ctx context.Context, handler func(context.Context, UploadEvent) error) error {
	notificationCh := s.client.ListenBucketNotification(ctx, s.bucket, s.prefix, s.suffix, []string{objectCreatedEvent})
	for {
		select {
		case <-ctx.Done():
			return nil
		case info, ok := <-notificationCh:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("minio notification stream closed")
			}
			if info.Err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("minio notification stream error: %w", info.Err)
			}
			for _, record := range info.Records {
				objectKey, err := decodeObjectKey(record.S3.Object.Key)
				if err != nil {
					continue
				}
				documentID, filename, err := parseObjectKey(objectKey)
				if err != nil {
					continue
				}
				event := UploadEvent{
					DocumentID: documentID,
					Filename:   filename,
					ObjectKey:  objectKey,
					EventName:  record.EventName,
				}
				if err := handler(ctx, event); err != nil {
					return err
				}
			}
		}
	}
}

func decodeObjectKey(encoded string) (string, error) {
	decoded, err := url.QueryUnescape(encoded)
	if err != nil {
		return "", err
	}
	decoded = strings.TrimSpace(decoded)
	if decoded == "" {
		return "", fmt.Errorf("object key is empty")
	}
	return decoded, nil
}

func parseObjectKey(objectKey string) (string, string, error) {
	cleaned := strings.Trim(strings.ReplaceAll(objectKey, "\\", "/"), "/")
	parts := strings.SplitN(cleaned, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("object key %q does not match document_id/filename", objectKey)
	}
	documentID := strings.TrimSpace(parts[0])
	filename := strings.TrimSpace(parts[1])
	if documentID == "" || filename == "" {
		return "", "", fmt.Errorf("object key %q missing document id or filename", objectKey)
	}
	return documentID, filename, nil
}
