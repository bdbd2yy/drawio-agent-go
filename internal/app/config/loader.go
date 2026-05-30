package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"ai-agent-scaffold-go/internal/domain/agent/model"
	"gopkg.in/yaml.v3"
)

type agentRoot struct {
	AI struct {
		Agent struct {
			Config struct {
				Tables map[string]model.AiAgentConfigTable `yaml:"tables"`
			} `yaml:"config"`
		} `yaml:"agent"`
	} `yaml:"ai"`
}

func LoadAgentTables(data []byte) (map[string]model.AiAgentConfigTable, error) {
	expanded := expandEnvPlaceholders(string(data))
	var root agentRoot
	if err := yaml.Unmarshal([]byte(expanded), &root); err != nil {
		return nil, fmt.Errorf("parse agent config: %w", err)
	}

	tables := root.AI.Agent.Config.Tables
	if len(tables) == 0 {
		return nil, fmt.Errorf("agent config tables are required")
	}

	for name, table := range tables {
		normalizeDefaults(&table)
		if err := validateTable(name, table); err != nil {
			return nil, err
		}
		tables[name] = table
	}

	return tables, nil
}

func LoadAgentTablesFile(path string) (map[string]model.AiAgentConfigTable, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read agent config %s: %w", path, err)
	}
	return LoadAgentTables(data)
}

var envPlaceholderRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// expandEnvPlaceholders replaces ${VAR} and ${VAR:-default} in YAML text using
// the current process environment. It deliberately ignores bare $NAME forms so
// dollar signs that happen to appear inside instructions or URLs are not
// mangled.
func expandEnvPlaceholders(input string) string {
	return envPlaceholderRE.ReplaceAllStringFunc(input, func(match string) string {
		groups := envPlaceholderRE.FindStringSubmatch(match)
		name := groups[1]
		if value, ok := os.LookupEnv(name); ok && value != "" {
			return value
		}
		if len(groups) > 2 {
			return groups[2]
		}
		return ""
	})
}

func normalizeDefaults(table *model.AiAgentConfigTable) {
	if table.Module.AiAPI.CompletionsPath == "" {
		table.Module.AiAPI.CompletionsPath = "v1/chat/completions"
	}
	if table.Module.AiAPI.EmbeddingsPath == "" {
		table.Module.AiAPI.EmbeddingsPath = "v1/embeddings"
	}
	for i := range table.Module.ChatModel.ToolSkillsList {
		if table.Module.ChatModel.ToolSkillsList[i].Type == "" {
			table.Module.ChatModel.ToolSkillsList[i].Type = "directory"
		}
	}
	for i := range table.Module.AgentWorkflows {
		if table.Module.AgentWorkflows[i].MaxIterations == 0 {
			table.Module.AgentWorkflows[i].MaxIterations = 3
		}
	}
}

func validateTable(name string, table model.AiAgentConfigTable) error {
	prefix := "agent table " + name
	required := map[string]string{
		"app-name":                 table.AppName,
		"agent.agent-id":           table.Agent.AgentID,
		"module.ai-api.base-url":   table.Module.AiAPI.BaseURL,
		"module.ai-api.api-key":    table.Module.AiAPI.APIKey,
		"module.chat-model.model":  table.Module.ChatModel.Model,
		"module.runner.agent-name": table.Module.Runner.AgentName,
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s: %s is required", prefix, field)
		}
	}
	if len(table.Module.Agents) == 0 {
		return fmt.Errorf("%s: module.agents is required", prefix)
	}
	for i, agent := range table.Module.Agents {
		if strings.TrimSpace(agent.Name) == "" {
			return fmt.Errorf("%s: module.agents[%d].name is required", prefix, i)
		}
		if strings.TrimSpace(agent.Instruction) == "" {
			return fmt.Errorf("%s: module.agents[%d].instruction is required", prefix, i)
		}
	}
	for i, workflow := range table.Module.AgentWorkflows {
		switch workflow.Type {
		case model.WorkflowTypeLoop, model.WorkflowTypeParallel, model.WorkflowTypeSequential:
		default:
			return fmt.Errorf("%s: module.agent-workflows[%d].type is invalid: %s", prefix, i, workflow.Type)
		}
		if strings.TrimSpace(workflow.Name) == "" {
			return fmt.Errorf("%s: module.agent-workflows[%d].name is required", prefix, i)
		}
	}
	for i, tool := range table.Module.ChatModel.ToolMCPList {
		if err := validateMCPEntry(prefix, i, tool); err != nil {
			return err
		}
	}
	return nil
}

func validateMCPEntry(prefix string, index int, tool model.ToolMCPConfig) error {
	fieldPrefix := fmt.Sprintf("%s: module.chat-model.tool-mcp-list[%d]", prefix, index)
	count := 0
	if tool.Local != nil {
		count++
	}
	if tool.SSE != nil {
		count++
	}
	if tool.Stdio != nil {
		count++
	}

	switch count {
	case 0:
		return fmt.Errorf("%s must define exactly one of local, sse, or stdio", fieldPrefix)
	case 1:
	default:
		return fmt.Errorf("%s cannot define multiple transport types", fieldPrefix)
	}

	if tool.Local != nil {
		if strings.TrimSpace(tool.Local.Name) == "" {
			return fmt.Errorf("%s.local.name is required", fieldPrefix)
		}
	}
	if tool.SSE != nil {
		if strings.TrimSpace(tool.SSE.Name) == "" {
			return fmt.Errorf("%s.sse.name is required", fieldPrefix)
		}
		if strings.TrimSpace(tool.SSE.BaseURI) == "" {
			return fmt.Errorf("%s.sse.base-uri is required", fieldPrefix)
		}
		if tool.SSE.RequestTimeout < 0 {
			return fmt.Errorf("%s.sse.request-timeout must be positive", fieldPrefix)
		}
	}
	if tool.Stdio != nil {
		if strings.TrimSpace(tool.Stdio.Name) == "" {
			return fmt.Errorf("%s.stdio.name is required", fieldPrefix)
		}
		if strings.TrimSpace(tool.Stdio.ServerParameters.Command) == "" {
			return fmt.Errorf("%s.stdio.server-parameters.command is required", fieldPrefix)
		}
		if tool.Stdio.RequestTimeout < 0 {
			return fmt.Errorf("%s.stdio.request-timeout must be positive", fieldPrefix)
		}
	}
	return nil
}
