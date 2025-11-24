# ACTSML Orchestrator API Documentation

This document describes how to interact with the ACTSML Jobs Orchestrator API for creating and monitoring machine learning training jobs.

## Base URL

```
http://localhost:8080/api/v1
```

For production, replace `localhost:8080` with your orchestrator's hostname and port.

## API Endpoints

### 1. Create Job

**Endpoint:** `POST /jobs/create`

**Description:** Creates a new Kubernetes training job from a JSON payload.

**Request Headers:**
```
Content-Type: application/json
Accept: application/json
```

**Request Body:** JSON payload following the ACTSML Training Payload Specification (v1)

**Response:** `202 Accepted`

**Response Body:**
```json
{
  "job_id": "uuid-string",
  "status": "submitted",
  "k8s_job_name": "actsml-job-{uuid}",
  "submitted_at": "2025-11-24T12:00:00Z",
  "project_id": "your-project-id",
  "experiment_id": "your-experiment-id"
}
```

**Example Request:**
```bash
curl -X POST http://localhost:8080/api/v1/jobs/create \
  -H "Content-Type: application/json" \
  -d '{
    "project_id": "swagger_test_proj",
    "experiment_id": "swagger_test_exp_001",
    "dataset": {
      "bucket": "datasets",
      "path": "adult.csv",
      "version": "v1",
      "target_column": "income",
      "feature_columns": [
        "age",
        "workclass",
        "fnlwgt",
        "education",
        "education-num",
        "marital-status",
        "occupation",
        "relationship",
        "race",
        "sex",
        "capital-gain",
        "capital-loss",
        "hours-per-week",
        "native-country"
      ]
    },
    "problem": {
      "type": "classification",
      "algorithm": "random_forest"
    },
    "hyperparameters": {
      "n_estimators": 100,
      "max_depth": 10,
      "random_state": 42
    },
    "preprocessing": {
      "missing_values": "drop",
      "scaling": "none",
      "encode_categorical": "label",
      "train_test_split": {
        "test_size": 0.2,
        "shuffle": true,
        "random_state": 42
      }
    },
    "compute": {
      "resource_class": "cpu_small",
      "num_cpus": 2,
      "memory": "2Gi"
    },
    "output": {
      "bucket": "experiments",
      "path": "swagger_test_proj/swagger_test_exp_001/"
    },
    "metadata": {
      "description": "Swagger UI test with adult.csv dataset"
    }
  }'
```

---

### 2. Get Job Status

**Endpoint:** `GET /jobs/{id}/status`

**Description:** Retrieves the current status of a training job.

**Path Parameters:**
- `id`: Job ID (UUID) or full job name (`actsml-job-{uuid}`)

**Response:** `200 OK`

**Response Body:**
```json
{
  "job_id": "uuid-string",
  "status": "running|completed|failed",
  "k8s_conditions": [
    {
      "type": "Complete",
      "status": "True",
      "lastProbeTime": "2025-11-24T12:05:00Z",
      "lastTransitionTime": "2025-11-24T12:05:00Z",
      "reason": "CompletionsReached",
      "message": "Reached expected number of succeeded pods"
    }
  ]
}
```

**Status Values:**
- `running`: Job is currently executing
- `completed`: Job finished successfully
- `failed`: Job encountered an error

**Example Request:**
```bash
curl -X GET http://localhost:8080/api/v1/jobs/f3d6f4d9-4d63-4820-9654-97e9d413ff78/status
```

Or with full job name:
```bash
curl -X GET http://localhost:8080/api/v1/jobs/actsml-job-f3d6f4d9-4d63-4820-9654-97e9d413ff78/status
```

---

## Payload Specification

### Required Fields

All payloads must include the following top-level fields:

- `project_id` (string, required): Unique identifier for the project
- `experiment_id` (string, required): Unique identifier for the experiment
- `dataset` (object, required): Dataset configuration
- `problem` (object, required): Problem definition
- `output` (object, required): Output configuration

### Dataset Configuration

```json
{
  "dataset": {
    "bucket": "datasets",              // MinIO bucket name (required)
    "path": "adult.csv",               // Path to dataset file (required)
    "version": "v1",                   // Dataset version (optional)
    "target_column": "income",         // Target/label column (required)
    "feature_columns": [                // List of feature columns (required for headerless CSVs)
      "age",
      "workclass",
      // ... more columns
    ]
  }
}
```

**Important:** The `feature_columns` array is **required** when your CSV file doesn't have headers. The worker uses this to apply column names and detect missing headers.

### Problem Definition

```json
{
  "problem": {
    "type": "classification|binary_classification|regression",  // Problem type (required)
    "algorithm": "random_forest"                                // Algorithm name (required)
  }
}
```

**Supported Problem Types:**
- `classification`: Multi-class classification
- `binary_classification`: Binary classification
- `regression`: Regression problems

**Supported Algorithms:**
- `random_forest`
- `logistic_regression`
- `xgboost`
- `decision_tree`
- `svm`
- (and others supported by the worker)

### Preprocessing Configuration

```json
{
  "preprocessing": {
    "missing_values": "drop|mean|median|none",     // How to handle missing values
    "scaling": "none|standard|minmax|robust",      // Feature scaling method
    "encode_categorical": "label|onehot|none",    // Categorical encoding method
    "train_test_split": {
      "test_size": 0.2,                            // Test set proportion (0.0-1.0)
      "shuffle": true,                             // Whether to shuffle data
      "random_state": 42                           // Random seed for reproducibility
    }
  }
}
```

**⚠️ Important Preprocessing Notes:**

1. **Scaling and Categorical Encoding:**
   - If using `scaling: "standard"` or other scaling methods, ensure categorical columns are encoded **before** scaling
   - The worker should handle this automatically, but if you encounter errors, use `scaling: "none"` as a workaround
   - **Recommended configuration for headerless CSVs:**
     ```json
     {
       "missing_values": "drop",
       "scaling": "none",
       "encode_categorical": "label"
     }
     ```

2. **Missing Values:**
   - `drop`: Remove rows with missing values
   - `mean`: Fill with column mean (numeric columns only)
   - `median`: Fill with column median (numeric columns only)
   - `none`: No handling

3. **Categorical Encoding:**
   - `label`: Label encoding (converts categories to integers)
   - `onehot`: One-hot encoding (creates binary columns)
   - `none`: No encoding

### Hyperparameters

```json
{
  "hyperparameters": {
    "n_estimators": 100,
    "max_depth": 10,
    "random_state": 42
    // ... algorithm-specific parameters
  }
}
```

Hyperparameters are algorithm-specific and passed directly to the ML algorithm. Common parameters:
- `n_estimators`: Number of trees (for tree-based algorithms)
- `max_depth`: Maximum tree depth
- `random_state`: Random seed for reproducibility

### Compute Configuration

```json
{
  "compute": {
    "resource_class": "cpu_small",  // Resource class identifier
    "num_cpus": 2,                  // Number of CPUs
    "memory": "2Gi"                 // Memory allocation
  }
}
```

**Memory Format:** Use Kubernetes resource format (e.g., `"2Gi"`, `"512Mi"`)

### Output Configuration

```json
{
  "output": {
    "bucket": "experiments",                    // MinIO bucket for outputs (required)
    "path": "project_id/experiment_id/"        // Output path prefix (required)
  }
}
```

The worker will save:
- Model artifact: `{bucket}/{path}model.pkl`
- Metrics: `{bucket}/{path}metrics.json`

### Metadata

```json
{
  "metadata": {
    "description": "Optional experiment description",
    "submitted_by": "user_id",
    // ... any additional metadata
  }
}
```

Metadata is optional and can contain any additional information about the experiment.

---

## Complete Example Payload

```json
{
  "project_id": "customer_churn_proj",
  "experiment_id": "exp_001",
  "dataset": {
    "bucket": "datasets",
    "path": "adult.csv",
    "version": "v1",
    "target_column": "income",
    "feature_columns": [
      "age",
      "workclass",
      "fnlwgt",
      "education",
      "education-num",
      "marital-status",
      "occupation",
      "relationship",
      "race",
      "sex",
      "capital-gain",
      "capital-loss",
      "hours-per-week",
      "native-country"
    ]
  },
  "problem": {
    "type": "classification",
    "algorithm": "random_forest"
  },
  "hyperparameters": {
    "n_estimators": 100,
    "max_depth": 10,
    "random_state": 42
  },
  "preprocessing": {
    "missing_values": "drop",
    "scaling": "none",
    "encode_categorical": "label",
    "train_test_split": {
      "test_size": 0.2,
      "shuffle": true,
      "random_state": 42
    }
  },
  "compute": {
    "resource_class": "cpu_small",
    "num_cpus": 2,
    "memory": "2Gi"
  },
  "output": {
    "bucket": "experiments",
    "path": "customer_churn_proj/exp_001/"
  },
  "metadata": {
    "description": "Customer churn prediction using random forest"
  }
}
```

---

## Error Responses

### 400 Bad Request
```json
{
  "code": 400,
  "message": "payload validation failed: project_id is required"
}
```

Common validation errors:
- Missing required fields
- Invalid problem type
- Invalid preprocessing options

### 404 Not Found
```json
{
  "code": 404,
  "message": "job not found: actsml-job-{uuid}"
}
```

### 500 Internal Server Error
```json
{
  "code": 500,
  "message": "failed to submit job: {error details}"
}
```

---

## Workflow Example

### 1. Create a Job

```bash
# Submit job
RESPONSE=$(curl -X POST http://localhost:8080/api/v1/jobs/create \
  -H "Content-Type: application/json" \
  -d @payload.json)

# Extract job ID
JOB_ID=$(echo $RESPONSE | jq -r '.job_id')
echo "Job ID: $JOB_ID"
```

### 2. Monitor Job Status

```bash
# Check status
curl -X GET http://localhost:8080/api/v1/jobs/$JOB_ID/status | jq .

# Poll until complete
while true; do
  STATUS=$(curl -s http://localhost:8080/api/v1/jobs/$JOB_ID/status | jq -r '.status')
  echo "Status: $STATUS"
  
  if [ "$STATUS" = "completed" ] || [ "$STATUS" = "failed" ]; then
    break
  fi
  
  sleep 5
done
```

### 3. Check Results

Once the job is completed, results are saved to MinIO:
- Model: `s3://experiments/{output_path}/model.pkl`
- Metrics: `s3://experiments/{output_path}/metrics.json`

---

## Important Notes

1. **Feature Columns Requirement:**
   - Always include `feature_columns` array when your CSV doesn't have headers
   - This enables the worker to detect and handle headerless CSVs correctly

2. **Preprocessing Order:**
   - The worker should handle preprocessing order automatically
   - If you encounter scaling errors with categorical columns, use `scaling: "none"`

3. **MinIO Configuration:**
   - The orchestrator automatically configures MinIO connectivity
   - Uses HTTPS endpoint: `https://api.staging.minio.actsml.com`
   - Credentials are injected automatically into worker pods

4. **Job Cleanup:**
   - Completed jobs are automatically cleaned up after 1 hour (TTL)
   - ConfigMaps are cleaned up when jobs are deleted

5. **Namespace:**
   - All jobs are created in the `actsml` namespace by default
   - Can be configured via `K8S_NAMESPACE` environment variable

---

## Swagger UI

The orchestrator includes Swagger UI documentation available at:

```
http://localhost:8080/swagger/index.html
```

You can use Swagger UI to:
- View all available endpoints
- Test API calls interactively
- See request/response schemas

---

## Troubleshooting

### Job Fails Immediately

1. Check payload validation:
   ```bash
   curl -X POST http://localhost:8080/api/v1/jobs/create \
     -H "Content-Type: application/json" \
     -d @payload.json
   ```

2. Verify all required fields are present
3. Check that `feature_columns` is included for headerless CSVs

### Job Runs But Fails During Training

1. Check job logs:
   ```bash
   kubectl logs -n actsml actsml-job-{uuid}-{pod-id}
   ```

2. Verify preprocessing configuration:
   - Use `scaling: "none"` if encountering categorical column errors
   - Ensure `feature_columns` matches your dataset structure

3. Check MinIO connectivity:
   - Verify dataset exists in specified bucket/path
   - Ensure output bucket exists and is writable

### Cannot Connect to API

1. Verify orchestrator is running:
   ```bash
   curl http://localhost:8080/health
   ```

2. Check if port is correct (default: 8080)
3. Verify firewall/network settings

---

## Support

For issues or questions:
- Check job logs in Kubernetes: `kubectl logs -n actsml <pod-name>`
- Review orchestrator logs
- Verify payload matches ACTSML v1 specification

