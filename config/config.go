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
