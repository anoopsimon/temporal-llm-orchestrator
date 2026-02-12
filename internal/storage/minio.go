package storage

import (
	"bytes"
	"context"
	"fmt"
	"path"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type MinioStore struct {
	client *minio.Client
	bucket string
}

func NewMinioStore(endpoint, accessKey, secretKey string, useSSL bool, bucket string) (*MinioStore, error) {
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return nil, err
		}
	}

	return &MinioStore{client: client, bucket: bucket}, nil
}

func (m *MinioStore) PutDocument(ctx context.Context, documentID, filename string, content []byte) (string, error) {
	objectKey := path.Join(documentID, filename)
	_, err := m.client.PutObject(ctx, m.bucket, objectKey, bytes.NewReader(content), int64(len(content)), minio.PutObjectOptions{
		ContentType: "application/octet-stream",
	})
	if err != nil {
		return "", err
	}
	return objectKey, nil
}

func (m *MinioStore) GetDocument(ctx context.Context, objectKey string) ([]byte, error) {
	obj, err := m.client.GetObject(ctx, m.bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()

	data := new(bytes.Buffer)
	if _, err := data.ReadFrom(obj); err != nil {
		return nil, fmt.Errorf("read object: %w", err)
	}
	return data.Bytes(), nil
}
