package job_usecase

import (
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// JobBuilderConfig holds configuration values for building Kubernetes jobs
type JobBuilderConfig struct {
	AppLabel           string
	ManagedByLabel     string
	ContainerName      string
	VolumeName         string
	WorkspaceMountPath string
	CPURequest         string
	CPULimit           string
	MemoryRequest      string
	MemoryLimit        string
	CPUMultiplier      int
	ImagePullPolicy    string
	RestartPolicy      string
	VolumeReadOnly     bool
}

// BuildK8sJob builds a Kubernetes Job manifest with optional compute resources, image pull secrets, and ConfigMap volume
func BuildK8sJob(jobName string, image string, envMap map[string]string, backoffLimit int32, namespace string, compute *ComputeConfig, imagePullSecretName string, configMapName string, ttlSeconds int32, jobConfig *JobBuilderConfig) *batchv1.Job {
	envVars := []corev1.EnvVar{}
	for k, v := range envMap {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}

	// Get label values from config
	appLabel := "actsml-worker"
	managedByLabel := "actsml-orchestrator"
	if jobConfig != nil {
		if jobConfig.AppLabel != "" {
			appLabel = jobConfig.AppLabel
		}
		if jobConfig.ManagedByLabel != "" {
			managedByLabel = jobConfig.ManagedByLabel
		}
	}

	labels := map[string]string{
		"app":        appLabel,
		"job-name":   jobName,
		"managed-by": managedByLabel,
	}

	// Default resource values from config -.
	cpuRequest := "250m"
	cpuLimit := "1"
	memoryRequest := "512Mi"
	memoryLimit := "1Gi"
	cpuMultiplier := 250
	if jobConfig != nil {
		if jobConfig.CPURequest != "" {
			cpuRequest = jobConfig.CPURequest
		}
		if jobConfig.CPULimit != "" {
			cpuLimit = jobConfig.CPULimit
		}
		if jobConfig.MemoryRequest != "" {
			memoryRequest = jobConfig.MemoryRequest
		}
		if jobConfig.MemoryLimit != "" {
			memoryLimit = jobConfig.MemoryLimit
		}
		if jobConfig.CPUMultiplier > 0 {
			cpuMultiplier = jobConfig.CPUMultiplier
		}
	}

	// Override with compute config if provided -.
	if compute != nil {
		if compute.NumCPUs > 0 {
			cpuRequest = fmt.Sprintf("%dm", compute.NumCPUs*cpuMultiplier)
			cpuLimit = fmt.Sprintf("%d", compute.NumCPUs)
		}
		if compute.Memory != "" {
			memoryRequest = compute.Memory
			// Set limit to request if not explicitly set
			memoryLimit = compute.Memory
		}
	}

	// Configure volume mount for payload file if ConfigMap is provided -.
	var volumeMounts []corev1.VolumeMount
	var volumes []corev1.Volume
	volumeName := "payload"
	workspaceMountPath := "/workspace"
	volumeReadOnly := true
	if jobConfig != nil {
		if jobConfig.VolumeName != "" {
			volumeName = jobConfig.VolumeName
		}
		if jobConfig.WorkspaceMountPath != "" {
			workspaceMountPath = jobConfig.WorkspaceMountPath
		}
		volumeReadOnly = jobConfig.VolumeReadOnly
	}
	if configMapName != "" {
		volumeMounts = []corev1.VolumeMount{
			{
				Name:      volumeName,
				MountPath: workspaceMountPath,
				ReadOnly:  volumeReadOnly,
			},
		}
		volumes = []corev1.Volume{
			{
				Name: volumeName,
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: configMapName,
						},
					},
				},
			},
		}
	}

	// Get container name from config
	containerName := "worker"
	imagePullPolicy := corev1.PullIfNotPresent
	if jobConfig != nil {
		if jobConfig.ContainerName != "" {
			containerName = jobConfig.ContainerName
		}
		// Parse image pull policy from config
		switch jobConfig.ImagePullPolicy {
		case "Always":
			imagePullPolicy = corev1.PullAlways
		case "Never":
			imagePullPolicy = corev1.PullNever
		case "IfNotPresent", "":
			imagePullPolicy = corev1.PullIfNotPresent
		default:
			imagePullPolicy = corev1.PullIfNotPresent
		}
	}

	container := corev1.Container{
		Name:            containerName,
		Image:           image,
		ImagePullPolicy: imagePullPolicy,
		Env:             envVars,
		VolumeMounts:    volumeMounts,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpuRequest),
				corev1.ResourceMemory: resource.MustParse(memoryRequest),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpuLimit),
				corev1.ResourceMemory: resource.MustParse(memoryLimit),
			},
		},
	}

	// Configure image pull secrets if provided
	var imagePullSecrets []corev1.LocalObjectReference
	if imagePullSecretName != "" {
		imagePullSecrets = []corev1.LocalObjectReference{
			{Name: imagePullSecretName},
		}
	}

	// Parse restart policy from config
	restartPolicy := corev1.RestartPolicyNever
	if jobConfig != nil {
		switch jobConfig.RestartPolicy {
		case "OnFailure":
			restartPolicy = corev1.RestartPolicyOnFailure
		case "Always":
			restartPolicy = corev1.RestartPolicyAlways
		case "Never", "":
			restartPolicy = corev1.RestartPolicyNever
		default:
			restartPolicy = corev1.RestartPolicyNever
		}
	}

	podTemplate := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: labels,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:    restartPolicy,
			Containers:       []corev1.Container{container},
			ImagePullSecrets: imagePullSecrets,
			Volumes:          volumes,
		},
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			Template:                podTemplate,
			BackoffLimit:            &backoffLimit,
			TTLSecondsAfterFinished: &ttlSeconds,
		},
	}

	return job
}
