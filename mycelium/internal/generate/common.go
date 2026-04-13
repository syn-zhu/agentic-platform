package generate

import v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"

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
