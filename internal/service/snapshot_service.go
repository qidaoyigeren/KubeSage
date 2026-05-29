package service

import (
	"context"

	"kubesage/internal/config"
	"kubesage/internal/diagnostic"
	"kubesage/internal/k8s"

	"go.uber.org/zap"
)

type SnapshotService struct {
	cfg      *config.Config
	log      *zap.Logger
	pods     *k8s.PodCollector
	events   *k8s.EventCollector
	logs     *k8s.LogCollector
	nodes    *k8s.NodeCollector
	topology *k8s.TopologyCollector
}

func NewSnapshotService(cfg *config.Config, client *k8s.Client, log *zap.Logger) *SnapshotService {
	return &SnapshotService{
		cfg:      cfg,
		log:      log,
		pods:     k8s.NewPodCollector(client),
		events:   k8s.NewEventCollector(client),
		logs:     k8s.NewLogCollector(client),
		nodes:    k8s.NewNodeCollector(client),
		topology: k8s.NewTopologyCollector(client),
	}
}

func (s *SnapshotService) Collect(ctx context.Context, req PodDiagnosisRequest) (*diagnostic.DiagnosticContext, error) {
	pod, err := s.pods.GetPod(ctx, req.Namespace, req.PodName)
	if err != nil {
		return nil, err
	}
	result := &diagnostic.DiagnosticContext{
		Namespace: req.Namespace,
		PodName:   req.PodName,
		Pod:       pod,
	}

	if req.IncludeEvents {
		events, err := s.events.ListPodEvents(ctx, pod)
		if err != nil {
			s.log.Warn("collect events failed", zap.Error(err))
		} else {
			result.Events = events
		}
	}
	if req.IncludeLogs {
		logs, err := s.logs.CollectPodLogs(ctx, pod, req.ContainerName)
		if err != nil {
			s.log.Warn("collect logs failed", zap.Error(err))
		} else {
			result.Logs = logs
		}
	}
	topology, err := s.topology.Collect(ctx, pod)
	if err != nil {
		s.log.Warn("collect topology failed", zap.Error(err))
	} else {
		result.Topology = topology
	}
	nodes, err := s.nodes.ListNodeSnapshots(ctx)
	if err != nil {
		s.log.Warn("collect node snapshots failed", zap.Error(err))
	} else {
		result.NodeSnapshots = nodes
	}

	return result, nil
}
