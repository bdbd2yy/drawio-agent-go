package adk

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ai-agent-scaffold-go/internal/domain/agent/model"
	"ai-agent-scaffold-go/internal/domain/agent/ports"
	execution "ai-agent-scaffold-go/internal/domain/agent/runtime"
)

const diagram = `<mxfile><diagram><mxGraphModel><root><mxCell id="0"/><mxCell id="1" parent="0"/></root></mxGraphModel></diagram></mxfile>`

type fakeModel struct {
	calls    int
	generate func(context.Context, []ports.ChatMessage) (ports.ChatReply, error)
}

func (f *fakeModel) Generate(ctx context.Context, m []ports.ChatMessage) (ports.ChatReply, error) {
	f.calls++
	return f.generate(ctx, m)
}
func (f *fakeModel) Stream(context.Context, []ports.ChatMessage) (<-chan ports.ChatStreamEvent, <-chan error) {
	panic("unexpected streaming candidate")
}
func (f *fakeModel) Tools() []ports.Tool { return nil }

type fakeTool struct {
	call  func(context.Context) (string, error)
	calls int
}

func (f *fakeTool) CallTool(ctx context.Context, _, _ string) (string, error) {
	f.calls++
	return f.call(ctx)
}
func content() model.ChatContent {
	return model.ChatContent{Texts: []model.TextPart{{Message: "draw"}}}
}
func TestDrawingRepair(t *testing.T) {
	for _, successAt := range []int{1, 2, 3, 4} {
		t.Run(string(rune('0'+successAt)), func(t *testing.T) {
			m := &fakeModel{}
			m.generate = func(_ context.Context, msg []ports.ChatMessage) (ports.ChatReply, error) {
				if m.calls > 1 && !strings.Contains(msg[len(msg)-1].Content, "Validation error:") {
					t.Fatal("missing feedback")
				}
				if m.calls >= successAt {
					return ports.ChatReply{Content: diagram}, nil
				}
				return ports.ChatReply{Content: "broken"}, nil
			}
			drawer := &Agent{name: "drawer", kind: "llm", chatModel: m}
			workflow := &Agent{name: "draw", kind: "sequential", subAgents: []ports.Agent{drawer}, validateDrawing: true, repairAgent: "drawer"}
			ctx, cancel := execution.NewContext(context.Background(), execution.Defaults())
			defer cancel()
			out, err := workflow.run(ctx, content())
			if successAt <= 3 {
				if err != nil || out != diagram {
					t.Fatalf("%q %v", out, err)
				}
			} else if err == nil || out != "" {
				t.Fatal("invalid final result escaped")
			}
			expected := successAt
			if expected > 3 {
				expected = 3
			}
			if m.calls != expected {
				t.Fatalf("calls=%d want %d", m.calls, expected)
			}
		})
	}
}
func TestModelLimits(t *testing.T) {
	for _, kind := range []string{"timeout", "cancel", "budget", "size"} {
		t.Run(kind, func(t *testing.T) {
			o := execution.Defaults()
			o.ModelTimeout = 5 * time.Millisecond
			o.MaxModelCalls = 1
			o.MaxModelBytes = 4
			m := &fakeModel{generate: func(ctx context.Context, _ []ports.ChatMessage) (ports.ChatReply, error) {
				if kind == "timeout" || kind == "cancel" {
					<-ctx.Done()
					return ports.ChatReply{}, ctx.Err()
				}
				if kind == "size" {
					return ports.ChatReply{Content: "12345"}, nil
				}
				return ports.ChatReply{ToolCalls: []ports.ChatToolCall{{ID: "i", Name: "t"}}}, nil
			}}
			tool := &fakeTool{call: func(context.Context) (string, error) { return "ok", nil }}
			a := &Agent{name: "a", kind: "llm", chatModel: m, router: tool}
			ctx, cancel := execution.NewContext(context.Background(), o)
			defer cancel()
			if kind == "cancel" {
				cancel()
			}
			_, err := a.run(ctx, content())
			want := execution.ErrBudget
			switch kind {
			case "timeout":
				want = context.DeadlineExceeded
			case "cancel":
				want = context.Canceled
			case "size":
				want = execution.ErrSize
			}
			if !errors.Is(err, want) {
				t.Fatalf("got %v want %v", err, want)
			}
		})
	}
}
func TestToolLimitsNoRetry(t *testing.T) {
	for _, kind := range []string{"timeout", "size", "failure"} {
		t.Run(kind, func(t *testing.T) {
			o := execution.Defaults()
			o.ToolTimeout = time.Millisecond
			o.MaxToolBytes = 2
			tool := &fakeTool{call: func(ctx context.Context) (string, error) {
				if kind == "timeout" {
					<-ctx.Done()
					return "", ctx.Err()
				}
				if kind == "failure" {
					return "", errors.New("upstream")
				}
				return "big", nil
			}}
			a := &Agent{router: tool}
			ctx, cancel := execution.NewContext(context.Background(), o)
			defer cancel()
			_, err := a.executeToolCalls(ctx, []ports.ChatToolCall{{Name: "t"}})
			if err == nil || tool.calls != 1 {
				t.Fatalf("err=%v calls=%d", err, tool.calls)
			}
		})
	}
}
func TestRunnerStreamCancellation(t *testing.T) {
	m := &fakeModel{generate: func(ctx context.Context, _ []ports.ChatMessage) (ports.ChatReply, error) {
		<-ctx.Done()
		return ports.ChatReply{}, ctx.Err()
	}}
	r := &Runner{appName: "a", agent: &Agent{name: "a", kind: "llm", chatModel: m}, counter: &atomic.Uint64{}}
	ctx, cancel := context.WithCancel(context.Background())
	out, errs := r.Stream(ctx, "u", "s", content())
	cancel()
	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream leaked")
	}
	for range out {
	}
}

func TestWorkflowSharesBudgetAcrossAgents(t *testing.T) {
	o := execution.Defaults()
	o.MaxModelCalls = 2
	m := &fakeModel{generate: func(context.Context, []ports.ChatMessage) (ports.ChatReply, error) {
		return ports.ChatReply{Content: "broken"}, nil
	}}
	analyst := &Agent{name: "analyst", kind: "llm", chatModel: m}
	drawer := &Agent{name: "drawer", kind: "llm", chatModel: m}
	workflow := &Agent{name: "draw", kind: "sequential", subAgents: []ports.Agent{analyst, drawer}, validateDrawing: true, repairAgent: "drawer"}
	ctx, cancel := execution.NewContext(context.Background(), o)
	defer cancel()
	if _, err := workflow.run(ctx, content()); !errors.Is(err, execution.ErrBudget) {
		t.Fatal(err)
	}
	if m.calls != 2 {
		t.Fatalf("budget escaped workflow: %d", m.calls)
	}
}
