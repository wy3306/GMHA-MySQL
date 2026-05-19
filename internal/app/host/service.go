package host

import (
	"context"

	"gmha/internal/domain"
	"gmha/internal/ports"
)

type Service struct {
	repo ports.Repository
}

func NewService(repo ports.Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) List(ctx context.Context) ([]domain.Host, error) {
	return s.repo.ListHosts(ctx)
}
