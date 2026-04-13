package generate

import "strings"

// TODO(mycelium): Make this configurable — should use the Helm release name
// or the actual controller deployment name rather than a hardcoded string.
const ManagedBy = "mycelium-controller"

// ManagedLabels returns the standard labels applied to all generated resources.
func ManagedLabels() map[string]string {
	return map[string]string{"app.kubernetes.io/managed-by": ManagedBy}
}

// ProjectAnnotations returns annotations indicating ownership by a Project.
func ProjectAnnotations(projectName string) map[string]string {
	return map[string]string{"mycelium.io/project": projectName}
}

// ToolAnnotations returns annotations indicating ownership by a Tool.
func ToolAnnotations(toolName string) map[string]string {
	return map[string]string{"mycelium.io/tool": toolName}
}

// AgentAnnotations returns annotations indicating ownership by an Agent.
func AgentAnnotations(agentName string) map[string]string {
	return map[string]string{"mycelium.io/agent": agentName}
}

// MCPToolName converts a K8s resource name to an MCP tool name by replacing
// hyphens with underscores. K8s names use hyphens (DNS-safe), MCP names use
// underscores (Python convention). The Mycelium API layer does the reverse
// conversion when creating Tool resources from user-provided names.
func MCPToolName(resourceName string) string {
	return strings.ReplaceAll(resourceName, "-", "_")
}
