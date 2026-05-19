package agent

import (
	"context"
	"time"

	"gmha/internal/domain"
	"gmha/internal/ports"
)

type Service struct {
	repo ports.Repository
}

func NewService(repo ports.Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) Register(ctx context.Context, agent domain.Agent, bootstrapToken string) (domain.Agent, error) {
	if err := s.repo.ValidateBootstrapToken(ctx, agent.HostID, bootstrapToken, time.Now()); err != nil {
		return domain.Agent{}, err
	}
	agent.State = domain.AgentStateOnline
	now := time.Now()
	agent.RegisteredAt = now
	agent.LastSeenAt = now
	return s.repo.UpsertAgent(ctx, agent)
}

func (s *Service) Heartbeat(ctx context.Context, hostID string) error {
	return s.repo.UpdateAgentHeartbeat(ctx, hostID, time.Now())
}
