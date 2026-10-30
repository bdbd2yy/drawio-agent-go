package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-agent-scaffold-go/internal/domain/agent/model"
	"ai-agent-scaffold-go/internal/domain/agent/ports"
	execution "ai-agent-scaffold-go/internal/domain/agent/runtime"
	"ai-agent-scaffold-go/internal/domain/agent/service/chat"
	"github.com/gin-gonic/gin"
)

type testRunner struct {
	run func(context.Context) ([]string, error)
}

func (r testRunner) CreateSession(string) (string, error) { return "s", nil }
func (r testRunner) Run(ctx context.Context, _, _ string, _ model.ChatContent) ([]string, error) {
	return r.run(ctx)
}
func (r testRunner) Stream(context.Context, string, string, model.ChatContent) (<-chan string, <-chan error) {
	panic("not used")
}
func setup(t *testing.T, r testRunner, limit int64) *gin.Engine {
	t.Helper()
	reg := ports.NewInMemoryAgentRegistry()
	if err := reg.Register(model.RegisteredAgent{AgentID: "a", Runner: r}); err != nil {
		t.Fatal(err)
	}
	s := chat.NewService(reg, ports.NewInMemorySessionStore())
	o := execution.Defaults()
	o.MaxRequestBytes = limit
	s.WithOptions(o)
	router := gin.New()
	RegisterAgentRoutes(router, s)
	return router
}

const body = `{"agentId":"a","userId":"u","sessionId":"s","message":"draw"}`

func TestRequestValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		limit      int64
		status     int
	}{
		{"valid", body, 1024, 200}, {"boundary", body, int64(len(body)), 200}, {"size", body, int64(len(body) - 1), 413}, {"trailing", body + strings.Repeat(" ", 1024), 1024, 413}, {"invalid", "{", 1024, 400}, {"empty", "{}", 1024, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := setup(t, testRunner{run: func(context.Context) ([]string, error) { return []string{"ok"}, nil }}, tc.limit)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
		})
	}
}
func TestStreamFinalAndFailure(t *testing.T) {
	for _, failure := range []bool{false, true} {
		r := setup(t, testRunner{run: func(context.Context) ([]string, error) {
			if failure {
				return nil, execution.ErrBudget
			}
			return []string{"final-xml"}, nil
		}}, 1024)
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/chat_stream", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		text := w.Body.String()
		if failure {
			if !strings.Contains(text, "event:error") || strings.Contains(text, "event:done") || strings.Contains(text, "event:message") {
				t.Fatal(text)
			}
		} else if !strings.Contains(text, "event:message") || !strings.Contains(text, "event:done") {
			t.Fatal(text)
		}
	}
}
func TestBusyHTTP(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	r := setup(t, testRunner{run: func(context.Context) ([]string, error) { close(started); <-release; return nil, nil }}, 1024)
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(body))
		r.ServeHTTP(httptest.NewRecorder(), req)
	}()
	<-started
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(body)))
	close(release)
	<-done
	if w.Code != 429 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
