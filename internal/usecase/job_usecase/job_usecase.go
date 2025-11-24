package job_usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/harmannkibue/actsml-jobs-orchestrator/config"
	"github.com/harmannkibue/actsml-jobs-orchestrator/internal/entity"
	"github.com/harmannkibue/actsml-jobs-orchestrator/internal/entity/intfaces"
	"github.com/harmannkibue/actsml-jobs-orchestrator/internal/usecase/microservices"
	"github.com/harmannkibue/actsml-jobs-orchestrator/pkg/logger"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// JobUseCase represents the job usecase with its dependencies
type JobUseCase struct {
	cfg    *config.Config
	logger logger.Interface
	k8s    intfaces.KubernetesClient
	minio  *microservices.MinIOClient
}

// NewJobUseCase creates a new instance of JobUseCase
func NewJobUseCase(cfg *config.Config, l logger.Interface, k8s intfaces.KubernetesClient, minio *microservices.MinIOClient) *JobUseCase {
	return &JobUseCase{
		cfg:    cfg,
		logger: l,
		k8s:    k8s,
		minio:  minio,
	}
}

// CreateJob creates a new Kubernetes job from the provided payload
func (uc *JobUseCase) CreateJob(ctx context.Context, payload json.RawMessage) (*intfaces.CreateJobResult, error) {
	// Validate payload against ACTSML v1 specification
	validatedPayload, err := ValidatePayload(payload)
	if err != nil {
		uc.logger.Error(fmt.Errorf("payload validation failed: %w", err), "job_usecase - CreateJob")
		return nil, entity.CreateError(entity.ErrBadRequest.Error(), fmt.Sprintf("payload validation failed: %v", err))
	}

	uc.logger.Info(fmt.Sprintf("Validated payload for project_id=%s, experiment_id=%s, algorithm=%s",
		validatedPayload.ProjectID, validatedPayload.ExperimentID, validatedPayload.Problem.Algorithm))

	// Generate unique job ID and name
	jobID := uuid.New().String()
	jobName := fmt.Sprintf("actsml-job-%s", jobID)

	// Get namespace from config or use default
	namespace := uc.getNamespace()

	// Marshal payload to JSON string
	// This ensures proper JSON encoding (handles special characters, etc.)
	payloadBytes, err := json.Marshal(validatedPayload)
	if err != nil {
		uc.logger.Error(fmt.Errorf("failed to marshal validated payload: %w", err), "job_usecase - CreateJob")
		return nil, entity.CreateError(entity.ErrInternalServerError.Error(), "failed to process payload")
	}
	payloadStr := string(payloadBytes)

	// Create ConfigMap with payload (worker expects PAYLOAD_FILE pointing to a file)
	configMapName := fmt.Sprintf("%s-payload", jobName)
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: namespace,
			Labels: map[string]string{
				"managed-by": "actsml-orchestrator",
				"job-name":   jobName,
			},
		},
		Data: map[string]string{
			"payload.json": payloadStr,
		},
	}

	// Create ConfigMap before creating the job
	_, err = uc.k8s.CreateConfigMap(ctx, namespace, configMap)
	if err != nil {
		uc.logger.Error(fmt.Errorf("failed to create ConfigMap for payload: %w", err), "job_usecase - CreateJob")
		return nil, entity.CreateError(entity.ErrInternalServerError.Error(), fmt.Sprintf("failed to create payload ConfigMap: %v", err))
	}

	// Build environment variables for the worker
	envMap := map[string]string{
		"PAYLOAD_FILE": "/workspace/payload.json", // Worker expects file path
		// MinIO configuration (can be overridden via config in the future)
		"MINIO_ENDPOINT":  uc.getMinIOEndpoint(),
		"MINIO_ACCESS_KEY": uc.getMinIOAccessKey(),
		"MINIO_SECRET_KEY": uc.getMinIOSecretKey(),
		"MINIO_SECURE":     uc.getMinIOSecure(),
	}

	// Get worker image from config
	workerImage := uc.getWorkerImage()

	// Get image pull secret name from config
	imagePullSecretName := uc.getImagePullSecretName()

	// Get job configuration from config
	backoffLimit := uc.getBackoffLimit()
	ttlSeconds := uc.getTTLSeconds()

	// Build Kubernetes Job manifest with compute resources from payload
	// Pass ConfigMap name for volume mounting
	job := BuildK8sJob(jobName, workerImage, envMap, backoffLimit, namespace, &validatedPayload.Compute, imagePullSecretName, configMapName, ttlSeconds)
	
	// Add project and experiment labels for better tracking
	if job.Labels == nil {
		job.Labels = make(map[string]string)
	}
	job.Labels["project-id"] = validatedPayload.ProjectID
	job.Labels["experiment-id"] = validatedPayload.ExperimentID
	job.Spec.Template.Labels["project-id"] = validatedPayload.ProjectID
	job.Spec.Template.Labels["experiment-id"] = validatedPayload.ExperimentID

	// Submit job to Kubernetes
	createdJob, err := uc.k8s.CreateJob(ctx, namespace, job)
	if err != nil {
		uc.logger.Error(fmt.Errorf("failed to create job in Kubernetes: %w", err), "job_usecase - CreateJob")
		return nil, entity.CreateError(entity.ErrInternalServerError.Error(), fmt.Sprintf("failed to submit job: %v", err))
	}

	uc.logger.Info(fmt.Sprintf("Job created successfully: %s in namespace %s", jobName, namespace))

	return &intfaces.CreateJobResult{
		JobID:        jobID,
		Status:       "submitted",
		K8sJobName:   jobName,
		SubmittedAt:  time.Now(),
		Namespace:    namespace,
		JobUID:       string(createdJob.UID),
		Submitted:    true,
		ProjectID:    validatedPayload.ProjectID,
		ExperimentID: validatedPayload.ExperimentID,
	}, nil
}

// GetJobStatus retrieves the status of a Kubernetes job -.
func (uc *JobUseCase) GetJobStatus(ctx context.Context, name string) (*batchv1.Job, error) {
	if name == "" {
		return nil, entity.CreateError(entity.ErrBadRequest.Error(), "job name cannot be empty")
	}

	namespace := uc.getNamespace()
	job, err := uc.k8s.GetJob(ctx, namespace, name)
	if err != nil {
		uc.logger.Error(fmt.Errorf("failed to get job status: %w", err), "job_usecase - GetJobStatus")
		return nil, entity.CreateError(entity.ErrNotFound.Error(), fmt.Sprintf("job not found: %s", name))
	}

	return job, nil
}

// GetJobStatusWithResults retrieves job status and metrics if completed
func (uc *JobUseCase) GetJobStatusWithResults(ctx context.Context, name string) (*intfaces.JobStatusWithResults, error) {
	if name == "" {
		return nil, entity.CreateError(entity.ErrBadRequest.Error(), "job name cannot be empty")
	}

	namespace := uc.getNamespace()
	job, err := uc.k8s.GetJob(ctx, namespace, name)
	if err != nil {
		uc.logger.Error(fmt.Errorf("failed to get job status: %w", err), "job_usecase - GetJobStatusWithResults")
		return nil, entity.CreateError(entity.ErrNotFound.Error(), fmt.Sprintf("job not found: %s", name))
	}

	// Determine status from job conditions
	status := "unknown"
	if len(job.Status.Conditions) > 0 {
		latestCondition := job.Status.Conditions[len(job.Status.Conditions)-1]
		if latestCondition.Type == "Complete" && latestCondition.Status == "True" {
			status = "completed"
		} else if latestCondition.Type == "Failed" && latestCondition.Status == "True" {
			status = "failed"
		} else {
			status = "running"
		}
	} else if job.Status.Active > 0 {
		status = "running"
	} else if job.Status.Succeeded > 0 {
		status = "completed"
	} else if job.Status.Failed > 0 {
		status = "failed"
	}

	// Extract job ID from job name
	jobID := name
	if strings.HasPrefix(name, "actsml-job-") {
		jobID = strings.TrimPrefix(name, "actsml-job-")
	}

	result := &intfaces.JobStatusWithResults{
		JobID:         jobID,
		Status:        status,
		K8sConditions: job.Status.Conditions,
	}

	// If completed, fetch metrics from MinIO
	if status == "completed" && uc.minio != nil {
		// Get output path from ConfigMap
		configMapName := fmt.Sprintf("%s-payload", name)
		configMap, err := uc.k8s.GetConfigMap(ctx, namespace, configMapName)
		if err == nil {
			var payload ACTSMLTrainingPayload
			payloadStr := configMap.Data["payload.json"]
			if err := json.Unmarshal([]byte(payloadStr), &payload); err == nil {
				// Fetch actual metrics JSON from MinIO
				metrics, err := uc.minio.GetMetrics(ctx, payload.Output.Bucket, payload.Output.Path)
				if err != nil {
					uc.logger.Warn(fmt.Sprintf("Failed to fetch metrics for job %s: %v", name, err))
					// Continue without metrics - don't fail the request
				} else {
					// Return actual metrics data
					result.Metrics = metrics
					
					// Return paths for artifacts
					result.MetricsPath = fmt.Sprintf("s3://%s/%smetrics.json", payload.Output.Bucket, payload.Output.Path)
					result.ModelPath = fmt.Sprintf("s3://%s/%smodel.pkl", payload.Output.Bucket, payload.Output.Path)
				}
			} else {
				uc.logger.Warn(fmt.Sprintf("Failed to parse payload from ConfigMap for job %s: %v", name, err))
			}
		} else {
			uc.logger.Warn(fmt.Sprintf("Failed to get ConfigMap for job %s: %v", name, err))
		}
	}

	return result, nil
}

// DeleteJob deletes a Kubernetes job
func (uc *JobUseCase) DeleteJob(ctx context.Context, name string) error {
	if name == "" {
		return entity.CreateError(entity.ErrBadRequest.Error(), "job name cannot be empty")
	}

	namespace := uc.getNamespace()
	err := uc.k8s.DeleteJob(ctx, namespace, name)
	if err != nil {
		uc.logger.Error(fmt.Errorf("failed to delete job: %w", err), "job_usecase - DeleteJob")
		return entity.CreateError(entity.ErrInternalServerError.Error(), fmt.Sprintf("failed to delete job: %v", err))
	}

	uc.logger.Info(fmt.Sprintf("Job deleted successfully: %s in namespace %s", name, namespace))
	return nil
}

// getNamespace returns the namespace to use for jobs
// Priority: Environment variable > Config > Default
func (uc *JobUseCase) getNamespace() string {
	// Environment variable takes highest priority
	if ns := os.Getenv("K8S_NAMESPACE"); ns != "" {
		return ns
	}
	// Use config value if available
	if uc.cfg != nil && uc.cfg.Job.Namespace != "" {
		return uc.cfg.Job.Namespace
	}
	// Default fallback
	return "actsml"
}

// getWorkerImage returns the worker image to use
// Priority: Environment variable > Config > Default
func (uc *JobUseCase) getWorkerImage() string {
	// Environment variable takes highest priority
	if image := os.Getenv("WORKER_IMAGE"); image != "" {
		return image
	}
	// Use config value if available
	if uc.cfg != nil && uc.cfg.Job.WorkerImage != "" {
		return uc.cfg.Job.WorkerImage
	}
	// Default fallback
	return "ghcr.io/afriqsiliconltd/actsml-worker-image:staging"
}

// getMinIOEndpoint returns the MinIO endpoint (with protocol)
// Priority: Environment variable > Config > Default
func (uc *JobUseCase) getMinIOEndpoint() string {
	// Environment variable takes highest priority
	if endpoint := os.Getenv("MINIO_ENDPOINT"); endpoint != "" {
		// Ensure protocol is included
		if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
			// If no protocol, add https:// by default (VPS uses HTTPS)
			endpoint = "https://" + endpoint
		}
		return endpoint
	}
	// Use config value if available
	if uc.cfg != nil && uc.cfg.Job.MinIO.Endpoint != "" {
		endpoint := uc.cfg.Job.MinIO.Endpoint
		// Ensure protocol is included
		if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
			endpoint = "https://" + endpoint
		}
		return endpoint
	}
	// Default fallback
	return "https://api.staging.minio.actsml.com"
}

// getMinIOAccessKey returns the MinIO access key
// Priority: Environment variable > Config > Default
func (uc *JobUseCase) getMinIOAccessKey() string {
	// Environment variable takes highest priority
	if key := os.Getenv("MINIO_ACCESS_KEY"); key != "" {
		return key
	}
	// Use config value if available
	if uc.cfg != nil && uc.cfg.Job.MinIO.AccessKey != "" {
		return uc.cfg.Job.MinIO.AccessKey
	}
	// Default fallback
	return "actsMl"
}

// getMinIOSecretKey returns the MinIO secret key
// Priority: Environment variable > Config > Default
func (uc *JobUseCase) getMinIOSecretKey() string {
	// Environment variable takes highest priority
	if key := os.Getenv("MINIO_SECRET_KEY"); key != "" {
		return key
	}
	// Use config value if available
	if uc.cfg != nil && uc.cfg.Job.MinIO.SecretKey != "" {
		return uc.cfg.Job.MinIO.SecretKey
	}
	// Default fallback
	return "6m35xip2UX50SpKh"
}

// getMinIOSecure returns whether MinIO uses secure connection
// Priority: Environment variable > Config > Default
func (uc *JobUseCase) getMinIOSecure() string {
	// Environment variable takes highest priority
	if secure := os.Getenv("MINIO_SECURE"); secure != "" {
		return secure
	}
	// Use config value if available
	if uc.cfg != nil {
		if uc.cfg.Job.MinIO.Secure {
			return "true"
		}
		return "false"
	}
	// Default fallback
	return "true"
}

// getImagePullSecretName returns the name of the image pull secret to use
// Priority: Environment variable > Config > Default
func (uc *JobUseCase) getImagePullSecretName() string {
	// Environment variable takes highest priority
	if secretName := os.Getenv("IMAGE_PULL_SECRET_NAME"); secretName != "" {
		return secretName
	}
	// Use config value if available
	if uc.cfg != nil && uc.cfg.Job.ImagePullSecretName != "" {
		return uc.cfg.Job.ImagePullSecretName
	}
	// Default fallback
	return "ghcr-pull-secret"
}

// getBackoffLimit returns the backoff limit for job retries
// Priority: Environment variable > Config > Default
func (uc *JobUseCase) getBackoffLimit() int32 {
	// Environment variable takes highest priority
	if limit := os.Getenv("JOB_BACKOFF_LIMIT"); limit != "" {
		var parsedLimit int32
		if _, err := fmt.Sscanf(limit, "%d", &parsedLimit); err == nil {
			return parsedLimit
		}
	}
	// Use config value if available
	if uc.cfg != nil && uc.cfg.Job.BackoffLimit > 0 {
		return uc.cfg.Job.BackoffLimit
	}
	// Default fallback
	return 3
}

// getTTLSeconds returns the TTL in seconds for completed jobs
// Priority: Environment variable > Config > Default
func (uc *JobUseCase) getTTLSeconds() int32 {
	// Environment variable takes highest priority
	if ttl := os.Getenv("JOB_TTL_SECONDS"); ttl != "" {
		var parsedTTL int32
		if _, err := fmt.Sscanf(ttl, "%d", &parsedTTL); err == nil {
			return parsedTTL
		}
	}
	// Use config value if available
	if uc.cfg != nil && uc.cfg.Job.TTLSecondsAfterFinished > 0 {
		return uc.cfg.Job.TTLSecondsAfterFinished
	}
	// Default fallback (1 hour)
	return 3600
}
