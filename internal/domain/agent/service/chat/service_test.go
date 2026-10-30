package chat

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"ai-agent-scaffold-go/internal/domain/agent/model"
	"ai-agent-scaffold-go/internal/domain/agent/ports"
	execution "ai-agent-scaffold-go/internal/domain/agent/runtime"
)

type runner struct {
	run func(context.Context) ([]string, error)
}

func (r runner) CreateSession(string) (string, error) { return "s", nil }
func (r runner) Run(ctx context.Context, _, _ string, _ model.ChatContent) ([]string, error) {
	return r.run(ctx)
}
func (r runner) Stream(context.Context, string, string, model.ChatContent) (<-chan string, <-chan error) {
	panic("not used")
}
func service(t *testing.T, r runner) *Service {
	t.Helper()
	reg := ports.NewInMemoryAgentRegistry()
	if err := reg.Register(model.RegisteredAgent{AgentID: "a", Runner: r}); err != nil {
		t.Fatal(err)
	}
	return NewService(reg, ports.NewInMemorySessionStore())
}
func TestAdmissionAndRelease(t *testing.T) {
	started := make(chan struct{})
	var count atomic.Int32
	s := service(t, runner{run: func(ctx context.Context) ([]string, error) {
		if count.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return []string{"ok"}, nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := s.HandleMessage(ctx, "a", "u", "s", "draw"); done <- err }()
	<-started
	if _, err := s.HandleMessage(context.Background(), "a", "u", "s", "draw"); !errors.Is(err, execution.ErrBusy) {
		t.Fatal(err)
	}
	out, errs := s.HandleMessageStream(context.Background(), "a", "u", "s", "draw")
	for range out {
	}
	if err := <-errs; !errors.Is(err, execution.ErrBusy) {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.HandleMessage(context.Background(), "a", "u", "s", "draw"); err != nil {
		t.Fatal(err)
	}
}
func TestFailureAndTimeoutRelease(t *testing.T) {
	for _, kind := range []string{"failure", "timeout", "budget", "size"} {
		t.Run(kind, func(t *testing.T) {
			var count int
			s := service(t, runner{run: func(ctx context.Context) ([]string, error) {
				count++
				if count > 1 {
					return []string{"ok"}, nil
				}
				switch kind {
				case "timeout":
					<-ctx.Done()
					return nil, ctx.Err()
				case "budget":
					return nil, execution.ErrBudget
				case "size":
					return nil, execution.ErrSize
				}
				return nil, errors.New("failed")
			}})
			o := execution.Defaults()
			o.RequestTimeout = time.Millisecond
			s.WithOptions(o)
			if _, err := s.HandleMessage(context.Background(), "a", "u", "s", "draw"); err == nil {
				t.Fatal("expected failure")
			}
			if _, err := s.HandleMessage(context.Background(), "a", "u", "s", "draw"); err != nil {
				t.Fatalf("slot leaked: %v", err)
			}
		})
	}
}
