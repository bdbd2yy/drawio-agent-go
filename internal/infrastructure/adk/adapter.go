package adk

import (
	execution "ai-agent-scaffold-go/internal/domain/agent/runtime"
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"ai-agent-scaffold-go/internal/domain/agent/model"
	"ai-agent-scaffold-go/internal/domain/agent/ports"
	"ai-agent-scaffold-go/internal/domain/drawing"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/plugin/loggingplugin"
)

type Factory struct {
	sessionCounter atomic.Uint64
	plugins        map[string]func() (ports.RunnerPlugin, error)
	router         ports.ToolRouter
}

type Agent struct {
	name            string
	kind            string
	description     string
	instruction     string
	outputKey       string
	chatModel       ports.ChatModel
	router          ports.ToolRouter
	subAgents       []ports.Agent
	validateDrawing bool
	repairAgent     string
}

type Runner struct {
	appName string
	agent   ports.Agent
	plugins []ports.RunnerPlugin
	counter *atomic.Uint64
}

func NewFactory() *Factory {
	return &Factory{plugins: defaultPlugins()}
}

func (f *Factory) UseToolRouter(router ports.ToolRouter) {
	f.router = router
}

func (f *Factory) NewLLMAgent(_ context.Context, config model.AgentConfig, chatModel ports.ChatModel) (ports.Agent, error) {
	if strings.TrimSpace(config.Name) == "" {
		return nil, fmt.Errorf("agent name is required")
	}
	if chatModel == nil {
		return nil, fmt.Errorf("agent %q requires a chat model", config.Name)
	}
	return &Agent{
		name:        config.Name,
		kind:        "llm",
		description: config.Description,
		instruction: config.Instruction,
		outputKey:   config.OutputKey,
		chatModel:   chatModel,
		router:      f.router,
	}, nil
}

func (f *Factory) NewLoopAgent(_ context.Context, config model.AgentWorkflowConfig, subAgents []ports.Agent) (ports.Agent, error) {
	return newWorkflowAgent("loop", config, subAgents, f.router)
}

func (f *Factory) NewParallelAgent(_ context.Context, config model.AgentWorkflowConfig, subAgents []ports.Agent) (ports.Agent, error) {
	return newWorkflowAgent("parallel", config, subAgents, f.router)
}

func (f *Factory) NewSequentialAgent(_ context.Context, config model.AgentWorkflowConfig, subAgents []ports.Agent) (ports.Agent, error) {
	return newWorkflowAgent("sequential", config, subAgents, f.router)
}

func (f *Factory) NewRunner(_ context.Context, appName string, agent ports.Agent, pluginNames []string) (model.Runner, error) {
	if strings.TrimSpace(appName) == "" {
		return nil, fmt.Errorf("app name is required")
	}
	if agent == nil {
		return nil, fmt.Errorf("agent is required")
	}
	plugins, err := f.resolvePlugins(pluginNames)
	if err != nil {
		return nil, err
	}
	return &Runner{appName: appName, agent: agent, plugins: plugins, counter: &f.sessionCounter}, nil
}

func newWorkflowAgent(kind string, config model.AgentWorkflowConfig, subAgents []ports.Agent, router ports.ToolRouter) (ports.Agent, error) {
	if strings.TrimSpace(config.Name) == "" {
		return nil, fmt.Errorf("%s agent name is required", kind)
	}
	if config.ValidateDrawing {
		if kind != "sequential" {
			return nil, fmt.Errorf("drawing validation requires a sequential workflow")
		}
		found := false
		for _, sub := range subAgents {
			if a, ok := sub.(*Agent); ok && a.name == config.RepairAgent && a.kind == "llm" {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("drawing repair-agent %q must reference a direct LLM sub-agent", config.RepairAgent)
		}
	}
	return &Agent{
		name:            config.Name,
		kind:            kind,
		description:     config.Description,
		subAgents:       subAgents,
		validateDrawing: config.ValidateDrawing,
		repairAgent:     config.RepairAgent,
		router:          router,
	}, nil
}

func (a *Agent) Name() string {
	return a.name
}

func (a *Agent) Description() string {
	return a.description
}

func (a *Agent) run(ctx context.Context, content model.ChatContent) (string, error) {
	return a.runWithVars(ctx, content, map[string]string{})
}

func (a *Agent) runWithVars(ctx context.Context, content model.ChatContent, vars map[string]string) (string, error) {
	switch a.kind {
	case "llm":
		return a.runLLM(ctx, content, vars)
	case "sequential":
		return a.runSequential(ctx, content, vars)
	case "loop", "parallel":
		return a.runFanOut(ctx, content, vars)
	default:
		return "", fmt.Errorf("agent %q has unknown kind %q", a.name, a.kind)
	}
}

func (a *Agent) stream(ctx context.Context, content model.ChatContent, out chan<- string) error {
	switch a.kind {
	case "llm":
		return a.streamLLM(ctx, content, out, map[string]string{})
	default:
		text, err := a.run(ctx, content)
		if err != nil {
			return err
		}
		if text != "" {
			select {
			case out <- text:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
}

func (a *Agent) runLLM(ctx context.Context, content model.ChatContent, vars map[string]string) (string, error) {
	messages := initialMessages(applyVars(a.instruction, vars), firstText(content))
	for iter := 0; iter < execution.OptionsFrom(ctx).MaxModelCalls; iter++ {
		callCtx, cancel, err := execution.ModelContext(ctx)
		if err != nil {
			return "", err
		}
		reply, err := a.chatModel.Generate(callCtx, messages)
		if err == nil {
			err = callCtx.Err()
		}
		cancel()
		if err == nil {
			size := len(reply.Content)
			for _, call := range reply.ToolCalls {
				size += len(call.Arguments) + len(call.Name) + len(call.ID)
			}
			if int64(size) > execution.OptionsFrom(ctx).MaxModelBytes {
				err = execution.ErrSize
			}
		}
		if err != nil {
			return "", err
		}
		if len(reply.ToolCalls) == 0 {
			return reply.Content, nil
		}
		messages = append(messages, ports.ChatMessage{Role: ports.ChatRoleAssistant, Content: reply.Content, ToolCalls: reply.ToolCalls})
		toolMessages, err := a.executeToolCalls(ctx, reply.ToolCalls)
		if err != nil {
			return "", err
		}
		messages = append(messages, toolMessages...)
	}
	return "", execution.ErrBudget
}

func (a *Agent) streamLLM(ctx context.Context, content model.ChatContent, out chan<- string, vars map[string]string) error {
	text, err := a.runLLM(ctx, content, vars)
	if err != nil {
		return err
	}
	select {
	case out <- text:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *Agent) executeToolCalls(ctx context.Context, calls []ports.ChatToolCall) ([]ports.ChatMessage, error) {
	if a.router == nil {
		return nil, fmt.Errorf("agent %q has no tool router but model requested tool calls", a.name)
	}
	out := make([]ports.ChatMessage, 0, len(calls))
	for _, call := range calls {
		toolCtx, cancel := context.WithTimeout(ctx, execution.OptionsFrom(ctx).ToolTimeout)
		result, err := a.router.CallTool(toolCtx, call.Name, call.Arguments)
		if err == nil {
			err = toolCtx.Err()
		}
		cancel()
		if err == nil && int64(len(result)) > execution.OptionsFrom(ctx).MaxToolBytes {
			err = execution.ErrSize
		}
		if err != nil {
			return nil, fmt.Errorf("tool %q: %w", call.Name, err)
		}
		out = append(out, ports.ChatMessage{
			Role:       ports.ChatRoleTool,
			Content:    result,
			ToolCallID: call.ID,
			Name:       call.Name,
		})
	}
	return out, nil
}

func (a *Agent) runSequential(ctx context.Context, content model.ChatContent, vars map[string]string) (string, error) {
	scope := cloneVars(vars)
	var last string
	for _, sub := range a.subAgents {
		impl, ok := sub.(*Agent)
		if !ok {
			return "", fmt.Errorf("sub-agent %q is not a runnable agent", sub.Name())
		}
		text, err := impl.runWithVars(ctx, content, scope)
		if err != nil {
			return "", err
		}
		last = text
		if key := strings.TrimSpace(impl.outputKey); key != "" {
			scope[key] = text
		}
	}
	if a.validateDrawing {
		for attempt := 0; ; attempt++ {
			validationErr := drawing.Validate(last)
			if validationErr == nil {
				return last, nil
			}
			if attempt >= 2 {
				return "", fmt.Errorf("drawing validation failed after 2 repairs: %w", validationErr)
			}
			var repair *Agent
			for _, sub := range a.subAgents {
				if impl, ok := sub.(*Agent); ok && impl.name == a.repairAgent {
					repair = impl
					break
				}
			}
			if repair == nil {
				return "", fmt.Errorf("drawing repair agent %q not found", a.repairAgent)
			}
			feedback := firstText(content) + "\nRepair the following candidate XML using the validation error. Return only complete mxfile XML.\nValidation error: " + validationErr.Error() + "\nCandidate XML:\n" + last
			var err error
			last, err = repair.runWithVars(ctx, model.ChatContent{Texts: []model.TextPart{{Message: feedback}}}, scope)
			if err != nil {
				return "", err
			}
		}
	}
	return last, nil
}

func (a *Agent) runFanOut(ctx context.Context, content model.ChatContent, vars map[string]string) (string, error) {
	parts := make([]string, 0, len(a.subAgents))
	for _, sub := range a.subAgents {
		impl, ok := sub.(*Agent)
		if !ok {
			return "", fmt.Errorf("sub-agent %q is not a runnable agent", sub.Name())
		}
		text, err := impl.runWithVars(ctx, content, vars)
		if err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("[%s] %s", sub.Name(), text))
	}
	return strings.Join(parts, "\n"), nil
}

func cloneVars(vars map[string]string) map[string]string {
	out := make(map[string]string, len(vars)+4)
	for k, v := range vars {
		out[k] = v
	}
	return out
}

func applyVars(template string, vars map[string]string) string {
	if template == "" || len(vars) == 0 {
		return template
	}
	out := template
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{"+k+"}", v)
	}
	return out
}

func initialMessages(instruction, userText string) []ports.ChatMessage {
	messages := make([]ports.ChatMessage, 0, 2)
	if strings.TrimSpace(instruction) != "" {
		messages = append(messages, ports.ChatMessage{Role: ports.ChatRoleSystem, Content: instruction})
	}
	messages = append(messages, ports.ChatMessage{Role: ports.ChatRoleUser, Content: userText})
	return messages
}

func (r *Runner) CreateSession(userID string) (string, error) {
	if strings.TrimSpace(userID) == "" {
		return "", fmt.Errorf("user id is required")
	}
	next := r.counter.Add(1)
	return fmt.Sprintf("%s:%s:%d", r.appName, userID, next), nil
}

func (r *Runner) Run(ctx context.Context, userID, sessionID string, content model.ChatContent) ([]string, error) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(sessionID) == "" {
		return nil, fmt.Errorf("user id and session id are required")
	}
	ctx, cancel := execution.EnsureContext(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := r.notifyPlugins(ctx, userID, sessionID, content); err != nil {
		return nil, err
	}
	impl, ok := r.agent.(*Agent)
	if !ok {
		return nil, fmt.Errorf("runner agent %q is not runnable", r.agent.Name())
	}
	output, err := impl.run(ctx, content)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if output == "" {
		return []string{}, nil
	}
	return []string{output}, nil
}

func (r *Runner) Stream(ctx context.Context, userID, sessionID string, content model.ChatContent) (<-chan string, <-chan error) {
	outputs := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		defer close(outputs)
		defer close(errs)
		result, err := r.Run(ctx, userID, sessionID, content)
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

func (r *Runner) notifyPlugins(ctx context.Context, userID, sessionID string, content model.ChatContent) error {
	for _, item := range r.plugins {
		if err := item.OnUserMessage(ctx, r.appName, userID, sessionID, r.agent, content); err != nil {
			return fmt.Errorf("plugin %q on user message: %w", item.Name(), err)
		}
		if err := item.BeforeAgent(ctx, r.appName, userID, sessionID, r.agent); err != nil {
			return fmt.Errorf("plugin %q before agent: %w", item.Name(), err)
		}
	}
	return nil
}

func firstText(content model.ChatContent) string {
	if len(content.Texts) == 0 {
		return ""
	}
	return content.Texts[0].Message
}

func defaultPlugins() map[string]func() (ports.RunnerPlugin, error) {
	return map[string]func() (ports.RunnerPlugin, error){
		"myTestPlugin": newMyTestPlugin,
		"myLogPlugin": func() (ports.RunnerPlugin, error) {
			p, err := loggingplugin.New("myLogPlugin")
			if err != nil {
				return nil, err
			}
			return adkRunnerPlugin{name: "myLogPlugin", plugin: p}, nil
		},
	}
}

func (f *Factory) resolvePlugins(names []string) ([]ports.RunnerPlugin, error) {
	if len(names) == 0 {
		return nil, nil
	}
	plugins := make([]ports.RunnerPlugin, 0, len(names))
	for _, name := range names {
		pluginName := strings.TrimSpace(name)
		if pluginName == "" {
			continue
		}
		builder, ok := f.plugins[pluginName]
		if !ok {
			return nil, fmt.Errorf("runner plugin %q is not registered", pluginName)
		}
		plugin, err := builder()
		if err != nil {
			return nil, fmt.Errorf("create runner plugin %q: %w", pluginName, err)
		}
		plugins = append(plugins, plugin)
	}
	return plugins, nil
}

func newMyTestPlugin() (ports.RunnerPlugin, error) {
	return myTestPlugin{}, nil
}

type myTestPlugin struct{}

func (myTestPlugin) Name() string {
	return "myTestPlugin"
}

func (myTestPlugin) OnUserMessage(_ context.Context, _, _, _ string, _ ports.Agent, content model.ChatContent) error {
	fmt.Printf("[myTestPlugin] 用户输入信息:%s\n", firstText(content))
	return nil
}

func (myTestPlugin) BeforeAgent(_ context.Context, _, _, _ string, agent ports.Agent) error {
	fmt.Printf("[myTestPlugin] 智能体名称:%s\n", agent.Name())
	return nil
}

type adkRunnerPlugin struct {
	name   string
	plugin *plugin.Plugin
}

func (p adkRunnerPlugin) Name() string {
	return p.name
}

func (p adkRunnerPlugin) OnUserMessage(_ context.Context, _, _, _ string, _ ports.Agent, content model.ChatContent) error {
	if p.plugin.OnUserMessageCallback() != nil {
		fmt.Printf("[%s] USER MESSAGE RECEIVED %s\n", p.name, firstText(content))
	}
	return nil
}

func (p adkRunnerPlugin) BeforeAgent(_ context.Context, _, _, _ string, agent ports.Agent) error {
	if p.plugin.BeforeAgentCallback() != nil {
		fmt.Printf("[%s] AGENT STARTING %s\n", p.name, agent.Name())
	}
	return nil
}
