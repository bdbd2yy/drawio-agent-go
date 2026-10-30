// Package bootstrap wires application config, armory assembly, chat service,
// and Gin HTTP routes into a single runnable engine.
package bootstrap

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"ai-agent-scaffold-go/internal/app/config"
	"ai-agent-scaffold-go/internal/domain/agent/model"
	"ai-agent-scaffold-go/internal/domain/agent/ports"
	"ai-agent-scaffold-go/internal/domain/agent/service/armory"
	"ai-agent-scaffold-go/internal/domain/agent/service/armory/factory"
	"ai-agent-scaffold-go/internal/domain/agent/service/chat"
	"ai-agent-scaffold-go/internal/infrastructure/adk"
	"ai-agent-scaffold-go/internal/infrastructure/ai"
	httptrigger "ai-agent-scaffold-go/internal/trigger/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type Engine struct {
	Application config.Application
	Router      *gin.Engine
	ChatService *chat.Service
	Registry    ports.AgentRegistry
	Sessions    ports.SessionStore
	Logger      *zap.Logger
}

func Build(ctx context.Context, appCfg config.Application, logger *zap.Logger) (*Engine, error) {
	if err := appCfg.Runtime.Validate(); err != nil {
		return nil, err
	}
	tables, err := loadAgentTables(appCfg.Agent.ConfigPaths)
	if err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("no agent tables loaded; configure agent.config-paths")
	}

	llmTimeout, err := appCfg.LLM.RequestTimeoutDuration()
	if err != nil {
		return nil, err
	}

	registry := ports.NewInMemoryAgentRegistry()
	sessions := ports.NewInMemorySessionStore()

	modelProvider := ai.NewEinoProvider().WithRequestTimeout(llmTimeout)
	toolRouter := ai.NewMCPToolRouter()
	mcpFactory := ai.NewToolFactory(toolRouter)
	skillFactory := ai.NewSkillFactory()
	agentFactory := adk.NewFactory()
	agentFactory.UseToolRouter(toolRouter)
	runnerFactory := agentFactory

	armoryFactory := factory.NewDefaultFactory(modelProvider, mcpFactory, skillFactory, agentFactory, runnerFactory, registry)
	armoryService := armory.NewService(armoryFactory.ArmoryStrategyHandler())

	if err := armoryService.AcceptArmoryAgents(ctx, tables); err != nil {
		return nil, fmt.Errorf("armory assembly: %w", err)
	}

	chatService := chat.NewService(registry, sessions).WithOptions(appCfg.Runtime)

	router := newRouter(appCfg.App.Env)
	httptrigger.RegisterAgentRoutes(router, chatService)

	if logger != nil {
		logger.Info("agents registered", zap.Int("count", len(registry.List())), zap.Duration("llm_request_timeout", llmTimeout))
	}

	return &Engine{
		Application: appCfg,
		Router:      router,
		ChatService: chatService,
		Registry:    registry,
		Sessions:    sessions,
		Logger:      logger,
	}, nil
}

func (e *Engine) Run() error {
	addr := e.Application.Server.Addr
	if addr == "" {
		addr = ":8091"
	}
	if e.Logger != nil {
		e.Logger.Info("http server listening", zap.String("addr", addr))
	}
	return e.Router.Run(addr)
}

func newRouter(env string) *gin.Engine {
	if env == "prod" || env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.New()
	router.Use(gin.Recovery())
	if env != "prod" && env != "production" {
		router.Use(gin.Logger())
	}
	router.Use(corsMiddleware())
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	return router
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		} else {
			c.Header("Access-Control-Allow-Origin", "*")
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		c.Header("Access-Control-Allow-Credentials", "true")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func loadAgentTables(paths []string) (map[string]model.AiAgentConfigTable, error) {
	merged := make(map[string]model.AiAgentConfigTable)
	for _, raw := range paths {
		path := strings.TrimSpace(raw)
		if path == "" {
			continue
		}
		expanded := os.ExpandEnv(path)
		tables, err := config.LoadAgentTablesFile(expanded)
		if err != nil {
			return nil, err
		}
		for name, table := range tables {
			merged[name] = table
		}
	}
	return merged, nil
}
