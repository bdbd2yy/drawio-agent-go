package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"ai-agent-scaffold-go/internal/api/dto"
	"ai-agent-scaffold-go/internal/api/response"
	execution "ai-agent-scaffold-go/internal/domain/agent/runtime"
	"ai-agent-scaffold-go/internal/domain/agent/service/chat"
	"ai-agent-scaffold-go/pkg/types"
	"github.com/gin-gonic/gin"
)

func RegisterAgentRoutes(router gin.IRouter, service *chat.Service) {
	group := router.Group("/api/v1")
	group.GET("/query_ai_agent_config_list", queryAgentConfigList(service))
	group.POST("/create_session", createSession(service))
	group.GET("/create_session", createSessionQuery(service))
	group.POST("/chat", chatMessage(service))
	group.POST("/chat_stream", chatStream(service))
}

func queryAgentConfigList(service *chat.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		agents := service.QueryAgentConfigList()
		responses := make([]dto.AiAgentConfigResponse, 0, len(agents))
		for _, agent := range agents {
			responses = append(responses, dto.AiAgentConfigResponse{
				AgentID:   agent.AgentID,
				AgentName: agent.AgentName,
				AgentDesc: agent.AgentDesc,
			})
		}
		c.JSON(http.StatusOK, response.Success(responses))
	}
}

func createSession(service *chat.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request dto.CreateSessionRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			writeError(c, types.NewAppError(types.CodeIllegalParameter, "invalid request JSON"))
			return
		}
		sessionID, err := service.CreateSession(request.AgentID, request.UserID)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, response.Success(dto.CreateSessionResponse{SessionID: sessionID}))
	}
}

func createSessionQuery(service *chat.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionID, err := service.CreateSession(c.Query("agentId"), c.Query("userId"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, response.Success(dto.CreateSessionResponse{SessionID: sessionID}))
	}
}

func chatMessage(service *chat.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request dto.ChatRequest
		if err := bindChatRequest(c, service, &request); err != nil {
			writeError(c, err)
			return
		}
		outputs, err := service.HandleMessage(c.Request.Context(), request.AgentID, request.UserID, request.SessionID, request.Message)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, response.Success(dto.ChatResponse{Content: strings.Join(outputs, "\n")}))
	}
}

func chatStream(service *chat.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var request dto.ChatRequest
		if err := bindChatRequest(c, service, &request); err != nil {
			writeError(c, err)
			return
		}
		outputs, errs := service.HandleMessageStream(c.Request.Context(), request.AgentID, request.UserID, request.SessionID, request.Message)
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.SSEvent("status", "running")
		c.Writer.Flush()
		for outputs != nil || errs != nil {
			select {
			case <-c.Request.Context().Done():
				return
			case output, ok := <-outputs:
				if !ok {
					outputs = nil
					continue
				}
				c.SSEvent("message", output)
				c.Writer.Flush()
			case err, ok := <-errs:
				if !ok {
					errs = nil
					continue
				}
				if err != nil {
					c.SSEvent("error", err.Error())
					c.Writer.Flush()
					return
				}
			}
		}
		c.SSEvent("done", "completed")
		c.Writer.Flush()
	}
}

func bindChatRequest(c *gin.Context, service *chat.Service, request *dto.ChatRequest) error {
	raw, err := execution.ReadBounded(c.Request.Body, service.MaxRequestBytes())
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, request); err != nil {
		return types.NewAppError(types.CodeIllegalParameter, "invalid request JSON")
	}
	if strings.TrimSpace(request.AgentID) == "" || strings.TrimSpace(request.UserID) == "" || strings.TrimSpace(request.Message) == "" {
		return types.NewAppError(types.CodeIllegalParameter, "agentId, userId and message are required")
	}
	return nil
}

func writeError(c *gin.Context, err error) {
	status := 0
	switch {
	case errors.Is(err, execution.ErrBusy):
		status = http.StatusTooManyRequests
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		status = http.StatusRequestTimeout
	case errors.Is(err, execution.ErrSize):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, execution.ErrBudget):
		status = http.StatusUnprocessableEntity
	}
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		status = http.StatusRequestEntityTooLarge
	}
	if status != 0 {
		c.JSON(status, response.Failure(types.CodeUnknownError, err.Error()))
		return
	}
	var appErr *types.AppError
	if errors.As(err, &appErr) {
		status := http.StatusBadRequest
		if appErr.Code == types.CodeAgentNotFound {
			status = http.StatusNotFound
		}
		c.JSON(status, response.Failure(appErr.Code, appErr.Info))
		return
	}
	c.JSON(http.StatusInternalServerError, response.Failure(types.CodeUnknownError, err.Error()))
}
