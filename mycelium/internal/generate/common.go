package generate

import (
	"strings"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
)

// TODO(mycelium): Make this configurable — should use the Helm release name
// or the actual controller deployment name rather than a hardcoded string.
const ManagedBy = "mycelium-controller"

// ManagedLabels returns the standard labels applied to all generated resources.
func ManagedLabels() map[string]string {
	return map[string]string{"app.kubernetes.io/managed-by": ManagedBy}
}

// ProjectNamespace returns the namespace name owned by a Project.
// By convention, the namespace name equals the Project name.
func ProjectNamespace(p *v1alpha1.Project) string {
	return p.Name
}

// MCPToolName converts a K8s resource name to an MCP tool name by replacing
// hyphens with underscores. K8s names use hyphens (DNS-safe), MCP names use
// underscores (Python convention). The Mycelium API layer does the reverse
// conversion when creating Tool resources from user-provided names.
func MCPToolName(resourceName string) string {
	return strings.ReplaceAll(resourceName, "-", "_")
}
