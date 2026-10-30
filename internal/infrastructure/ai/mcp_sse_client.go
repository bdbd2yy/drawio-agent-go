package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ai-agent-scaffold-go/internal/domain/agent/ports"
	execution "ai-agent-scaffold-go/internal/domain/agent/runtime"
)

const (
	mcpProtocolVersion = "2024-11-05"
	mcpClientName      = "ai-agent-scaffold-go"
	mcpClientVersion   = "0.1.0"
)

type MCPSSEClient struct {
	baseURI        string
	endpoint       string
	httpClient     *http.Client
	timeout        time.Duration
	maxResultBytes int64

	mu          sync.Mutex
	started     bool
	postURL     string
	cancelFn    context.CancelFunc
	pending     map[uint64]chan json.RawMessage
	nextID      atomic.Uint64
	streamErr   chan error
	endpointSig chan struct{}
}

func NewMCPSSEClient(baseURI, endpoint string, requestTimeoutMillis int) *MCPSSEClient {
	timeout := time.Duration(requestTimeoutMillis) * time.Millisecond
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &MCPSSEClient{
		baseURI:        strings.TrimRight(baseURI, "/"),
		endpoint:       endpoint,
		httpClient:     &http.Client{Timeout: 0},
		timeout:        timeout,
		maxResultBytes: execution.Defaults().MaxToolBytes,
	}
}

func (c *MCPSSEClient) ensureStarted(ctx context.Context) error {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	c.pending = make(map[uint64]chan json.RawMessage)
	c.endpointSig = make(chan struct{})
	c.streamErr = make(chan error, 1)
	streamCtx, cancel := context.WithCancel(ctx)
	c.cancelFn = cancel
	c.started = true
	c.mu.Unlock()

	if err := c.openSSE(streamCtx); err != nil {
		c.shutdown()
		return err
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, c.timeout)
	defer waitCancel()
	select {
	case <-c.endpointSig:
	case err := <-c.streamErr:
		c.shutdown()
		return err
	case <-waitCtx.Done():
		c.shutdown()
		return fmt.Errorf("mcp sse endpoint event timeout: %w", waitCtx.Err())
	}

	if err := c.initialize(ctx); err != nil {
		c.shutdown()
		return err
	}
	return nil
}

func (c *MCPSSEClient) openSSE(ctx context.Context) error {
	target := c.baseURI + c.endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("mcp sse build request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("mcp sse open: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := execution.ReadBounded(resp.Body, c.maxResultBytes*6+65536)
		_ = resp.Body.Close()
		return fmt.Errorf("mcp sse status %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	go c.readLoop(resp)
	return nil
}

func (c *MCPSSEClient) readLoop(resp *http.Response) {
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), int(c.maxResultBytes*6+65536))
	var event string
	var dataBuf strings.Builder
	dispatch := func() {
		defer func() {
			event = ""
			dataBuf.Reset()
		}()
		data := dataBuf.String()
		if data == "" {
			return
		}
		switch event {
		case "endpoint", "":
			c.handleEndpoint(data, event)
		case "message":
			c.handleMessage(data)
		}
	}
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			dispatch()
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			if int64(dataBuf.Len()) > c.maxResultBytes*6+65536 {
				c.signalStreamErr(execution.ErrSize)
				return
			}
		}
	}
	c.signalStreamErr(fmt.Errorf("mcp stream closed: %v", scanner.Err()))
}

func (c *MCPSSEClient) handleEndpoint(data, eventName string) {
	if c.postURLAlreadySet() {
		if eventName == "" {
			c.handleMessage(data)
		}
		return
	}
	target := c.resolvePostURL(data)
	c.mu.Lock()
	if c.started && c.postURL == "" {
		c.postURL = target
		close(c.endpointSig)
	}
	c.mu.Unlock()
}

func (c *MCPSSEClient) postURLAlreadySet() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.postURL != ""
}

func (c *MCPSSEClient) resolvePostURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() {
		base, baseErr := url.Parse(c.baseURI)
		if baseErr == nil {
			ref, refErr := url.Parse(raw)
			if refErr == nil {
				return base.ResolveReference(ref).String()
			}
		}
	}
	return raw
}

func (c *MCPSSEClient) handleMessage(data string) {
	var resp struct {
		ID     json.Number     `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		return
	}
	if resp.ID == "" {
		return
	}
	id, err := resp.ID.Int64()
	if err != nil || id <= 0 {
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[uint64(id)]
	if ok {
		delete(c.pending, uint64(id))
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	if resp.Error != nil {
		ch <- mustJSON(map[string]any{"__error__": resp.Error.Message, "code": resp.Error.Code})
		close(ch)
		return
	}
	ch <- resp.Result
	close(ch)
}

func mustJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

func (c *MCPSSEClient) signalStreamErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case c.streamErr <- err:
	default:
	}
	for id, ch := range c.pending {
		ch <- mustJSON(map[string]any{"__error__": err.Error()})
		close(ch)
		delete(c.pending, id)
	}
}

func (c *MCPSSEClient) shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancelFn != nil {
		c.cancelFn()
	}
	c.started = false
	c.postURL = ""
	c.pending = nil
}

func (c *MCPSSEClient) initialize(ctx context.Context) error {
	_, err := c.callRPC(ctx, "initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    mcpClientName,
			"version": mcpClientVersion,
		},
	})
	if err != nil {
		return fmt.Errorf("mcp initialize: %w", err)
	}
	if err := c.notify(ctx, "notifications/initialized", map[string]any{}); err != nil {
		return fmt.Errorf("mcp notifications/initialized: %w", err)
	}
	return nil
}

func (c *MCPSSEClient) CallTool(ctx context.Context, name, arguments string) (string, error) {
	if err := c.ensureStarted(ctx); err != nil {
		return "", err
	}
	args := map[string]any{}
	trimmed := strings.TrimSpace(arguments)
	if trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
			return "", fmt.Errorf("mcp tool %q arguments not valid json: %w", name, err)
		}
	}
	result, err := c.callRPC(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return "", fmt.Errorf("mcp tools/call %q: %w", name, err)
	}
	var parsed toolCallResult
	if err := json.Unmarshal(result, &parsed); err != nil {
		return "", fmt.Errorf("invalid tool result: %w", err)
	}
	if parsed.IsError {
		return "", fmt.Errorf("mcp tool %q reported failure", name)
	}
	text := extractToolText(result)
	if int64(len(text)) > c.maxResultBytes {
		return "", execution.ErrSize
	}
	return text, nil
}

func (c *MCPSSEClient) ListTools(ctx context.Context) ([]string, error) {
	if err := c.ensureStarted(ctx); err != nil {
		return nil, err
	}
	result, err := c.callRPC(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(parsed.Tools))
	for _, t := range parsed.Tools {
		names = append(names, t.Name)
	}
	return names, nil
}

type toolCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

func extractToolText(raw json.RawMessage) string {
	var parsed toolCallResult
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return string(raw)
	}
	parts := make([]string, 0, len(parsed.Content))
	for _, item := range parsed.Content {
		if item.Type == "text" && item.Text != "" {
			parts = append(parts, item.Text)
		}
	}
	if len(parts) == 0 {
		return string(raw)
	}
	return strings.Join(parts, "\n")
}

func (c *MCPSSEClient) callRPC(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan json.RawMessage, 1)
	c.mu.Lock()
	postURL := c.postURL
	if c.pending == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp connection is closed")
	}
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if postURL == "" {
		return nil, fmt.Errorf("mcp post url is not set")
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return nil, fmt.Errorf("mcp marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("mcp build post: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp post: %w", err)
	}
	_, readErr := execution.ReadBounded(resp.Body, c.maxResultBytes*6+65536)
	resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("mcp post status %d", resp.StatusCode)
	}

	waitCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	select {
	case msg, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("mcp call %s closed unexpectedly", method)
		}
		var probe struct {
			Err string `json:"__error__"`
		}
		if err := json.Unmarshal(msg, &probe); err == nil && probe.Err != "" {
			return nil, fmt.Errorf("%s", probe.Err)
		}
		return msg, nil
	case <-waitCtx.Done():
		return nil, fmt.Errorf("mcp call %s timeout: %w", method, waitCtx.Err())
	}
}

func (c *MCPSSEClient) notify(ctx context.Context, method string, params any) error {
	c.mu.Lock()
	postURL := c.postURL
	c.mu.Unlock()
	if postURL == "" {
		return fmt.Errorf("mcp post url is not set")
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	_, readErr := execution.ReadBounded(resp.Body, c.maxResultBytes*6+65536)
	resp.Body.Close()
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("mcp notification status %d", resp.StatusCode)
	}
	return nil
}

type MCPToolRouter struct {
	clients      map[string]*MCPSSEClient
	tools        []ports.Tool
	registered   map[string]struct{}
	listTimeout  time.Duration
	listFailures map[string]error
}

func NewMCPToolRouter() *MCPToolRouter {
	return &MCPToolRouter{
		clients:      make(map[string]*MCPSSEClient),
		registered:   make(map[string]struct{}),
		listTimeout:  10 * time.Second,
		listFailures: make(map[string]error),
	}
}

func (r *MCPToolRouter) Register(tool ports.Tool) {
	r.RegisterAndExpand(tool)
}

func (r *MCPToolRouter) RegisterAndExpand(tool ports.Tool) []ports.Tool {
	mcp, ok := tool.(MCPTool)
	if !ok {
		r.tools = append(r.tools, tool)
		return []ports.Tool{tool}
	}
	if mcp.TransportType != "sse" {
		r.tools = append(r.tools, mcp)
		return []ports.Tool{mcp}
	}
	if _, exists := r.clients[mcp.ToolName]; exists {
		return nil
	}
	client := NewMCPSSEClient(mcp.BaseURI, mcp.SSEEndpoint, mcp.RequestTimeout)
	r.clients[mcp.ToolName] = client

	ctx, cancel := context.WithTimeout(context.Background(), r.listTimeout)
	defer cancel()
	names, err := client.ListTools(ctx)
	client.shutdown()
	if err != nil {
		r.listFailures[mcp.ToolName] = err
		r.tools = append(r.tools, mcp)
		return []ports.Tool{mcp}
	}
	expanded := make([]ports.Tool, 0, len(names))
	for _, n := range names {
		r.clients[n] = client
		r.registered[n] = struct{}{}
		t := EinoTool{ToolName: n}
		r.tools = append(r.tools, t)
		expanded = append(expanded, t)
	}
	return expanded
}

func (r *MCPToolRouter) Tools() []ports.Tool {
	return r.tools
}

func (r *MCPToolRouter) CallTool(ctx context.Context, name, arguments string) (string, error) {
	if client, ok := r.clients[name]; ok {
		// A request owns its transport; cancellation closes its SSE reader as well.
		scoped := NewMCPSSEClient(client.baseURI, client.endpoint, int(client.timeout/time.Millisecond))
		scoped.maxResultBytes = execution.OptionsFrom(ctx).MaxToolBytes
		defer scoped.shutdown()
		return scoped.CallTool(ctx, name, arguments)
	}
	for _, tool := range r.tools {
		if tool.Name() != name {
			continue
		}
		switch tool.(type) {
		case MCPTool:
			return "", fmt.Errorf("mcp tool %q transport not supported in runtime", name)
		default:
			return "", fmt.Errorf("tool %q is not callable in current runtime", name)
		}
	}
	return "", fmt.Errorf("tool %q is not registered", name)
}
