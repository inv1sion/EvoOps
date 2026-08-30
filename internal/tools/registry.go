package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/cloudwego/eino-ext/components/tool/mcp/officialmcp"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Registry struct {
	mu       sync.RWMutex
	tools    map[string]einotool.BaseTool
	sessions []*mcp.ClientSession
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]einotool.BaseTool)}
}

func (r *Registry) Register(ctx context.Context, candidate einotool.BaseTool) error {
	info, err := candidate.Info(ctx)
	if err != nil {
		return fmt.Errorf("read tool info: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[info.Name]; exists {
		return fmt.Errorf("tool %q already registered", info.Name)
	}
	r.tools[info.Name] = candidate
	return nil
}

func (r *Registry) Invoke(ctx context.Context, name string, arguments map[string]any, target any) (string, error) {
	r.mu.RLock()
	base, ok := r.tools[name]
	r.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("tool %q not found", name)
	}
	invokable, ok := base.(einotool.InvokableTool)
	if !ok {
		return "", fmt.Errorf("tool %q is not invokable", name)
	}
	payload, err := json.Marshal(arguments)
	if err != nil {
		return "", fmt.Errorf("encode arguments for %s: %w", name, err)
	}
	result, err := invokable.InvokableRun(ctx, string(payload))
	if err != nil {
		return "", err
	}
	if target != nil {
		if err := json.Unmarshal([]byte(result), target); err != nil {
			return result, fmt.Errorf("decode result from %s: %w", name, err)
		}
	}
	return result, nil
}

func (r *Registry) Infos(ctx context.Context) ([]*schema.ToolInfo, error) {
	r.mu.RLock()
	items := make([]einotool.BaseTool, 0, len(r.tools))
	for _, item := range r.tools {
		items = append(items, item)
	}
	r.mu.RUnlock()
	result := make([]*schema.ToolInfo, 0, len(items))
	for _, item := range items {
		info, err := item.Info(ctx)
		if err != nil {
			return nil, err
		}
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// ConnectMCP discovers tools from a Model Context Protocol SSE server and exposes them through
// the same Eino registry as local tools. An allowlist is strongly recommended
// in production; an empty list imports every tool advertised by the server.
func (r *Registry) ConnectMCP(ctx context.Context, endpoint string, allowlist []string) error {
	client := mcp.NewClient(&mcp.Implementation{Name: "evoops", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.SSEClientTransport{Endpoint: endpoint}, nil)
	if err != nil {
		return fmt.Errorf("connect MCP server %s: %w", endpoint, err)
	}
	discovered, err := officialmcp.GetTools(ctx, &officialmcp.Config{
		Cli:          session,
		ToolNameList: allowlist,
	})
	if err != nil {
		session.Close()
		return fmt.Errorf("discover MCP tools from %s: %w", endpoint, err)
	}
	for _, candidate := range discovered {
		if err := r.Register(ctx, candidate); err != nil {
			session.Close()
			return err
		}
	}
	r.mu.Lock()
	r.sessions = append(r.sessions, session)
	r.mu.Unlock()
	return nil
}

func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var first error
	for _, session := range r.sessions {
		if err := session.Close(); err != nil && first == nil {
			first = err
		}
	}
	r.sessions = nil
	return first
}
