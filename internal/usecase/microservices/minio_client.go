package microservices

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/harmannkibue/actsml-jobs-orchestrator/config"
)

type MinIOClient struct {
	client *minio.Client
}

func NewMinIOClient(cfg *config.Config) (*MinIOClient, error) {
	var endpoint, accessKey, secretKey string
	var useSSL bool

	// Priority: Environment variable > Config > Default
	if envEndpoint := os.Getenv("MINIO_ENDPOINT"); envEndpoint != "" {
		endpoint = envEndpoint
	} else if cfg != nil && cfg.Job.MinIO.Endpoint != "" {
		endpoint = cfg.Job.MinIO.Endpoint
	} else {
		endpoint = "https://api.staging.minio.actsml.com"
	}

	// Remove protocol prefix for MinIO client (it expects hostname only)
	endpointHost := strings.TrimPrefix(endpoint, "https://")
	endpointHost = strings.TrimPrefix(endpointHost, "http://")

	// Determine SSL from endpoint or config
	if strings.HasPrefix(endpoint, "https://") {
		useSSL = true
	} else if strings.HasPrefix(endpoint, "http://") {
		useSSL = false
	} else {
		// Check environment variable
		if envSecure := os.Getenv("MINIO_SECURE"); envSecure != "" {
			useSSL = envSecure != "false"
		} else if cfg != nil {
			useSSL = cfg.Job.MinIO.Secure
		} else {
			useSSL = true // Default to secure
		}
	}

	// Access key priority: Environment > Config > Default
	if envKey := os.Getenv("MINIO_ACCESS_KEY"); envKey != "" {
		accessKey = envKey
	} else if cfg != nil && cfg.Job.MinIO.AccessKey != "" {
		accessKey = cfg.Job.MinIO.AccessKey
	} else {
		accessKey = "actsMl"
	}

	// Secret key priority: Environment > Config > Default
	if envSecret := os.Getenv("MINIO_SECRET_KEY"); envSecret != "" {
		secretKey = envSecret
	} else if cfg != nil && cfg.Job.MinIO.SecretKey != "" {
		secretKey = cfg.Job.MinIO.SecretKey
	} else {
		secretKey = "6m35xip2UX50SpKh"
	}

	client, err := minio.New(endpointHost, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create MinIO client: %w", err)
	}

	return &MinIOClient{
		client: client,
	}, nil
}

// GetMetrics fetches the metrics.json file from MinIO and returns it as a map
func (m *MinIOClient) GetMetrics(ctx context.Context, bucket, path string) (map[string]interface{}, error) {
	objectPath := fmt.Sprintf("%smetrics.json", path)
	
	obj, err := m.client.GetObject(ctx, bucket, objectPath, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics object from MinIO: %w", err)
	}
	defer obj.Close()
	
	data, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to read metrics data: %w", err)
	}
	
	var metrics map[string]interface{}
	if err := json.Unmarshal(data, &metrics); err != nil {
		return nil, fmt.Errorf("failed to parse metrics JSON: %w", err)
	}
	
	return metrics, nil
}

