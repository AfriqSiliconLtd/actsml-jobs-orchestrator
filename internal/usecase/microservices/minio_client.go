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
)

type MinIOClient struct {
	client *minio.Client
}

func NewMinIOClient() (*MinIOClient, error) {
	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		endpoint = "https://api.staging.minio.actsml.com"
	}
	
	// Remove protocol prefix
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")
	
	accessKey := os.Getenv("MINIO_ACCESS_KEY")
	if accessKey == "" {
		accessKey = "actsMl"
	}
	
	secretKey := os.Getenv("MINIO_SECRET_KEY")
	if secretKey == "" {
		secretKey = "6m35xip2UX50SpKh"
	}
	
	useSSL := os.Getenv("MINIO_SECURE") != "false"
	if os.Getenv("MINIO_ENDPOINT") != "" {
		// Check if endpoint has https:// prefix
		originalEndpoint := os.Getenv("MINIO_ENDPOINT")
		useSSL = strings.HasPrefix(originalEndpoint, "https://")
	}
	
	client, err := minio.New(endpoint, &minio.Options{
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

