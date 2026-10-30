package model

import "context"

type ArmoryCommand struct {
	Table AiAgentConfigTable
}

type ChatCommand struct {
	AgentID   string
	UserID    string
	SessionID string
	Message   string
	Content   ChatContent
}

type ChatContent struct {
	Texts       []TextPart
	Files       []FilePart
	InlineDatas []InlineDataPart
}

type TextPart struct {
	Message string
}

type FilePart struct {
	FileURI  string
	MimeType string
}

type InlineDataPart struct {
	Bytes    []byte
	MimeType string
}

type RegisteredAgent struct {
	AppName   string
	AgentID   string
	AgentName string
	AgentDesc string
	Runner    Runner
}

type Runner interface {
	CreateSession(userID string) (string, error)
	Run(ctx context.Context, userID, sessionID string, content ChatContent) ([]string, error)
	Stream(ctx context.Context, userID, sessionID string, content ChatContent) (<-chan string, <-chan error)
}
