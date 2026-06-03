package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kubesage/internal/model"

	"github.com/gin-gonic/gin"
)

type fakeTaskReader struct {
	task *model.DiagnosisTask
}

func (f fakeTaskReader) GetTask(ctx context.Context, id uint) (*model.DiagnosisTask, error) {
	_ = ctx
	_ = id
	return f.task, nil
}

func (f fakeTaskReader) ListTasks(ctx context.Context, page, pageSize int) ([]model.DiagnosisTask, int64, error) {
	_ = ctx
	_ = page
	_ = pageSize
	return []model.DiagnosisTask{*f.task}, 1, nil
}

func TestGetTaskIncludesAgentTimeline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	task := &model.DiagnosisTask{
		ID:        1,
		Namespace: "default",
		PodName:   "api-0",
		Status:    model.TaskStatusSuccess,
		Report: &model.DiagnosisReport{
			TaskID:                1,
			Namespace:             "default",
			PodName:               "api-0",
			AgentExecutionSummary: "agent completed",
			AgentTimeline: []model.AgentStep{{
				TaskID:           1,
				StepIndex:        1,
				Stage:            "plan",
				Status:           model.AgentStepStatusSuccess,
				ReasoningSummary: "received diagnosis goal",
				CreatedAt:        time.Now(),
			}},
		},
	}
	router := gin.New()
	router.GET("/tasks/:id", NewTaskHandler(fakeTaskReader{task: task}).GetTask)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tasks/1", nil)
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data model.DiagnosisTask `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Report == nil || len(response.Data.Report.AgentTimeline) != 1 {
		t.Fatalf("expected agent_timeline in response: %s", recorder.Body.String())
	}
}

func TestStreamTaskEmitsInitialAgentTimelineSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	task := &model.DiagnosisTask{
		ID:        1,
		Namespace: "default",
		PodName:   "api-0",
		Status:    model.TaskStatusSuccess,
		Report: &model.DiagnosisReport{
			TaskID:    1,
			Namespace: "default",
			PodName:   "api-0",
			AgentTimeline: []model.AgentStep{{
				ID:               10,
				TaskID:           1,
				StepIndex:        1,
				Stage:            "plan",
				Status:           model.AgentStepStatusSuccess,
				ReasoningSummary: "received diagnosis goal",
				CreatedAt:        time.Now(),
			}},
		},
	}
	router := gin.New()
	router.GET("/tasks/:id/events", NewTaskHandler(fakeTaskReader{task: task}).StreamTask)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tasks/1/events", nil)
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status %d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "event:task") || !strings.Contains(body, "agent_timeline") {
		t.Fatalf("expected task SSE event with timeline, got %q", body)
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "text/event-stream") {
		t.Fatalf("expected text/event-stream content type, got %q", contentType)
	}
}
