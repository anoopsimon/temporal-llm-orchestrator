package main

import (
	"log"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"

	"temporal-llm-orchestrator/internal/config"
	"temporal-llm-orchestrator/internal/openai"
	"temporal-llm-orchestrator/internal/storage"
	appTemporal "temporal-llm-orchestrator/internal/temporal"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	store, err := storage.NewPostgresStore(cfg.PostgresDSN)
	if err != nil {
		log.Fatalf("connect postgres: %v", err)
	}
	defer store.Close()

	blob, err := storage.NewMinioStore(cfg.MinioEndpoint, cfg.MinioAccessKey, cfg.MinioSecretKey, cfg.MinioUseSSL, cfg.MinioBucket)
	if err != nil {
		log.Fatalf("connect minio: %v", err)
	}

	llm := openai.NewHTTPClient(cfg.OpenAIAPIKey, cfg.OpenAIModel)

	temporalClient, err := client.Dial(client.Options{
		HostPort:  cfg.TemporalAddress,
		Namespace: cfg.TemporalNamespace,
	})
	if err != nil {
		log.Fatalf("connect temporal: %v", err)
	}
	defer temporalClient.Close()

	activities := &appTemporal.Activities{
		Store:          store,
		Blob:           blob,
		LLM:            llm,
		OpenAIModel:    cfg.OpenAIModel,
		OpenAITimeout:  time.Duration(cfg.OpenAITimeoutSec) * time.Second,
		OpenAIMaxRetry: 3,
	}

	w := worker.New(temporalClient, cfg.TemporalTaskQueue, worker.Options{})
	w.RegisterWorkflowWithOptions(appTemporal.DocumentIntakeWorkflow, workflow.RegisterOptions{Name: appTemporal.DocumentIntakeWorkflowName})
	w.RegisterActivity(activities.StoreDocumentActivity)
	w.RegisterActivity(activities.DetectDocTypeActivity)
	w.RegisterActivity(activities.ExtractFieldsWithOpenAIActivity)
	w.RegisterActivity(activities.ValidateFieldsActivity)
	w.RegisterActivity(activities.CorrectFieldsWithOpenAIActivity)
	w.RegisterActivity(activities.QueueReviewActivity)
	w.RegisterActivity(activities.ResolveReviewActivity)
	w.RegisterActivity(activities.ApplyReviewerCorrectionActivity)
	w.RegisterActivity(activities.PersistResultActivity)
	w.RegisterActivity(activities.RejectDocumentActivity)

	log.Printf("worker running on task queue %s", cfg.TemporalTaskQueue)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("worker stopped with error: %v", err)
	}
}
