package armory

import (
	"ai-agent-scaffold-go/internal/domain/agent/model"
	"ai-agent-scaffold-go/internal/domain/shared/tree"
)

type Handler = tree.Handler[model.ArmoryCommand, *DynamicContext, model.RegisteredAgent]
