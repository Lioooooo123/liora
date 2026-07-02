package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var listToolsTimeout = 2 * time.Second

func NewManager(config Config) *Manager {
	if config.Servers == nil {
		config.Servers = map[string]ServerConfig{}
	}
	clients := make(map[string]*Client, len(config.Servers))
	for name, server := range config.Servers {
		if !server.IsEnabled() {
			continue
		}
		clients[name] = NewClient(server)
	}
	return &Manager{config: config, clients: clients}
}

func (m *Manager) ListTools(ctx context.Context) ([]Tool, error) {
	tools, err := m.ListToolsDetailed(ctx)
	if len(tools) > 0 {
		return tools, nil
	}
	return tools, err
}

func (m *Manager) ListToolsDetailed(ctx context.Context) ([]Tool, error) {
	names := m.serverNames()
	results := make([]listToolsResult, len(names))
	var wg sync.WaitGroup
	for index, name := range names {
		wg.Add(1)
		go func(index int, name string) {
			defer wg.Done()
			results[index] = m.listOneServer(ctx, name)
		}(index, name)
	}
	wg.Wait()
	return collectListToolsResults(results)
}

func (m *Manager) Call(ctx context.Context, server string, tool string, args map[string]any) (string, error) {
	config, ok := m.config.Servers[server]
	if !ok {
		return "", fmt.Errorf("unknown MCP server %q", server)
	}
	if !config.IsEnabled() {
		return "", fmt.Errorf("MCP server %q is disabled", server)
	}
	return m.clientFor(server).CallTool(ctx, tool, args)
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, client := range m.clients {
		client.Close()
	}
	m.clients = map[string]*Client{}
}

func (m *Manager) serverNames() []string {
	names := make([]string, 0, len(m.config.Servers))
	for name, server := range m.config.Servers {
		if !server.IsEnabled() {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type listToolsResult struct {
	tools []Tool
	err   error
}

func (m *Manager) listOneServer(ctx context.Context, name string) listToolsResult {
	serverCtx, cancel := context.WithTimeout(ctx, listToolsTimeout)
	defer cancel()
	serverTools, err := m.clientFor(name).ListTools(serverCtx)
	if err != nil {
		return listToolsResult{err: fmt.Errorf("%s: %w", name, err)}
	}
	for i := range serverTools {
		serverTools[i].Server = name
	}
	return listToolsResult{tools: serverTools}
}

func (m *Manager) clientFor(name string) *Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	client, ok := m.clients[name]
	if !ok {
		client = NewClient(m.config.Servers[name])
		m.clients[name] = client
	}
	return client
}

func collectListToolsResults(results []listToolsResult) ([]Tool, error) {
	var tools []Tool
	var errs []error
	for _, result := range results {
		if result.err != nil {
			errs = append(errs, result.err)
			continue
		}
		tools = append(tools, result.tools...)
	}
	sort.SliceStable(tools, func(i, j int) bool {
		if tools[i].Server == tools[j].Server {
			return tools[i].Name < tools[j].Name
		}
		return tools[i].Server < tools[j].Server
	})
	if len(tools) == 0 && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return tools, errors.Join(errs...)
}
