package job_route

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/harmannkibue/actsml-jobs-orchestrator/internal/entity"
	"github.com/harmannkibue/actsml-jobs-orchestrator/internal/entity/intfaces"
	"github.com/harmannkibue/actsml-jobs-orchestrator/pkg/logger"
)

type JobRoute struct {
	u intfaces.IntJobUsecase
	l logger.Interface
}

func NewJobRoute(handler *gin.RouterGroup, t intfaces.IntJobUsecase, l logger.Interface) {
	r := &JobRoute{t, l}

	h := handler.Group("/jobs")
	{
		h.POST("/create", r.createJob)
		h.GET("/:id/status", r.getJobStatus)
	}
}

type createJobResponse struct {
	JobID       string `json:"job_id"`
	Status      string `json:"status"`
	K8sJobName  string `json:"k8s_job_name"`
	SubmittedAt string `json:"submitted_at"`
	ProjectID   string `json:"project_id,omitempty"`
	ExperimentID string `json:"experiment_id,omitempty"`
}

type getJobStatusResponse struct {
	JobID       string      `json:"job_id"`
	Status      string      `json:"status"`
	K8sConditions interface{} `json:"k8s_conditions"`
}

// @Summary     Create a Job
// @Description Create a Kubernetes training job
// @ID          Create a Job
// @Tags        Job
// @Accept      json
// @Produce     json
// @Param       request body object true "Job payload (raw JSON)"
// @Success     202 {object} createJobResponse
// @Failure     400 {object} entity.HTTPError
// @Failure     500 {object} entity.HTTPError
// @Router      /jobs/create [post]
func (route *JobRoute) createJob(ctx *gin.Context) {
	var raw json.RawMessage

	if err := ctx.ShouldBindJSON(&raw); err != nil {
		route.l.Error(err, "http - v1 - create job route")
		ctx.JSON(entity.GetStatusCode(err), entity.ErrorCodeResponse(err))
		return
	}

	res, err := route.u.CreateJob(ctx.Request.Context(), raw)
	if err != nil {
		route.l.Error(err, "http - v1 - create job usecase")
		ctx.JSON(entity.GetStatusCode(err), entity.ErrorCodeResponse(err))
		return
	}

	ctx.JSON(http.StatusAccepted, createJobResponse{
		JobID:        res.JobID,
		Status:       res.Status,
		K8sJobName:   res.K8sJobName,
		SubmittedAt:  res.SubmittedAt.Format("2006-01-02T15:04:05Z"),
		ProjectID:    res.ProjectID,
		ExperimentID: res.ExperimentID,
	})
}

// @Summary     Get Job Status
// @Description Get status of a Kubernetes training job with metrics if completed
// @ID          Get Job Status
// @Tags        Job
// @Produce     json
// @Param       id path string true "Job ID (actsml-job-{uuid})"
// @Success     200 {object} intfaces.JobStatusWithResults
// @Failure     404 {object} entity.HTTPError
// @Failure     500 {object} entity.HTTPError
// @Router      /jobs/{id}/status [get]
func (route *JobRoute) getJobStatus(ctx *gin.Context) {
	jobID := ctx.Param("id")

	// Pass jobID to usecase - it will handle job name normalization internally
	// The usecase will check if it's already a full job name or just an ID
	result, err := route.u.GetJobStatusWithResults(ctx.Request.Context(), jobID)
	if err != nil {
		route.l.Error(err, "http - v1 - get job status")
		ctx.JSON(entity.GetStatusCode(err), entity.ErrorCodeResponse(err))
		return
	}

	ctx.JSON(http.StatusOK, result)
}
