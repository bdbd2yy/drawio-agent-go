package dto

type AiAgentConfigResponse struct {
	AgentID   string `json:"agentId"`
	AgentName string `json:"agentName"`
	AgentDesc string `json:"agentDesc"`
}

type CreateSessionRequest struct {
	AgentID string `json:"agentId"`
	UserID  string `json:"userId"`
}

type CreateSessionResponse struct {
	SessionID string `json:"sessionId"`
}

type ChatRequest struct {
	AgentID   string `json:"agentId"`
	UserID    string `json:"userId"`
	SessionID string `json:"sessionId"`
	Message   string `json:"message"`
}

type ChatResponse struct {
	Content string `json:"content"`
}
