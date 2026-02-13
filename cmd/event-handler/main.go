package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"temporal-llm-orchestrator/internal/config"
	"temporal-llm-orchestrator/internal/events"
	appTemporal "temporal-llm-orchestrator/internal/temporal"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	minioClient, err := minio.New(cfg.MinioEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.MinioAccessKey, cfg.MinioSecretKey, ""),
		Secure: cfg.MinioUseSSL,
	})
	if err != nil {
		log.Fatalf("connect minio: %v", err)
	}

	temporalClient, err := client.Dial(client.Options{
		HostPort:  cfg.TemporalAddress,
		Namespace: cfg.TemporalNamespace,
	})
	if err != nil {
		log.Fatalf("connect temporal: %v", err)
	}
	defer temporalClient.Close()

	source := events.NewMinioUploadEventSource(minioClient, cfg.MinioBucket, "", "")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("event-handler listening for object-created events on bucket=%s", cfg.MinioBucket)
	err = source.Run(ctx, func(parent context.Context, event events.UploadEvent) error {
		workflowID := fmt.Sprintf("%s-%s", cfg.WorkflowIDPrefix, event.DocumentID)
		execCtx, cancel := context.WithTimeout(parent, 15*time.Second)
		defer cancel()

		_, startErr := temporalClient.ExecuteWorkflow(execCtx, client.StartWorkflowOptions{
			ID:        workflowID,
			TaskQueue: cfg.TemporalTaskQueue,
		}, appTemporal.DocumentIntakeWorkflowName, appTemporal.WorkflowInput{
			DocumentID: event.DocumentID,
			Filename:   event.Filename,
			ObjectKey:  event.ObjectKey,
		})
		if startErr != nil {
			var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
			if errors.As(startErr, &alreadyStarted) {
				log.Printf("workflow already started for object=%s workflow_id=%s", event.ObjectKey, workflowID)
				return nil
			}
			return fmt.Errorf("start workflow for object %s: %w", event.ObjectKey, startErr)
		}

		log.Printf("started workflow workflow_id=%s object=%s", workflowID, event.ObjectKey)
		return nil
	})
	if err != nil {
		log.Fatalf("event-handler stopped with error: %v", err)
	}
}
