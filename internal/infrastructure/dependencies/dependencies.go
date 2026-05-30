// Package dependencies anchors verified runtime dependencies while adapters are added.
package dependencies

import (
	_ "github.com/cloudwego/eino/adk"
	_ "github.com/cloudwego/eino/components/model"
	_ "github.com/cloudwego/eino/components/tool"
	_ "github.com/gin-gonic/gin"
	_ "github.com/redis/go-redis/v9"
	_ "go.uber.org/zap"
	_ "google.golang.org/adk/agent"
	_ "google.golang.org/adk/agent/llmagent"
	_ "google.golang.org/adk/agent/workflowagents/loopagent"
	_ "google.golang.org/adk/agent/workflowagents/parallelagent"
	_ "google.golang.org/adk/agent/workflowagents/sequentialagent"
	_ "google.golang.org/adk/runner"
	_ "gopkg.in/yaml.v3"
	_ "gorm.io/driver/mysql"
	_ "gorm.io/gorm"
)
