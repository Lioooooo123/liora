package daemon

import (
	"context"
	"sort"
	"strings"

	"github.com/Lioooooo123/liora/internal/capabilities"
	mcppkg "github.com/Lioooooo123/liora/internal/mcp"
	"github.com/Lioooooo123/liora/internal/store"
)

func (s *server) mcpCapabilities(ctx context.Context) ([]capabilities.MCPToolSpec, []capabilities.MCPServerStatus, error) {
	if s.store == nil {
		return nil, nil, nil
	}
	config, err := s.store.LoadMCPConfig()
	if err != nil {
		return nil, nil, err
	}
	if len(config.Servers) == 0 {
		return nil, nil, nil
	}
	statuses := mcpStatusFromConfig(config)
	servers := make(map[string]mcppkg.ServerConfig, len(config.Servers))
	for name, server := range config.Servers {
		servers[name] = mcppkg.ServerConfig{
			Command:     server.Command,
			Args:        server.Args,
			Env:         server.Env,
			Enabled:     server.Enabled,
			Source:      server.Source,
			Version:     server.Version,
			Permissions: server.Permissions,
		}
	}
	manager := mcppkg.NewManager(mcppkg.Config{Servers: servers})
	defer manager.Close()
	tools, err := manager.ListToolsDetailed(ctx)
	specs := make([]capabilities.MCPToolSpec, 0, len(tools))
	for _, tool := range tools {
		server := config.Servers[tool.Server]
		specs = append(specs, capabilities.MCPToolSpec{
			Server:      tool.Server,
			Name:        tool.Name,
			Usage:       "mcp " + tool.Server + " " + tool.Name + " <json arguments>",
			Description: tool.Description,
			Kind:        capabilities.ToolExternal,
			InputSchema: tool.InputSchema,
			Permissions: append([]string(nil), server.Permissions...),
		})
	}
	applyMCPToolCounts(statuses, tools)
	if err != nil {
		applyMCPError(statuses, err.Error())
	}
	return specs, statuses, err
}

func mcpStatusFromConfig(config store.MCPConfig) []capabilities.MCPServerStatus {
	names := make([]string, 0, len(config.Servers))
	for name := range config.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	statuses := make([]capabilities.MCPServerStatus, 0, len(names))
	for _, name := range names {
		server := config.Servers[name]
		statuses = append(statuses, capabilities.MCPServerStatus{
			Name:        name,
			Enabled:     server.Enabled == nil || *server.Enabled,
			Source:      strings.TrimSpace(server.Source),
			Version:     strings.TrimSpace(server.Version),
			Permissions: append([]string(nil), server.Permissions...),
			Auth:        "not_probed",
		})
	}
	return statuses
}

func applyMCPToolCounts(statuses []capabilities.MCPServerStatus, tools []mcppkg.Tool) {
	counts := map[string]int{}
	for _, tool := range tools {
		counts[tool.Server]++
	}
	for i := range statuses {
		statuses[i].ToolCount = counts[statuses[i].Name]
	}
}

func applyMCPError(statuses []capabilities.MCPServerStatus, message string) {
	for i := range statuses {
		if !statuses[i].Enabled || !strings.Contains(message, statuses[i].Name) {
			continue
		}
		statuses[i].LastError = message
	}
}
