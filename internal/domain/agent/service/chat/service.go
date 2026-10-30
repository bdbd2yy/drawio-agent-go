package chat

import (
	"context"
	"fmt"
	"sort"
	"sync/atomic"

	"ai-agent-scaffold-go/internal/domain/agent/model"
	"ai-agent-scaffold-go/internal/domain/agent/ports"
	execution "ai-agent-scaffold-go/internal/domain/agent/runtime"
	"ai-agent-scaffold-go/pkg/types"
)

type Service struct {
	registry ports.AgentRegistry
	sessions ports.SessionStore
	busy     atomic.Bool
	options  execution.Options
}

func NewService(registry ports.AgentRegistry, sessions ports.SessionStore) *Service {
	return &Service{registry: registry, sessions: sessions, options: execution.Defaults()}
}

func (s *Service) WithOptions(o execution.Options) *Service { s.options = o; return s }
func (s *Service) MaxRequestBytes() int64                   { return s.options.MaxRequestBytes }

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

func (s *Service) HandleMessage(ctx context.Context, agentID, userID, sessionID, message string) ([]string, error) {
	content := model.ChatContent{Texts: []model.TextPart{{Message: message}}}
	return s.HandleCommand(ctx, model.ChatCommand{
		AgentID:   agentID,
		UserID:    userID,
		SessionID: sessionID,
		Message:   message,
		Content:   content,
	})
}

func (s *Service) HandleCommand(ctx context.Context, command model.ChatCommand) ([]string, error) {
	if !s.busy.CompareAndSwap(false, true) {
		return nil, execution.ErrBusy
	}
	defer s.busy.Store(false)
	ctx, cancel := execution.NewContext(ctx, s.options)
	defer cancel()
	if len(command.Content.Texts) == 0 && command.Message != "" {
		command.Content.Texts = []model.TextPart{{Message: command.Message}}
	}
	registered, sessionID, err := s.resolveRunnerSession(command)
	if err != nil {
		return nil, err
	}
	result, err := registered.Runner.Run(ctx, command.UserID, sessionID, command.Content)
	if err == nil {
		err = ctx.Err()
	}
	return result, err
}

func (s *Service) HandleMessageStream(ctx context.Context, agentID, userID, sessionID, message string) (<-chan string, <-chan error) {
	return s.HandleCommandStream(ctx, model.ChatCommand{
		AgentID:   agentID,
		UserID:    userID,
		SessionID: sessionID,
		Message:   message,
		Content:   model.ChatContent{Texts: []model.TextPart{{Message: message}}},
	})
}

func (s *Service) HandleCommandStream(ctx context.Context, command model.ChatCommand) (<-chan string, <-chan error) {
	outputs := make(chan string, 1)
	errs := make(chan error, 1)
	// Acquire synchronously, before creating any execution goroutine.
	if !s.busy.CompareAndSwap(false, true) {
		errs <- execution.ErrBusy
		close(outputs)
		close(errs)
		return outputs, errs
	}
	ctx, cancel := execution.NewContext(ctx, s.options)
	go func() {
		defer close(outputs)
		defer close(errs)
		defer s.busy.Store(false)
		defer cancel()
		if len(command.Content.Texts) == 0 && command.Message != "" {
			command.Content.Texts = []model.TextPart{{Message: command.Message}}
		}
		registered, sessionID, err := s.resolveRunnerSession(command)
		if err != nil {
			errs <- err
			return
		}
		// Only complete final output is published; candidate XML never reaches the client.
		result, err := registered.Runner.Run(ctx, command.UserID, sessionID, command.Content)
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			errs <- err
			return
		}
		for _, text := range result {
			select {
			case outputs <- text:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
		}
	}()
	return outputs, errs
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
