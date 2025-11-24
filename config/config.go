package config

import (
	"fmt"
	"github.com/ilyakaznacheev/cleanenv"
)

type (
	// Config -.
	Config struct {
		App  `yaml:"app"`
		HTTP `yaml:"http"`
		Log  `yaml:"logger"`
		Job  `yaml:"job"`
	}

	// App -.
	App struct {
		Name    string `env-required:"true" yaml:"name"    env:"APP_NAME"`
		Version string `env-required:"true" yaml:"version" env:"APP_VERSION"`
	}

	// HTTP -.
	HTTP struct {
		Port string `env-required:"true" yaml:"port" env:"HTTP_PORT"`
		Mode string `env-required:"true" yaml:"mode" env:"MODE"`
	}

	// Log -.
	Log struct {
		Level string `env-required:"true" yaml:"log_level"   env:"LOG_LEVEL"`
	}

	// Job -.
	Job struct {
		TTLSecondsAfterFinished int32  `yaml:"ttl_seconds_after_finished" env:"JOB_TTL_SECONDS"`
		BackoffLimit            int32  `yaml:"backoff_limit" env:"JOB_BACKOFF_LIMIT"`
		Namespace               string `yaml:"namespace" env:"K8S_NAMESPACE"`
		WorkerImage             string `yaml:"worker_image" env:"WORKER_IMAGE"`
		ImagePullSecretName     string `yaml:"image_pull_secret_name" env:"IMAGE_PULL_SECRET_NAME"`
		MinIO                   MinIO  `yaml:"minio"`
		JobConfig               JobConfig `yaml:"job_config"`
	}

	// JobConfig -.
	JobConfig struct {
		JobNamePrefix      string `yaml:"job_name_prefix" env:"JOB_NAME_PREFIX"`
		ConfigMapSuffix    string `yaml:"configmap_suffix" env:"CONFIGMAP_SUFFIX"`
		PayloadFileName    string `yaml:"payload_file_name" env:"PAYLOAD_FILE_NAME"`
		WorkspaceMountPath string `yaml:"workspace_mount_path" env:"WORKSPACE_MOUNT_PATH"`
		VolumeName         string `yaml:"volume_name" env:"VOLUME_NAME"`
		ContainerName      string `yaml:"container_name" env:"CONTAINER_NAME"`
		Labels             JobLabels `yaml:"labels"`
		Artifacts          ArtifactConfig `yaml:"artifacts"`
		Resources          ResourceDefaults `yaml:"resources"`
		Kubernetes         KubernetesConfig `yaml:"kubernetes"`
	}

	// KubernetesConfig -.
	KubernetesConfig struct {
		ImagePullPolicy string `yaml:"image_pull_policy" env:"IMAGE_PULL_POLICY"` // IfNotPresent, Always, Never
		RestartPolicy   string `yaml:"restart_policy" env:"RESTART_POLICY"`       // Never, OnFailure, Always
		VolumeReadOnly  bool   `yaml:"volume_read_only" env:"VOLUME_READ_ONLY"`   // Whether ConfigMap volume is read-only
	}

	// JobLabels -.
	JobLabels struct {
		AppLabel        string `yaml:"app_label" env:"JOB_APP_LABEL"`
		ManagedByLabel  string `yaml:"managed_by_label" env:"JOB_MANAGED_BY_LABEL"`
	}

	// ArtifactConfig -.
	ArtifactConfig struct {
		MetricsFileName string `yaml:"metrics_file_name" env:"METRICS_FILE_NAME"`
		ModelFileName   string `yaml:"model_file_name" env:"MODEL_FILE_NAME"`
		S3PathPrefix    string `yaml:"s3_path_prefix" env:"S3_PATH_PREFIX"`
	}

	// ResourceDefaults -.
	ResourceDefaults struct {
		CPURequest    string `yaml:"cpu_request" env:"DEFAULT_CPU_REQUEST"`
		CPULimit      string `yaml:"cpu_limit" env:"DEFAULT_CPU_LIMIT"`
		MemoryRequest string `yaml:"memory_request" env:"DEFAULT_MEMORY_REQUEST"`
		MemoryLimit   string `yaml:"memory_limit" env:"DEFAULT_MEMORY_LIMIT"`
		CPUMultiplier int    `yaml:"cpu_multiplier" env:"CPU_MULTIPLIER"` // Millicores per CPU (e.g., 250 = 250m per CPU)
	}

	// MinIO -.
	MinIO struct {
		Endpoint  string `yaml:"endpoint" env:"MINIO_ENDPOINT"`
		AccessKey string `yaml:"access_key" env:"MINIO_ACCESS_KEY"`
		SecretKey string `yaml:"secret_key" env:"MINIO_SECRET_KEY"`
		Secure    bool   `yaml:"secure" env:"MINIO_SECURE"`
	}
)

// NewConfig returns app config -.
func NewConfig() (*Config, error) {

	cfg := &Config{}

	err := cleanenv.ReadConfig("./config/config.yml", cfg)

	if err != nil {
		return nil, fmt.Errorf("config error: %w", err)
	}

	err = cleanenv.ReadEnv(cfg)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

type T struct {
	Storage struct {
		File struct {
			Path string `json:"path"`
		} `json:"file"`
	} `json:"storage"`
	Listener struct {
		Tcp struct {
			Address    string `json:"address"`
			TlsDisable bool   `json:"tls_disable"`
		} `json:"tcp"`
	} `json:"listener"`
	Ui              bool   `json:"ui"`
	MaxLeaseTtl     string `json:"max_lease_ttl"`
	DefaultLeaseTtl string `json:"default_lease_ttl"`
	DisableMlock    bool   `json:"disable_mlock"`
}
