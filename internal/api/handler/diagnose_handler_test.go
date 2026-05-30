package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"kubesage/internal/model"
	"kubesage/internal/service"

	"github.com/gin-gonic/gin"
)

type mockDiagnosisStarter struct {
	task *model.DiagnosisTask
	err  error
}

func (m *mockDiagnosisStarter) StartPodDiagnosis(ctx context.Context, req service.PodDiagnosisRequest) (*model.DiagnosisTask, error) {
	return m.task, m.err
}

type mockAuditRecorder struct{}

func (m *mockAuditRecorder) RecordAudit(ctx context.Context, actor, action, namespace, resourceKind, resourceName string, taskID *uint, summary string, metadata interface{}) error {
	return nil
}

func TestDiagnosePod_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockTask := &model.DiagnosisTask{ID: 42, Status: model.TaskStatusRunning}
	handler := NewDiagnoseHandler(&mockDiagnosisStarter{task: mockTask}, nil, &mockAuditRecorder{})

	body, _ := json.Marshal(service.PodDiagnosisRequest{Namespace: "default", PodName: "test-pod"})
	w := httptest.NewRecorder()
	c, r := gin.CreateTestContext(w)
	r.POST("/diagnose/pod", handler.DiagnosePod)
	c.Request, _ = http.NewRequest(http.MethodPost, "/diagnose/pod", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, c.Request)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"].(float64) != 0 {
		t.Errorf("code = %v, want 0", resp["code"])
	}
}

func TestDiagnosePod_InvalidRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewDiagnoseHandler(&mockDiagnosisStarter{}, nil, &mockAuditRecorder{})

	body, _ := json.Marshal(map[string]string{"namespace": ""})
	w := httptest.NewRecorder()
	c, r := gin.CreateTestContext(w)
	r.POST("/diagnose/pod", handler.DiagnosePod)
	c.Request, _ = http.NewRequest(http.MethodPost, "/diagnose/pod", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, c.Request)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}
