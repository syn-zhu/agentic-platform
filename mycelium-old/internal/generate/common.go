package generate

import "strings"

// ProjectLabels returns labels indicating ownership by a Project.
func ProjectLabels(projectName string) map[string]string {
	return map[string]string{"mycelium.io/project": projectName}
}

// ToolLabels returns labels indicating ownership by a Tool.
func ToolLabels(toolName string) map[string]string {
	return map[string]string{"mycelium.io/tool": toolName}
}

// AgentLabels returns labels indicating ownership by an Agent.
func AgentLabels(agentName string) map[string]string {
	return map[string]string{"mycelium.io/agent": agentName}
}

// MCPToolName converts a K8s resource name to an MCP tool name by replacing
// hyphens with underscores. K8s names use hyphens (DNS-safe), MCP names use
// underscores (Python convention). The Mycelium API layer does the reverse
// conversion when creating Tool resources from user-provided names.
func MCPToolName(resourceName string) string {
	return strings.ReplaceAll(resourceName, "-", "_")
}
