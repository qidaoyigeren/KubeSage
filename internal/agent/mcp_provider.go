package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"kubesage/internal/config"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// MCPProvider manages connections to configured MCP servers and exposes their
// tools as KubeSage Tool instances. Connection failures are non-fatal — the
// agent continues to work with its built-in tools.
type MCPProvider struct {
	clients map[string]*mcpClient
}

type mcpClient struct {
	client *client.Client
	cfg    config.MCPServerConfig
	tools  []mcp.Tool
}

// NewMCPProvider connects to every configured MCP server and discovers tools.
// Servers that fail to connect are logged but do not prevent startup.
func NewMCPProvider(cfg config.MCPConfig) (*MCPProvider, error) {
	if !cfg.Enabled || len(cfg.Servers) == 0 {
		return &MCPProvider{clients: map[string]*mcpClient{}}, nil
	}

	provider := &MCPProvider{clients: make(map[string]*mcpClient, len(cfg.Servers))}
	for _, serverCfg := range cfg.Servers {
		connected, err := provider.connect(serverCfg)
		if err != nil {
			// Non-fatal: the agent works without MCP tools.
			continue
		}
		provider.clients[serverCfg.Name] = connected
	}
	return provider, nil
}

func (p *MCPProvider) connect(cfg config.MCPServerConfig) (*mcpClient, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, fmt.Errorf("mcp server %s has no command", cfg.Name)
	}

	args := cfg.Args
	if args == nil {
		args = []string{}
	}
	env := cfg.Env
	if env == nil {
		env = []string{}
	}

	stdioClient, err := client.NewStdioMCPClient(cfg.Command, env, args...)
	if err != nil {
		return nil, fmt.Errorf("mcp server %s: stdio client: %w", cfg.Name, err)
	}

	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "kubesage-agent",
		Version: "1.0.0",
	}
	if _, err := stdioClient.Initialize(ctx, initReq); err != nil {
		_ = stdioClient.Close()
		return nil, fmt.Errorf("mcp server %s: initialize: %w", cfg.Name, err)
	}

	toolsResp, err := stdioClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		_ = stdioClient.Close()
		return nil, fmt.Errorf("mcp server %s: list tools: %w", cfg.Name, err)
	}

	return &mcpClient{
		client: stdioClient,
		cfg:    cfg,
		tools:  toolsResp.Tools,
	}, nil
}

// Tools returns all discovered MCP tools wrapped as KubeSage Tool instances.
// Tool names are prefixed with "mcp.<server_name>." to prevent collisions.
func (p *MCPProvider) Tools() []Tool {
	if p == nil {
		return nil
	}
	var tools []Tool
	for _, mc := range p.clients {
		for _, mcpTool := range mc.tools {
			tools = append(tools, p.wrapTool(mc, mcpTool))
		}
	}
	return tools
}

// ToolNames returns the names of all discovered MCP tools.
func (p *MCPProvider) ToolNames() []string {
	var names []string
	for _, tool := range p.Tools() {
		names = append(names, tool.Metadata().Name)
	}
	return names
}

// Count returns the total number of tools across all connected MCP servers.
func (p *MCPProvider) Count() int {
	if p == nil {
		return 0
	}
	count := 0
	for _, mc := range p.clients {
		count += len(mc.tools)
	}
	return count
}

// Close shuts down all MCP client connections.
func (p *MCPProvider) Close() {
	if p == nil {
		return
	}
	for _, mc := range p.clients {
		if mc.client != nil {
			_ = mc.client.Close()
		}
	}
}

func (p *MCPProvider) wrapTool(mc *mcpClient, tool mcp.Tool) Tool {
	fullName := "mcp." + mc.cfg.Name + "." + tool.Name
	desc := tool.Description
	if desc == "" {
		desc = fmt.Sprintf("MCP tool %s from server %s", tool.Name, mc.cfg.Name)
	}

	inputSchema := buildInputSchema(tool.InputSchema)

	return simpleTool{
		meta: ToolMetadata{
			Name:        fullName,
			Description: desc,
			InputSchema: inputSchema,
			RiskLevel:   "low",
			ReadOnly:    true,
			Timeout:     30 * time.Second,
			Critical:    false,
		},
		fn: func(ctx context.Context, input map[string]interface{}, state *ReadOnlyToolState) ToolResult {
			callReq := mcp.CallToolRequest{
				Request: mcp.Request{Method: "tools/call"},
			}
			callReq.Params.Name = tool.Name
			if input != nil {
				callReq.Params.Arguments = input
			} else {
				callReq.Params.Arguments = map[string]interface{}{}
			}

			result, err := mc.client.CallTool(ctx, callReq)
			if err != nil {
				return ToolResult{
					ToolName:    fullName,
					Success:     false,
					Error:       err.Error(),
					Observation: fmt.Sprintf("MCP tool %s failed: %s", fullName, err.Error()),
				}
			}

			observation := extractTextContent(result.Content)
			return ToolResult{
				ToolName:    fullName,
				Success:     true,
				Observation: observation,
				Data:        result,
			}
		},
	}
}

func buildInputSchema(schema mcp.ToolInputSchema) map[string]string {
	result := make(map[string]string)
	if schema.Properties == nil {
		return result
	}
	for name, prop := range schema.Properties {
		propMap, ok := prop.(map[string]interface{})
		if !ok {
			result[name] = "any"
			continue
		}
		propType, _ := propMap["type"].(string)
		if propType == "" {
			propType = "string"
		}
		if desc, ok := propMap["description"].(string); ok && desc != "" {
			propType = propType + ": " + desc
		}
		result[name] = propType
	}
	return result
}

func extractTextContent(content []mcp.Content) string {
	if len(content) == 0 {
		return "MCP tool returned empty response"
	}
	var parts []string
	for _, c := range content {
		switch ct := c.(type) {
		case mcp.TextContent:
			parts = append(parts, ct.Text)
		default:
			parts = append(parts, fmt.Sprintf("%v", ct))
		}
	}
	if len(parts) == 0 {
		return "MCP tool returned empty response"
	}
	return strings.Join(parts, "\n")
}
