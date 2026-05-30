package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"kubesage/internal/config"
	"kubesage/internal/model"
	"kubesage/internal/repository"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type queuedDiagnosis struct {
	TaskID    uint                `json:"task_id"`
	Request   PodDiagnosisRequest `json:"request"`
	LockKey   string              `json:"lock_key"`
	LockToken string              `json:"lock_token"`
	Attempts  int                 `json:"attempts"`
}

type redisDiagnosisQueue struct {
	client      redis.UniversalClient
	cfg         config.QueueConfig
	deadLetters *repository.DeadLetterRepository
	log         *zap.Logger
}

func newRedisDiagnosisQueue(client redis.UniversalClient, cfg config.QueueConfig, repo *repository.DeadLetterRepository, log *zap.Logger) *redisDiagnosisQueue {
	if client == nil || !strings.EqualFold(cfg.Type, "redis_stream") {
		return nil
	}
	if cfg.Stream == "" {
		cfg.Stream = "kubesage:diagnosis"
	}
	if cfg.Group == "" {
		cfg.Group = "kubesage-workers"
	}
	if cfg.Consumer == "" {
		cfg.Consumer = "worker-1"
	}
	if cfg.BlockingSeconds <= 0 {
		cfg.BlockingSeconds = 5
	}
	if cfg.MaxRetry <= 0 {
		cfg.MaxRetry = 3
	}
	return &redisDiagnosisQueue{client: client, cfg: cfg, deadLetters: repo, log: log}
}

func (q *redisDiagnosisQueue) ensureGroup(ctx context.Context) error {
	if q == nil {
		return nil
	}
	err := q.client.XGroupCreateMkStream(ctx, q.cfg.Stream, q.cfg.Group, "0").Err()
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "busygroup") {
		return err
	}
	return nil
}

func (q *redisDiagnosisQueue) Enqueue(ctx context.Context, payload queuedDiagnosis) error {
	if q == nil {
		return fmt.Errorf("diagnosis queue is not configured")
	}
	bytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return q.client.XAdd(ctx, &redis.XAddArgs{
		Stream: q.cfg.Stream,
		Values: map[string]interface{}{"payload": string(bytes), "task_id": payload.TaskID},
	}).Err()
}

func (q *redisDiagnosisQueue) Run(ctx context.Context, svc *DiagnosisService) error {
	if q == nil {
		return nil
	}
	if err := q.ensureGroup(ctx); err != nil {
		return err
	}
	claimInterval := 30 // claim stale messages every N loop iterations
	loopCount := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Periodically claim stale messages from crashed consumers.
		loopCount++
		if loopCount%claimInterval == 0 {
			q.claimStaleMessages(ctx, svc)
		}
		streams, err := q.client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    q.cfg.Group,
			Consumer: q.cfg.Consumer,
			Streams:  []string{q.cfg.Stream, ">"},
			Count:    1,
			Block:    time.Duration(q.cfg.BlockingSeconds) * time.Second,
		}).Result()
		if err != nil {
			if err == redis.Nil || ctx.Err() != nil {
				continue
			}
			if q.log != nil {
				q.log.Warn("read diagnosis queue failed", zap.Error(err))
			}
			continue
		}
		for _, stream := range streams {
			for _, message := range stream.Messages {
				q.handleMessage(ctx, svc, message)
			}
		}
	}
}

// claimStaleMessages uses XAUTOCLAIM to pick up messages that were claimed but
// never acknowledged (e.g. worker crashed mid-processing).
func (q *redisDiagnosisQueue) claimStaleMessages(ctx context.Context, svc *DiagnosisService) {
	minIdle := 2 * time.Minute
	var start string
	for {
		msgs, newStart, err := q.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream:   q.cfg.Stream,
			Group:    q.cfg.Group,
			Consumer: q.cfg.Consumer,
			MinIdle:  minIdle,
			Start:    start,
			Count:    10,
		}).Result()
		if err != nil {
			if err != redis.Nil && q.log != nil {
				q.log.Warn("xautoclaim failed", zap.Error(err))
			}
			return
		}
		for _, msg := range msgs {
			q.handleMessage(ctx, svc, msg)
		}
		if len(msgs) == 0 || newStart == "0-0" {
			return
		}
		start = newStart
	}
}

func (q *redisDiagnosisQueue) handleMessage(ctx context.Context, svc *DiagnosisService, message redis.XMessage) {
	payload, raw, err := parseQueuedDiagnosis(message)
	if err != nil {
		q.deadLetter(ctx, message.ID, 0, raw, err, 0)
		_ = q.client.XAck(ctx, q.cfg.Stream, q.cfg.Group, message.ID).Err()
		return
	}
	svc.workerWG.Add(1)
	defer svc.workerWG.Done()
	svc.runDiagnosis(context.Background(), payload.TaskID, payload.Request, payload.LockKey, payload.LockToken)
	failed, failure := q.taskFailed(ctx, svc, payload.TaskID)
	if failed {
		if payload.Attempts < q.cfg.MaxRetry {
			payload.Attempts++
			q.scheduleRetry(payload)
		} else {
			q.deadLetter(ctx, message.ID, payload.TaskID, raw, fmt.Errorf("%s", failure), payload.Attempts)
		}
	}
	_ = q.client.XAck(ctx, q.cfg.Stream, q.cfg.Group, message.ID).Err()
	// Trim stream to prevent unbounded growth.
	if q.cfg.MaxStreamLen > 0 {
		_ = q.client.XTrimMaxLen(ctx, q.cfg.Stream, q.cfg.MaxStreamLen).Err()
	}
}

func (q *redisDiagnosisQueue) taskFailed(ctx context.Context, svc *DiagnosisService, taskID uint) (bool, string) {
	if svc == nil || svc.taskRepo == nil {
		return false, ""
	}
	task, err := svc.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return true, err.Error()
	}
	if task.Status != model.TaskStatusFailed {
		return false, ""
	}
	task.Status = model.TaskStatusRunning
	task.FinishedAt = nil
	if err := svc.taskRepo.Update(ctx, task); err != nil && q.log != nil {
		q.log.Warn("mark retry task running failed", zap.Uint("task_id", taskID), zap.Error(err))
	}
	return true, task.RootCauseSummary
}

func (q *redisDiagnosisQueue) scheduleRetry(payload queuedDiagnosis) {
	delay := time.Duration(q.cfg.RetryDelaySeconds) * time.Second
	if delay <= 0 {
		delay = 5 * time.Second
	}
	go func() {
		time.Sleep(delay)
		if err := q.Enqueue(context.Background(), payload); err != nil && q.log != nil {
			q.log.Warn("enqueue diagnosis retry failed", zap.Uint("task_id", payload.TaskID), zap.Error(err))
		}
	}()
}

func parseQueuedDiagnosis(message redis.XMessage) (queuedDiagnosis, string, error) {
	raw, _ := message.Values["payload"].(string)
	if raw == "" {
		return queuedDiagnosis{}, raw, fmt.Errorf("queue message missing payload")
	}
	var payload queuedDiagnosis
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return queuedDiagnosis{}, raw, err
	}
	if payload.TaskID == 0 {
		return queuedDiagnosis{}, raw, fmt.Errorf("queue payload missing task_id")
	}
	return payload, raw, nil
}

func (q *redisDiagnosisQueue) deadLetter(ctx context.Context, messageID string, taskID uint, raw string, err error, attempts int) {
	if q == nil || q.deadLetters == nil {
		return
	}
	_ = q.deadLetters.Create(ctx, &model.DiagnosisQueueDeadLetter{
		Stream:      q.cfg.Stream,
		MessageID:   messageID,
		TaskID:      taskID,
		PayloadJSON: model.JSONText(raw),
		Error:       err.Error(),
		Attempts:    attempts,
		CreatedAt:   time.Now(),
	})
}
