package microservices

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/harmannkibue/actsml-jobs-orchestrator/config"
)

type PrometheusClient struct {
	client   *http.Client
	endpoint string
	enabled  bool
}

// ResourceUsage represents CPU and memory usage for a pod
type ResourceUsage struct {
	CPUUsageCores    float64 `json:"cpu_usage_cores"`    // CPU usage in cores
	MemoryUsageBytes int64   `json:"memory_usage_bytes"`  // Memory usage in bytes
	MemoryUsageMB    float64 `json:"memory_usage_mb"`    // Memory usage in MB (for convenience)
	Timestamp        string  `json:"timestamp"`          // When the metrics were collected
}

// PrometheusQueryResponse represents the response from Prometheus API
type PrometheusQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}      `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

func NewPrometheusClient(cfg *config.Config) (*PrometheusClient, error) {
	var endpoint string
	var enabled bool

	// Priority: Environment variable > Config > Default
	if envEndpoint := os.Getenv("PROMETHEUS_ENDPOINT"); envEndpoint != "" {
		endpoint = envEndpoint
	} else if cfg != nil && cfg.Job.Prometheus.Endpoint != "" {
		endpoint = cfg.Job.Prometheus.Endpoint
	} else {
		endpoint = "http://prometheus.monitoring.svc.cluster.local:9090"
	}

	if envEnabled := os.Getenv("PROMETHEUS_ENABLED"); envEnabled != "" {
		enabled = envEnabled != "false"
	} else if cfg != nil {
		enabled = cfg.Job.Prometheus.Enabled
	} else {
		enabled = true // Default to enabled
	}

	// Create HTTP client with timeout that respects context cancellation
	// Use a custom dialer with DNS and connection timeouts to prevent hanging
	// This ensures DNS resolution doesn't hang indefinitely when Prometheus is unreachable
	dialer := &net.Dialer{
		Timeout:   3 * time.Second,   // DNS resolution + connection timeout
		KeepAlive: 30 * time.Second,
	}
	
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   3 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	
	client := &http.Client{
		Timeout:   5 * time.Second, // Overall request timeout
		Transport: transport,
	}

	return &PrometheusClient{
		client:   client,
		endpoint: endpoint,
		enabled:  enabled,
	}, nil
}

// GetPodResourceUsage fetches CPU and memory usage for a pod from Prometheus
func (p *PrometheusClient) GetPodResourceUsage(ctx context.Context, namespace, podName string) (*ResourceUsage, error) {
	if !p.enabled {
		return nil, fmt.Errorf("Prometheus client is disabled")
	}

	// Check if context is already cancelled or timed out
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context cancelled or timed out: %w", ctx.Err())
	default:
	}

	// Get CPU usage (rate over 5 minutes)
	cpuUsage, err := p.queryCPUUsage(ctx, namespace, podName)
	if err != nil {
		return nil, fmt.Errorf("failed to query CPU usage: %w", err)
	}

	// Check context again before second query
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context cancelled or timed out: %w", ctx.Err())
	default:
	}

	// Get memory usage
	memoryUsage, err := p.queryMemoryUsage(ctx, namespace, podName)
	if err != nil {
		return nil, fmt.Errorf("failed to query memory usage: %w", err)
	}

	return &ResourceUsage{
		CPUUsageCores:    cpuUsage,
		MemoryUsageBytes: memoryUsage,
		MemoryUsageMB:    float64(memoryUsage) / (1024 * 1024),
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// queryCPUUsage queries Prometheus for CPU usage of a pod
func (p *PrometheusClient) queryCPUUsage(ctx context.Context, namespace, podName string) (float64, error) {
	// Query: Try to get the latest CPU usage rate
	// For running pods: use rate over 5m window
	// For completed pods: use last_over_time to get the last known rate value
	// We use pod=~ to match the pod name pattern since Kubernetes pod names can have suffixes
	// First try with rate (for running pods), if that fails, try with last_over_time for completed pods
	query := fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{pod=~"%s.*", namespace="%s"}[5m]))`, podName, namespace)
	
	result, err := p.executeQuery(ctx, query)
	if err != nil {
		return 0, err
	}

	// If no results with rate query (pod might be completed), try last_over_time to get historical data
	if len(result.Data.Result) == 0 {
		// Try to get the last known rate value from the last hour
		fallbackQuery := fmt.Sprintf(`sum(last_over_time(rate(container_cpu_usage_seconds_total{pod=~"%s.*", namespace="%s"}[5m])[1h:]))`, podName, namespace)
		fallbackResult, fallbackErr := p.executeQuery(ctx, fallbackQuery)
		if fallbackErr == nil && len(fallbackResult.Data.Result) > 0 {
			result = fallbackResult
		} else {
			return 0, fmt.Errorf("no CPU metrics found for pod %s in namespace %s", podName, namespace)
		}
	}

	// Extract the value (Prometheus returns [timestamp, value])
	valueStr, ok := result.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, fmt.Errorf("invalid CPU metric value format")
	}

	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse CPU value: %w", err)
	}

	return value, nil
}

// queryMemoryUsage queries Prometheus for memory usage of a pod
func (p *PrometheusClient) queryMemoryUsage(ctx context.Context, namespace, podName string) (int64, error) {
	// Query: container_memory_working_set_bytes{pod=~"pod-name.*", namespace="namespace"}
	// For completed pods, use last_over_time to get the last known value
	query := fmt.Sprintf(`sum(container_memory_working_set_bytes{pod=~"%s.*", namespace="%s"})`, podName, namespace)
	
	result, err := p.executeQuery(ctx, query)
	if err != nil {
		return 0, err
	}

	// If no results (pod might be completed), try last_over_time to get historical data
	if len(result.Data.Result) == 0 {
		// Try to get the last known memory value from the last hour
		fallbackQuery := fmt.Sprintf(`sum(last_over_time(container_memory_working_set_bytes{pod=~"%s.*", namespace="%s"}[1h:]))`, podName, namespace)
		fallbackResult, fallbackErr := p.executeQuery(ctx, fallbackQuery)
		if fallbackErr == nil && len(fallbackResult.Data.Result) > 0 {
			result = fallbackResult
		} else {
			return 0, fmt.Errorf("no memory metrics found for pod %s in namespace %s", podName, namespace)
		}
	}

	// Extract the value (Prometheus returns [timestamp, value])
	valueStr, ok := result.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, fmt.Errorf("invalid memory metric value format")
	}

	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse memory value: %w", err)
	}

	return int64(value), nil
}

// executeQuery executes a PromQL query against Prometheus API
func (p *PrometheusClient) executeQuery(ctx context.Context, query string) (*PrometheusQueryResponse, error) {
	// Build the API URL
	apiURL := fmt.Sprintf("%s/api/v1/query", strings.TrimSuffix(p.endpoint, "/"))
	
	// Create request with query parameter
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	q := req.URL.Query()
	q.Add("query", query)
	req.URL.RawQuery = q.Encode()

	// Execute request with context timeout
	resp, err := p.client.Do(req)
	if err != nil {
		// Check if error is due to context timeout or cancellation
		if ctx.Err() == context.DeadlineExceeded || ctx.Err() == context.Canceled {
			return nil, fmt.Errorf("Prometheus query timeout: %w", ctx.Err())
		}
		return nil, fmt.Errorf("failed to execute Prometheus query: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Prometheus API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var result PrometheusQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode Prometheus response: %w", err)
	}

	if result.Status != "success" {
		return nil, fmt.Errorf("Prometheus query failed with status: %s", result.Status)
	}

	return &result, nil
}

// IsEnabled returns whether Prometheus client is enabled
func (p *PrometheusClient) IsEnabled() bool {
	return p.enabled
}

