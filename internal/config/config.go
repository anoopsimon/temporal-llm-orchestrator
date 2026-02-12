package config

import (
	"fmt"
	"os"
	"strconv"
)

const (
	defaultHTTPPort        = "8080"
	defaultTemporalAddress = "localhost:7233"
	defaultTemporalNS      = "default"
	defaultTaskQueue       = "document-intake-task-queue"
	defaultOpenAIModel     = "gpt-4o-mini"
	defaultOpenAITimeout   = 30
	defaultMinioEndpoint   = "localhost:9000"
	defaultMinioBucket     = "documents"
)

type Config struct {
	HTTPPort           string
	PostgresDSN        string
	TemporalAddress    string
	TemporalNamespace  string
	TemporalTaskQueue  string
	OpenAIAPIKey       string
	OpenAIModel        string
	OpenAITimeoutSec   int
	MinioEndpoint      string
	MinioAccessKey     string
	MinioSecretKey     string
	MinioBucket        string
	MinioUseSSL        bool
	WorkflowIDPrefix   string
	AllowedUploadBytes int64
}

func Load() (Config, error) {
	cfg := Config{
		HTTPPort:           getenv("HTTP_PORT", defaultHTTPPort),
		PostgresDSN:        os.Getenv("POSTGRES_DSN"),
		TemporalAddress:    getenv("TEMPORAL_ADDRESS", defaultTemporalAddress),
		TemporalNamespace:  getenv("TEMPORAL_NAMESPACE", defaultTemporalNS),
		TemporalTaskQueue:  getenv("TEMPORAL_TASK_QUEUE", defaultTaskQueue),
		OpenAIAPIKey:       os.Getenv("OPENAI_API_KEY"),
		OpenAIModel:        getenv("OPENAI_MODEL", defaultOpenAIModel),
		OpenAITimeoutSec:   getenvInt("OPENAI_TIMEOUT_SEC", defaultOpenAITimeout),
		MinioEndpoint:      getenv("MINIO_ENDPOINT", defaultMinioEndpoint),
		MinioAccessKey:     os.Getenv("MINIO_ACCESS_KEY"),
		MinioSecretKey:     os.Getenv("MINIO_SECRET_KEY"),
		MinioBucket:        getenv("MINIO_BUCKET", defaultMinioBucket),
		MinioUseSSL:        getenvBool("MINIO_USE_SSL", false),
		WorkflowIDPrefix:   getenv("WORKFLOW_ID_PREFIX", "doc-intake"),
		AllowedUploadBytes: int64(getenvInt("MAX_UPLOAD_BYTES", 10*1024*1024)),
	}

	if cfg.PostgresDSN == "" {
		return Config{}, fmt.Errorf("POSTGRES_DSN is required")
	}

	return cfg, nil
}

func getenv(key string, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func getenvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}
