package generate_test

import (
	"testing"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/mongodb/mycelium/internal/generate"

	agwv1alpha1 "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"
	"github.com/agentgateway/agentgateway/controller/api/v1alpha1/shared"
	corev1 "k8s.io/api/core/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testProject() *v1alpha1.Project {
	return &v1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "acme"},
		Spec: v1alpha1.ProjectSpec{
			UserVerifierURL: "https://app.acme.com/verify",
			IdentityProvider: v1alpha1.IdentityProviderConfig{
				Issuer:         "https://accounts.google.com",
				Audiences:      []string{"mycelium-tenant-a"},
				AllowedClients: []string{"client-id-1"},
				AllowedScopes:  []string{"openid", "profile"},
			},
		},
	}
}

func TestMCPBackend(t *testing.T) {
	backend := generate.MCPBackend(testProject())

	assert.Equal(t, "mycelium-engine", backend.Name)
	assert.Equal(t, "acme", backend.Namespace)
	assert.Equal(t, "agentgateway.dev/v1alpha1", backend.APIVersion)
	assert.Equal(t, "AgentgatewayBackend", backend.Kind)
	assert.Equal(t, "mycelium-controller", backend.Labels["app.kubernetes.io/managed-by"])

	require.NotNil(t, backend.Spec.MCP)
	require.Len(t, backend.Spec.MCP.Targets, 1)
	assert.Equal(t, "mycelium-engine", string(backend.Spec.MCP.Targets[0].Name))
	require.NotNil(t, backend.Spec.MCP.Targets[0].Static)
	assert.Equal(t, "mycelium-engine", string(backend.Spec.MCP.Targets[0].Static.BackendRef.Name))
	assert.Equal(t, int32(8080), backend.Spec.MCP.Targets[0].Static.Port)
}

func TestMCPRoute(t *testing.T) {
	route := generate.MCPRoute(testProject())

	assert.Equal(t, "mcp-route", route.Name)
	assert.Equal(t, "acme", route.Namespace)
	assert.Equal(t, "gateway.networking.k8s.io/v1", route.APIVersion)
	assert.Equal(t, "HTTPRoute", route.Kind)
	assert.Equal(t, "mycelium-controller", route.Labels["app.kubernetes.io/managed-by"])

	require.Len(t, route.Spec.ParentRefs, 1)
	assert.Equal(t, "tenant-gateway", string(route.Spec.ParentRefs[0].Name))
	assert.Equal(t, "internal", string(*route.Spec.ParentRefs[0].SectionName))

	require.Len(t, route.Spec.Rules, 1)
	require.Len(t, route.Spec.Rules[0].Matches, 1)
	assert.Equal(t, "/mcp", *route.Spec.Rules[0].Matches[0].Path.Value)

	require.Len(t, route.Spec.Rules[0].BackendRefs, 1)
	assert.Equal(t, "mycelium-engine", string(route.Spec.Rules[0].BackendRefs[0].Name))
	assert.Equal(t, "agentgateway.dev", string(*route.Spec.Rules[0].BackendRefs[0].Group))
	assert.Equal(t, "AgentgatewayBackend", string(*route.Spec.Rules[0].BackendRefs[0].Kind))
}

func TestJWTPolicy(t *testing.T) {
	policy := generate.JWTPolicy(testProject())

	assert.Equal(t, "jwt-auth", policy.Name)
	assert.Equal(t, "acme", policy.Namespace)
	assert.Equal(t, "agentgateway.dev/v1alpha1", policy.APIVersion)
	assert.Equal(t, "AgentgatewayPolicy", policy.Kind)

	require.Len(t, policy.Spec.TargetRefs, 1)
	assert.Equal(t, "Gateway", string(policy.Spec.TargetRefs[0].Kind))
	assert.Equal(t, "tenant-gateway", string(policy.Spec.TargetRefs[0].Name))
	assert.Equal(t, "external", string(*policy.Spec.TargetRefs[0].SectionName))

	require.NotNil(t, policy.Spec.Traffic)
	require.NotNil(t, policy.Spec.Traffic.JWTAuthentication)
	assert.Equal(t, "Strict", string(policy.Spec.Traffic.JWTAuthentication.Mode))
	require.Len(t, policy.Spec.Traffic.JWTAuthentication.Providers, 1)
	assert.Equal(t, "https://accounts.google.com", string(policy.Spec.Traffic.JWTAuthentication.Providers[0].Issuer))
	assert.Equal(t, "mycelium-tenant-a", string(policy.Spec.Traffic.JWTAuthentication.Providers[0].Audiences[0]))
}

func TestSourceContextPolicy(t *testing.T) {
	policy := generate.SourceContextPolicy(testProject())

	assert.Equal(t, "internal-source-context", policy.Name)
	assert.Equal(t, "acme", policy.Namespace)
	assert.Equal(t, "AgentgatewayPolicy", policy.Kind)

	require.Len(t, policy.Spec.TargetRefs, 1)
	assert.Equal(t, "internal", string(*policy.Spec.TargetRefs[0].SectionName))

	require.NotNil(t, policy.Spec.Traffic)
	assert.Equal(t, "PreRouting", string(*policy.Spec.Traffic.Phase))
	require.NotNil(t, policy.Spec.Traffic.Transformation)
	require.NotNil(t, policy.Spec.Traffic.Transformation.Request)
	require.Len(t, policy.Spec.Traffic.Transformation.Request.Set, 2)
	assert.Equal(t, agwv1alpha1.HeaderName("X-Source-Pod-IP"), policy.Spec.Traffic.Transformation.Request.Set[0].Name)
	assert.Equal(t, shared.CELExpression("source.address"), policy.Spec.Traffic.Transformation.Request.Set[0].Value)
	assert.Equal(t, agwv1alpha1.HeaderName("X-Source-Service-Account"), policy.Spec.Traffic.Transformation.Request.Set[1].Name)
}

func TestToolAccessPolicy_WithAgentsAndTools(t *testing.T) {
	proj := testProject()
	agents := []v1alpha1.Agent{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "github-assistant", Namespace: "acme"},
			Spec: v1alpha1.AgentSpec{
				Description: "GH agent",
				Tools: []v1alpha1.ToolRef{
					{Ref: corev1.LocalObjectReference{Name: "list-repos"}},
					{Ref: corev1.LocalObjectReference{Name: "create-issue"}},
				},
				Container: v1alpha1.AgentContainer{Image: "img"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "multi-tool-agent", Namespace: "acme"},
			Spec: v1alpha1.AgentSpec{
				Description: "Multi agent",
				Tools: []v1alpha1.ToolRef{
					{Ref: corev1.LocalObjectReference{Name: "list-repos"}},
				},
				Container: v1alpha1.AgentContainer{Image: "img"},
			},
		},
	}
	policy := generate.ToolAccessPolicy(proj, agents)

	assert.Equal(t, "mcp-tool-access", policy.Name)
	assert.Equal(t, "acme", policy.Namespace)
	assert.Equal(t, "AgentgatewayPolicy", policy.Kind)

	require.Len(t, policy.Spec.TargetRefs, 1)
	assert.Equal(t, "AgentgatewayBackend", string(policy.Spec.TargetRefs[0].Kind))
	assert.Equal(t, "mycelium-engine", string(policy.Spec.TargetRefs[0].Name))

	require.NotNil(t, policy.Spec.Backend)
	require.NotNil(t, policy.Spec.Backend.MCP)
	require.NotNil(t, policy.Spec.Backend.MCP.Authorization)
	assert.Equal(t, shared.AuthorizationPolicyActionAllow, policy.Spec.Backend.MCP.Authorization.Action)

	exprs := policy.Spec.Backend.MCP.Authorization.Policy.MatchExpressions
	require.Len(t, exprs, 2)

	// Agents sorted alphabetically: github-assistant before multi-tool-agent
	// github-assistant has list_repos + create_issue (sorted: create_issue, list_repos)
	assert.Equal(t, shared.CELExpression(
		`source.workload.unverified.serviceAccount == "github-assistant" && mcp.tool.name in ["create_issue", "list_repos"]`,
	), exprs[0])

	// multi-tool-agent has only list_repos
	assert.Equal(t, shared.CELExpression(
		`source.workload.unverified.serviceAccount == "multi-tool-agent" && mcp.tool.name == "list_repos"`,
	), exprs[1])
}

func TestToolAccessPolicy_NoAgents_DenyAll(t *testing.T) {
	proj := testProject()

	policy := generate.ToolAccessPolicy(proj, nil)
	assert.Equal(t, shared.AuthorizationPolicyActionDeny, policy.Spec.Backend.MCP.Authorization.Action)
	assert.Equal(t, shared.CELExpression("true"), policy.Spec.Backend.MCP.Authorization.Policy.MatchExpressions[0])
}

func TestToolAccessPolicy_AgentsWithNoToolRefs_DenyAll(t *testing.T) {
	proj := testProject()
	agents := []v1alpha1.Agent{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "empty-agent", Namespace: "acme"},
			Spec: v1alpha1.AgentSpec{
				Description: "Agent with no tools",
				Container:   v1alpha1.AgentContainer{Image: "i"},
				Tools:       []v1alpha1.ToolRef{}, // no tool refs
			},
		},
	}
	policy := generate.ToolAccessPolicy(proj, agents)
	assert.Equal(t, shared.AuthorizationPolicyActionDeny, policy.Spec.Backend.MCP.Authorization.Action)
	assert.Equal(t, shared.CELExpression("true"), policy.Spec.Backend.MCP.Authorization.Policy.MatchExpressions[0])
}
