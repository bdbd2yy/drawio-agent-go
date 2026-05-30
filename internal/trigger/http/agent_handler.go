package http

import (
	"errors"
	"net/http"
	"strings"

	"ai-agent-scaffold-go/internal/api/dto"
	"ai-agent-scaffold-go/internal/api/response"
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
			writeError(c, types.NewAppError(types.CodeIllegalParameter, err.Error()))
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
		if err := c.ShouldBindJSON(&request); err != nil {
			writeError(c, types.NewAppError(types.CodeIllegalParameter, err.Error()))
			return
		}
		outputs, err := service.HandleMessage(request.AgentID, request.UserID, request.SessionID, request.Message)
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
		if err := c.ShouldBindJSON(&request); err != nil {
			writeError(c, types.NewAppError(types.CodeIllegalParameter, err.Error()))
			return
		}
		outputs, errs := service.HandleMessageStream(request.AgentID, request.UserID, request.SessionID, request.Message)
		c.Header("Content-Type", "text/event-stream")
		for output := range outputs {
			c.SSEvent("message", output)
			c.Writer.Flush()
		}
		if err, ok := <-errs; ok && err != nil {
			c.SSEvent("error", err.Error())
			c.Writer.Flush()
		}
	}
}

func writeError(c *gin.Context, err error) {
	var appErr *types.AppError
	if errors.As(err, &appErr) {
		c.JSON(http.StatusOK, response.Failure(appErr.Code, appErr.Info))
		return
	}
	c.JSON(http.StatusOK, response.Failure(types.CodeUnknownError, err.Error()))
}
