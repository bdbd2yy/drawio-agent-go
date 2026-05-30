package armory

import (
	"context"

	"ai-agent-scaffold-go/internal/domain/agent/model"
)

type Service struct {
	handler Handler
}

func NewService(handler Handler) *Service {
	return &Service{handler: handler}
}

func (s *Service) AcceptArmoryAgents(ctx context.Context, tables map[string]model.AiAgentConfigTable) error {
	for _, table := range tables {
		_, err := s.handler.Apply(ctx, model.ArmoryCommand{Table: table}, NewDynamicContext())
		if err != nil {
			return err
		}
	}
	return nil
}
