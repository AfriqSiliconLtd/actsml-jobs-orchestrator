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
	cfg       *config.Config
	logger    logger.Interface
	k8s       intfaces.KubernetesClient
	minio     *microservices.MinIOClient
	prometheus *microservices.PrometheusClient
}

// NewJobUseCase creates a new instance of JobUseCase
func NewJobUseCase(cfg *config.Config, l logger.Interface, k8s intfaces.KubernetesClient, minio *microservices.MinIOClient, prometheus *microservices.PrometheusClient) *JobUseCase {
	return &JobUseCase{
		cfg:       cfg,
		logger:    l,
		k8s:       k8s,
		minio:     minio,
		prometheus: prometheus,
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
	jobNamePrefix := uc.getJobNamePrefix()
	jobName := fmt.Sprintf("%s%s", jobNamePrefix, jobID)

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
	configMapSuffix := uc.getConfigMapSuffix()
	payloadFileName := uc.getPayloadFileName()
	managedByLabel := uc.getManagedByLabel()
	configMapName := fmt.Sprintf("%s%s", jobName, configMapSuffix)
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: namespace,
			Labels: map[string]string{
				"managed-by": managedByLabel,
				"job-name":   jobName,
			},
		},
		Data: map[string]string{
			payloadFileName: payloadStr,
		},
	}

	// Create ConfigMap before creating the job
	_, err = uc.k8s.CreateConfigMap(ctx, namespace, configMap)
	if err != nil {
		uc.logger.Error(fmt.Errorf("failed to create ConfigMap for payload: %w", err), "job_usecase - CreateJob")
		return nil, entity.CreateError(entity.ErrInternalServerError.Error(), fmt.Sprintf("failed to create payload ConfigMap: %v", err))
	}

	// Build environment variables for the worker
	workspaceMountPath := uc.getWorkspaceMountPath()
	// payloadFileName already declared above, reuse it
	payloadFilePath := fmt.Sprintf("%s/%s", workspaceMountPath, payloadFileName)
	envMap := map[string]string{
		"PAYLOAD_FILE": payloadFilePath, // Worker expects file path
		// MinIO configuration
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

	// Build job builder config from orchestrator config
	jobBuilderConfig := uc.getJobBuilderConfig()

	// Build Kubernetes Job manifest with compute resources from payload
	// Pass ConfigMap name for volume mounting
	job := BuildK8sJob(jobName, workerImage, envMap, backoffLimit, namespace, &validatedPayload.Compute, imagePullSecretName, configMapName, ttlSeconds, jobBuilderConfig)
	
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

	// Normalize job name - if it's just an ID, prepend the job name prefix
	jobNamePrefix := uc.getJobNamePrefix()
	jobName := name
	if !strings.HasPrefix(name, jobNamePrefix) {
		// If it doesn't start with the prefix, assume it's just an ID and prepend the prefix
		jobName = fmt.Sprintf("%s%s", jobNamePrefix, name)
	}

	namespace := uc.getNamespace()
	job, err := uc.k8s.GetJob(ctx, namespace, jobName)
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
	jobID := jobName
	if strings.HasPrefix(jobName, jobNamePrefix) {
		jobID = strings.TrimPrefix(jobName, jobNamePrefix)
	}

	result := &intfaces.JobStatusWithResults{
		JobID:         jobID,
		Status:        status,
		K8sConditions: job.Status.Conditions,
	}

	// Fetch Prometheus resource usage for running or completed jobs
	if (status == "running" || status == "completed") && uc.prometheus != nil && uc.prometheus.IsEnabled() {
		// Create a context with timeout for Prometheus query to prevent hanging
		promCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		
		resourceUsage, err := uc.prometheus.GetPodResourceUsage(promCtx, namespace, jobName)
		if err != nil {
			uc.logger.Warn(fmt.Sprintf("Failed to fetch resource usage for job %s: %v", jobName, err))
			// Continue without resource usage - don't fail the request
		} else {
			result.ResourceUsage = &intfaces.ResourceUsage{
				CPUUsageCores:    resourceUsage.CPUUsageCores,
				MemoryUsageBytes: resourceUsage.MemoryUsageBytes,
				MemoryUsageMB:    resourceUsage.MemoryUsageMB,
				Timestamp:        resourceUsage.Timestamp,
			}
		}
	}

	// If completed, fetch metrics from MinIO (metrics.json content)
	if status == "completed" && uc.minio != nil {
		// Get output path from ConfigMap
		configMapSuffix := uc.getConfigMapSuffix()
		payloadFileName := uc.getPayloadFileName()
		configMapName := fmt.Sprintf("%s%s", jobName, configMapSuffix)
		configMap, err := uc.k8s.GetConfigMap(ctx, namespace, configMapName)
		if err == nil {
			var payload ACTSMLTrainingPayload
			payloadStr := configMap.Data[payloadFileName]
			if err := json.Unmarshal([]byte(payloadStr), &payload); err == nil {
				// Fetch actual metrics JSON from MinIO
				metricsFileName := uc.getMetricsFileName()
				modelFileName := uc.getModelFileName()
				s3PathPrefix := uc.getS3PathPrefix()
				metrics, err := uc.minio.GetMetrics(ctx, payload.Output.Bucket, payload.Output.Path, metricsFileName)
				if err != nil {
					uc.logger.Warn(fmt.Sprintf("Failed to fetch metrics for job %s: %v", jobName, err))
					// Continue without metrics - don't fail the request
				} else {
					// Return actual metrics data
					result.Metrics = metrics
					
					// Return paths for artifacts
					result.MetricsPath = fmt.Sprintf("%s%s/%s%s", s3PathPrefix, payload.Output.Bucket, payload.Output.Path, metricsFileName)
					result.ModelPath = fmt.Sprintf("%s%s/%s%s", s3PathPrefix, payload.Output.Bucket, payload.Output.Path, modelFileName)
				}
			} else {
				uc.logger.Warn(fmt.Sprintf("Failed to parse payload from ConfigMap for job %s: %v", jobName, err))
			}
		} else {
			uc.logger.Warn(fmt.Sprintf("Failed to get ConfigMap for job %s: %v", jobName, err))
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

// getJobNamePrefix returns the job name prefix
// Priority: Environment variable > Config > Default
func (uc *JobUseCase) getJobNamePrefix() string {
	if prefix := os.Getenv("JOB_NAME_PREFIX"); prefix != "" {
		return prefix
	}
	if uc.cfg != nil && uc.cfg.Job.JobConfig.JobNamePrefix != "" {
		return uc.cfg.Job.JobConfig.JobNamePrefix
	}
	return "actsml-job-"
}

// getConfigMapSuffix returns the ConfigMap name suffix
// Priority: Environment variable > Config > Default
func (uc *JobUseCase) getConfigMapSuffix() string {
	if suffix := os.Getenv("CONFIGMAP_SUFFIX"); suffix != "" {
		return suffix
	}
	if uc.cfg != nil && uc.cfg.Job.JobConfig.ConfigMapSuffix != "" {
		return uc.cfg.Job.JobConfig.ConfigMapSuffix
	}
	return "-payload"
}

// getPayloadFileName returns the payload file name
// Priority: Environment variable > Config > Default
func (uc *JobUseCase) getPayloadFileName() string {
	if fileName := os.Getenv("PAYLOAD_FILE_NAME"); fileName != "" {
		return fileName
	}
	if uc.cfg != nil && uc.cfg.Job.JobConfig.PayloadFileName != "" {
		return uc.cfg.Job.JobConfig.PayloadFileName
	}
	return "payload.json"
}

// getWorkspaceMountPath returns the workspace mount path
// Priority: Environment variable > Config > Default
func (uc *JobUseCase) getWorkspaceMountPath() string {
	if path := os.Getenv("WORKSPACE_MOUNT_PATH"); path != "" {
		return path
	}
	if uc.cfg != nil && uc.cfg.Job.JobConfig.WorkspaceMountPath != "" {
		return uc.cfg.Job.JobConfig.WorkspaceMountPath
	}
	return "/workspace"
}

// getManagedByLabel returns the managed-by label value
// Priority: Environment variable > Config > Default
func (uc *JobUseCase) getManagedByLabel() string {
	if label := os.Getenv("JOB_MANAGED_BY_LABEL"); label != "" {
		return label
	}
	if uc.cfg != nil && uc.cfg.Job.JobConfig.Labels.ManagedByLabel != "" {
		return uc.cfg.Job.JobConfig.Labels.ManagedByLabel
	}
	return "actsml-orchestrator"
}

// getMetricsFileName returns the metrics file name
// Priority: Environment variable > Config > Default
func (uc *JobUseCase) getMetricsFileName() string {
	if fileName := os.Getenv("METRICS_FILE_NAME"); fileName != "" {
		return fileName
	}
	if uc.cfg != nil && uc.cfg.Job.JobConfig.Artifacts.MetricsFileName != "" {
		return uc.cfg.Job.JobConfig.Artifacts.MetricsFileName
	}
	return "metrics.json"
}

// getModelFileName returns the model file name
// Priority: Environment variable > Config > Default
func (uc *JobUseCase) getModelFileName() string {
	if fileName := os.Getenv("MODEL_FILE_NAME"); fileName != "" {
		return fileName
	}
	if uc.cfg != nil && uc.cfg.Job.JobConfig.Artifacts.ModelFileName != "" {
		return uc.cfg.Job.JobConfig.Artifacts.ModelFileName
	}
	return "model.pkl"
}

// getS3PathPrefix returns the S3 path prefix
// Priority: Environment variable > Config > Default
func (uc *JobUseCase) getS3PathPrefix() string {
	if prefix := os.Getenv("S3_PATH_PREFIX"); prefix != "" {
		return prefix
	}
	if uc.cfg != nil && uc.cfg.Job.JobConfig.Artifacts.S3PathPrefix != "" {
		return uc.cfg.Job.JobConfig.Artifacts.S3PathPrefix
	}
	return "s3://"
}

// getJobBuilderConfig returns the job builder configuration
func (uc *JobUseCase) getJobBuilderConfig() *JobBuilderConfig {
	config := &JobBuilderConfig{}

	// App label
	if label := os.Getenv("JOB_APP_LABEL"); label != "" {
		config.AppLabel = label
	} else if uc.cfg != nil && uc.cfg.Job.JobConfig.Labels.AppLabel != "" {
		config.AppLabel = uc.cfg.Job.JobConfig.Labels.AppLabel
	} else {
		config.AppLabel = "actsml-worker"
	}

	// Managed by label
	if label := os.Getenv("JOB_MANAGED_BY_LABEL"); label != "" {
		config.ManagedByLabel = label
	} else if uc.cfg != nil && uc.cfg.Job.JobConfig.Labels.ManagedByLabel != "" {
		config.ManagedByLabel = uc.cfg.Job.JobConfig.Labels.ManagedByLabel
	} else {
		config.ManagedByLabel = "actsml-orchestrator"
	}

	// Container name
	if name := os.Getenv("CONTAINER_NAME"); name != "" {
		config.ContainerName = name
	} else if uc.cfg != nil && uc.cfg.Job.JobConfig.ContainerName != "" {
		config.ContainerName = uc.cfg.Job.JobConfig.ContainerName
	} else {
		config.ContainerName = "worker"
	}

	// Volume name
	if name := os.Getenv("VOLUME_NAME"); name != "" {
		config.VolumeName = name
	} else if uc.cfg != nil && uc.cfg.Job.JobConfig.VolumeName != "" {
		config.VolumeName = uc.cfg.Job.JobConfig.VolumeName
	} else {
		config.VolumeName = "payload"
	}

	// Workspace mount path
	if path := os.Getenv("WORKSPACE_MOUNT_PATH"); path != "" {
		config.WorkspaceMountPath = path
	} else if uc.cfg != nil && uc.cfg.Job.JobConfig.WorkspaceMountPath != "" {
		config.WorkspaceMountPath = uc.cfg.Job.JobConfig.WorkspaceMountPath
	} else {
		config.WorkspaceMountPath = "/workspace"
	}

	// Resource defaults
	if cpuReq := os.Getenv("DEFAULT_CPU_REQUEST"); cpuReq != "" {
		config.CPURequest = cpuReq
	} else if uc.cfg != nil && uc.cfg.Job.JobConfig.Resources.CPURequest != "" {
		config.CPURequest = uc.cfg.Job.JobConfig.Resources.CPURequest
	} else {
		config.CPURequest = "250m"
	}

	if cpuLimit := os.Getenv("DEFAULT_CPU_LIMIT"); cpuLimit != "" {
		config.CPULimit = cpuLimit
	} else if uc.cfg != nil && uc.cfg.Job.JobConfig.Resources.CPULimit != "" {
		config.CPULimit = uc.cfg.Job.JobConfig.Resources.CPULimit
	} else {
		config.CPULimit = "1"
	}

	if memReq := os.Getenv("DEFAULT_MEMORY_REQUEST"); memReq != "" {
		config.MemoryRequest = memReq
	} else if uc.cfg != nil && uc.cfg.Job.JobConfig.Resources.MemoryRequest != "" {
		config.MemoryRequest = uc.cfg.Job.JobConfig.Resources.MemoryRequest
	} else {
		config.MemoryRequest = "512Mi"
	}

	if memLimit := os.Getenv("DEFAULT_MEMORY_LIMIT"); memLimit != "" {
		config.MemoryLimit = memLimit
	} else if uc.cfg != nil && uc.cfg.Job.JobConfig.Resources.MemoryLimit != "" {
		config.MemoryLimit = uc.cfg.Job.JobConfig.Resources.MemoryLimit
	} else {
		config.MemoryLimit = "1Gi"
	}

	// CPU multiplier
	if multiplier := os.Getenv("CPU_MULTIPLIER"); multiplier != "" {
		if _, err := fmt.Sscanf(multiplier, "%d", &config.CPUMultiplier); err != nil {
			config.CPUMultiplier = 250
		}
	} else if uc.cfg != nil && uc.cfg.Job.JobConfig.Resources.CPUMultiplier > 0 {
		config.CPUMultiplier = uc.cfg.Job.JobConfig.Resources.CPUMultiplier
	} else {
		config.CPUMultiplier = 250
	}

	// Kubernetes settings
	if policy := os.Getenv("IMAGE_PULL_POLICY"); policy != "" {
		config.ImagePullPolicy = policy
	} else if uc.cfg != nil && uc.cfg.Job.JobConfig.Kubernetes.ImagePullPolicy != "" {
		config.ImagePullPolicy = uc.cfg.Job.JobConfig.Kubernetes.ImagePullPolicy
	} else {
		config.ImagePullPolicy = "IfNotPresent"
	}

	if policy := os.Getenv("RESTART_POLICY"); policy != "" {
		config.RestartPolicy = policy
	} else if uc.cfg != nil && uc.cfg.Job.JobConfig.Kubernetes.RestartPolicy != "" {
		config.RestartPolicy = uc.cfg.Job.JobConfig.Kubernetes.RestartPolicy
	} else {
		config.RestartPolicy = "Never"
	}

	if readOnly := os.Getenv("VOLUME_READ_ONLY"); readOnly != "" {
		config.VolumeReadOnly = readOnly == "true"
	} else if uc.cfg != nil {
		config.VolumeReadOnly = uc.cfg.Job.JobConfig.Kubernetes.VolumeReadOnly
	} else {
		config.VolumeReadOnly = true
	}

	return config
}
