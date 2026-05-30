package chat

import (
	"fmt"
	"sort"

	"ai-agent-scaffold-go/internal/domain/agent/model"
	"ai-agent-scaffold-go/internal/domain/agent/ports"
	"ai-agent-scaffold-go/pkg/types"
)

type Service struct {
	registry ports.AgentRegistry
	sessions ports.SessionStore
}

func NewService(registry ports.AgentRegistry, sessions ports.SessionStore) *Service {
	return &Service{registry: registry, sessions: sessions}
}

func (s *Service) QueryAgentConfigList() []model.AgentSummary {
	registered := s.registry.List()
	sort.Slice(registered, func(i, j int) bool {
		return registered[i].AgentID < registered[j].AgentID
	})
	agents := make([]model.AgentSummary, 0, len(registered))
	for _, agent := range registered {
		agents = append(agents, model.AgentSummary{
			AgentID:   agent.AgentID,
			AgentName: agent.AgentName,
			AgentDesc: agent.AgentDesc,
		})
	}
	return agents
}

func (s *Service) CreateSession(agentID, userID string) (string, error) {
	if sessionID, ok := s.sessions.Get(userID, agentID); ok {
		return sessionID, nil
	}
	registered, ok := s.registry.Get(agentID)
	if !ok || registered.Runner == nil {
		return "", types.NewAppError(types.CodeAgentNotFound, types.InfoAgentNotFound)
	}
	sessionID, err := registered.Runner.CreateSession(userID)
	if err != nil {
		return "", err
	}
	if err := s.sessions.Set(userID, agentID, sessionID); err != nil {
		return "", err
	}
	return sessionID, nil
}

func (s *Service) HandleMessage(agentID, userID, sessionID, message string) ([]string, error) {
	content := model.ChatContent{Texts: []model.TextPart{{Message: message}}}
	return s.HandleCommand(model.ChatCommand{
		AgentID:   agentID,
		UserID:    userID,
		SessionID: sessionID,
		Message:   message,
		Content:   content,
	})
}

func (s *Service) HandleCommand(command model.ChatCommand) ([]string, error) {
	registered, sessionID, err := s.resolveRunnerSession(command)
	if err != nil {
		return nil, err
	}
	return registered.Runner.Run(command.UserID, sessionID, command.Content)
}

func (s *Service) HandleMessageStream(agentID, userID, sessionID, message string) (<-chan string, <-chan error) {
	return s.HandleCommandStream(model.ChatCommand{
		AgentID:   agentID,
		UserID:    userID,
		SessionID: sessionID,
		Message:   message,
		Content:   model.ChatContent{Texts: []model.TextPart{{Message: message}}},
	})
}

func (s *Service) HandleCommandStream(command model.ChatCommand) (<-chan string, <-chan error) {
	registered, sessionID, err := s.resolveRunnerSession(command)
	if err != nil {
		outputs := make(chan string)
		errs := make(chan error, 1)
		errs <- err
		close(outputs)
		close(errs)
		return outputs, errs
	}
	return registered.Runner.Stream(command.UserID, sessionID, command.Content)
}

func (s *Service) resolveRunnerSession(command model.ChatCommand) (model.RegisteredAgent, string, error) {
	registered, ok := s.registry.Get(command.AgentID)
	if !ok || registered.Runner == nil {
		return model.RegisteredAgent{}, "", types.NewAppError(types.CodeAgentNotFound, types.InfoAgentNotFound)
	}
	sessionID := command.SessionID
	if sessionID == "" {
		var err error
		sessionID, err = s.CreateSession(command.AgentID, command.UserID)
		if err != nil {
			return model.RegisteredAgent{}, "", err
		}
	}
	if len(command.Content.Texts) == 0 && command.Message != "" {
		command.Content.Texts = []model.TextPart{{Message: command.Message}}
	}
	if len(command.Content.Texts) == 0 && len(command.Content.Files) == 0 && len(command.Content.InlineDatas) == 0 {
		return model.RegisteredAgent{}, "", fmt.Errorf("chat content is required")
	}
	return registered, sessionID, nil
}
