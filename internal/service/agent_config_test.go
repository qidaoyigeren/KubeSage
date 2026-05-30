package service

import (
	"testing"
	"time"

	"kubesage/internal/config"
	"kubesage/internal/repository"
)

func TestAgentRuntimeEnabledSwitch(t *testing.T) {
	svc := &DiagnosisService{
		cfg:       &config.Config{Agent: config.AgentConfig{Enabled: true}},
		agentRepo: &repository.AgentRepository{},
	}
	if !svc.agentRuntimeEnabled() {
		t.Fatalf("agent should be enabled when config is true and repo exists")
	}
	svc.cfg.Agent.Enabled = false
	if svc.agentRuntimeEnabled() {
		t.Fatalf("agent should be disabled by config")
	}
	svc.cfg.Agent.Enabled = true
	svc.agentRepo = nil
	if svc.agentRuntimeEnabled() {
		t.Fatalf("agent should fall back to legacy when repo is missing")
	}
}

func TestAgentRuntimeDefaults(t *testing.T) {
	svc := &DiagnosisService{cfg: &config.Config{}}
	if got := svc.agentMaxSteps(); got != 12 {
		t.Fatalf("unexpected max steps default: %d", got)
	}
	if got := svc.agentToolTimeout(); got != 10*time.Second {
		t.Fatalf("unexpected tool timeout default: %s", got)
	}
}
