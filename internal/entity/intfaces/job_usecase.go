package intfaces

import (
	"context"
	"encoding/json"
	"time"

	batchv1 "k8s.io/api/batch/v1"
)

// CreateJobResult represents the result of creating a job (ACTSML standard response)
type CreateJobResult struct {
	JobID        string    `json:"job_id"`
	Status       string    `json:"status"`
	K8sJobName   string    `json:"k8s_job_name"`
	SubmittedAt  time.Time `json:"submitted_at"`
	Namespace    string    `json:"namespace"`
	JobUID       string    `json:"job_uid"`
	Submitted    bool      `json:"submitted"`
	ProjectID    string    `json:"project_id,omitempty"`
	ExperimentID string    `json:"experiment_id,omitempty"`
}

// ResourceUsage represents CPU and memory usage for a pod
type ResourceUsage struct {
	CPUUsageCores    float64 `json:"cpu_usage_cores"`    // CPU usage in cores
	MemoryUsageBytes int64   `json:"memory_usage_bytes"`  // Memory usage in bytes
	MemoryUsageMB    float64 `json:"memory_usage_mb"`    // Memory usage in MB (for convenience)
	Timestamp        string  `json:"timestamp"`          // When the metrics were collected
}

// JobStatusWithResults represents job status with metrics data when completed
type JobStatusWithResults struct {
	JobID         string                 `json:"job_id"`
	Status        string                 `json:"status"`
	K8sConditions interface{}            `json:"k8s_conditions"`
	Metrics       map[string]interface{} `json:"metrics,omitempty"`      // Actual metrics data (when completed)
	MetricsPath   string                 `json:"metrics_path,omitempty"` // Path to metrics.json
	ModelPath     string                 `json:"model_path,omitempty"`   // Path to model.pkl
	ResourceUsage *ResourceUsage         `json:"resource_usage,omitempty"` // Resource usage from Prometheus (when running or completed)
}

type IntJobUsecase interface {
	CreateJob(ctx context.Context, payload json.RawMessage) (*CreateJobResult, error)
	GetJobStatus(ctx context.Context, name string) (*batchv1.Job, error)
	GetJobStatusWithResults(ctx context.Context, name string) (*JobStatusWithResults, error)
	DeleteJob(ctx context.Context, name string) error
}
