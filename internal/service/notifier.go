package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"kubesage/internal/config"
	"kubesage/internal/diagnostic"
	"kubesage/internal/model"
)

type Notifier struct {
	cfg  config.NotificationConfig
	http *http.Client
}

func NewNotifier(cfg config.NotificationConfig) *Notifier {
	return &Notifier{cfg: cfg, http: &http.Client{Timeout: 10 * time.Second}}
}

func (n *Notifier) Configured() bool {
	return n != nil && (n.cfg.SlackWebhookURL != "" || n.cfg.FeishuWebhookURL != "" || n.cfg.DingTalkWebhookURL != "")
}

func (n *Notifier) NotifyDiagnosisComplete(ctx context.Context, task *model.DiagnosisTask, report *diagnostic.Report) {
	if n == nil || !n.Configured() || task == nil {
		return
	}
	text := fmt.Sprintf("KubeSage diagnosis #%d %s/%s status=%s fault=%s confidence=%.2f", task.ID, task.Namespace, task.PodName, task.Status, task.FaultType, task.ConfidenceScore)
	if report != nil && strings.TrimSpace(report.RootCauseSummary) != "" {
		text += "\n" + report.RootCauseSummary
	}
	if n.cfg.SlackWebhookURL != "" {
		_ = n.postJSON(ctx, n.cfg.SlackWebhookURL, map[string]string{"text": text})
	}
	if n.cfg.FeishuWebhookURL != "" {
		_ = n.postJSON(ctx, n.cfg.FeishuWebhookURL, map[string]interface{}{"msg_type": "text", "content": map[string]string{"text": text}})
	}
	if n.cfg.DingTalkWebhookURL != "" {
		_ = n.postJSON(ctx, n.cfg.DingTalkWebhookURL, map[string]interface{}{"msgtype": "text", "text": map[string]string{"content": text}})
	}
}

func (n *Notifier) postJSON(ctx context.Context, url string, payload interface{}) error {
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("notification webhook failed: %s", resp.Status)
	}
	return nil
}
