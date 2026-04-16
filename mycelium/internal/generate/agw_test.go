package generate_test

import (
	"testing"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/internal/generate"

	agwv1alpha1 "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"
	agwshared "github.com/agentgateway/agentgateway/controller/api/v1alpha1/shared"
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

	assert.Equal(t, "mycelium-engine", *backend.GetName())
	assert.Equal(t, "acme", *backend.GetNamespace())
	assert.Equal(t, "agentgateway.dev/v1alpha1", *backend.GetAPIVersion())
	assert.Equal(t, "AgentgatewayBackend", *backend.GetKind())
	assert.Equal(t, "mycelium-controller", backend.Labels["app.kubernetes.io/managed-by"])

	require.NotNil(t, backend.Spec)
	require.NotNil(t, backend.Spec.MCP)
	require.Len(t, backend.Spec.MCP.Targets, 1)
	assert.Equal(t, "mycelium-engine", string(*backend.Spec.MCP.Targets[0].Name))
	require.NotNil(t, backend.Spec.MCP.Targets[0].Static)
	assert.Equal(t, "mycelium-engine", backend.Spec.MCP.Targets[0].Static.BackendRef.Name)
	assert.Equal(t, int32(8080), *backend.Spec.MCP.Targets[0].Static.Port)
}

func TestMCPRoute(t *testing.T) {
	route := generate.MCPRoute(testProject())

	assert.Equal(t, "mcp-route", *route.GetName())
	assert.Equal(t, "acme", *route.GetNamespace())
	assert.Equal(t, "gateway.networking.k8s.io/v1", *route.GetAPIVersion())
	assert.Equal(t, "HTTPRoute", *route.GetKind())
	assert.Equal(t, "mycelium-controller", route.Labels["app.kubernetes.io/managed-by"])

	require.NotNil(t, route.Spec)
	require.Len(t, route.Spec.ParentRefs, 1)
	assert.Equal(t, "tenant-gateway", string(*route.Spec.ParentRefs[0].Name))
	assert.Equal(t, "internal", string(*route.Spec.ParentRefs[0].SectionName))

	require.Len(t, route.Spec.Rules, 1)
	require.Len(t, route.Spec.Rules[0].Matches, 1)
	assert.Equal(t, "/mcp", *route.Spec.Rules[0].Matches[0].Path.Value)

	require.Len(t, route.Spec.Rules[0].BackendRefs, 1)
	assert.Equal(t, "mycelium-engine", string(*route.Spec.Rules[0].BackendRefs[0].Name))
	assert.Equal(t, "agentgateway.dev", string(*route.Spec.Rules[0].BackendRefs[0].Group))
	assert.Equal(t, "AgentgatewayBackend", string(*route.Spec.Rules[0].BackendRefs[0].Kind))
}

func TestJWTPolicy(t *testing.T) {
	policy := generate.JWTPolicy(testProject())

	assert.Equal(t, "jwt-auth", *policy.GetName())
	assert.Equal(t, "acme", *policy.GetNamespace())
	assert.Equal(t, "agentgateway.dev/v1alpha1", *policy.GetAPIVersion())
	assert.Equal(t, "AgentgatewayPolicy", *policy.GetKind())

	require.NotNil(t, policy.Spec)
	require.Len(t, policy.Spec.TargetRefs, 1)
	assert.Equal(t, "Gateway", string(*policy.Spec.TargetRefs[0].Kind))
	assert.Equal(t, "tenant-gateway", string(*policy.Spec.TargetRefs[0].Name))
	assert.Equal(t, "external", string(*policy.Spec.TargetRefs[0].SectionName))

	require.NotNil(t, policy.Spec.Traffic)
	require.NotNil(t, policy.Spec.Traffic.JWTAuthentication)
	assert.Equal(t, agwv1alpha1.JWTAuthenticationModeStrict, *policy.Spec.Traffic.JWTAuthentication.Mode)
	require.Len(t, policy.Spec.Traffic.JWTAuthentication.Providers, 1)
	assert.Equal(t, "https://accounts.google.com", *policy.Spec.Traffic.JWTAuthentication.Providers[0].Issuer)
	assert.Equal(t, "mycelium-tenant-a", policy.Spec.Traffic.JWTAuthentication.Providers[0].Audiences[0])
}

func TestSourceContextPolicy(t *testing.T) {
	policy := generate.SourceContextPolicy(testProject())

	assert.Equal(t, "internal-source-context", *policy.GetName())
	assert.Equal(t, "acme", *policy.GetNamespace())
	assert.Equal(t, "AgentgatewayPolicy", *policy.GetKind())

	require.NotNil(t, policy.Spec)
	require.Len(t, policy.Spec.TargetRefs, 1)
	assert.Equal(t, "internal", string(*policy.Spec.TargetRefs[0].SectionName))

	require.NotNil(t, policy.Spec.Traffic)
	assert.Equal(t, agwv1alpha1.PolicyPhasePreRouting, *policy.Spec.Traffic.Phase)
	require.NotNil(t, policy.Spec.Traffic.Transformation)
	require.NotNil(t, policy.Spec.Traffic.Transformation.Request)
	require.Len(t, policy.Spec.Traffic.Transformation.Request.Set, 2)
	assert.Equal(t, agwv1alpha1.HeaderName("X-Source-Pod-IP"), *policy.Spec.Traffic.Transformation.Request.Set[0].Name)
	assert.Equal(t, agwshared.CELExpression("source.address"), *policy.Spec.Traffic.Transformation.Request.Set[0].Value)
	assert.Equal(t, agwv1alpha1.HeaderName("X-Source-Service-Account"), *policy.Spec.Traffic.Transformation.Request.Set[1].Name)
}

func TestToolAccessPolicy_WithAgentsAndTools(t *testing.T) {
	proj := testProject()
	agents := []v1alpha1.Agent{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "github-assistant", Namespace: "acme"},
			Spec: v1alpha1.AgentSpec{
				Description: "GH agent",
				ToolBindings: []v1alpha1.ToolBinding{
					{Tool: v1alpha1.ToolRef{Name: "list-repos"}},
					{Tool: v1alpha1.ToolRef{Name: "create-issue"}},
				},
				Sandbox: v1alpha1.SandboxPoolConfig{Image: "img", ShutdownTimeout: "30m"},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "multi-tool-agent", Namespace: "acme"},
			Spec: v1alpha1.AgentSpec{
				Description: "Multi agent",
				ToolBindings: []v1alpha1.ToolBinding{
					{Tool: v1alpha1.ToolRef{Name: "list-repos"}},
				},
				Sandbox: v1alpha1.SandboxPoolConfig{Image: "img", ShutdownTimeout: "30m"},
			},
		},
	}
	policy := generate.ToolAccessPolicy(proj, agents)

	assert.Equal(t, "mcp-tool-access", *policy.GetName())
	assert.Equal(t, "acme", *policy.GetNamespace())
	assert.Equal(t, "AgentgatewayPolicy", *policy.GetKind())

	require.NotNil(t, policy.Spec)
	require.Len(t, policy.Spec.TargetRefs, 1)
	assert.Equal(t, "AgentgatewayBackend", string(*policy.Spec.TargetRefs[0].Kind))
	assert.Equal(t, "mycelium-engine", string(*policy.Spec.TargetRefs[0].Name))

	require.NotNil(t, policy.Spec.Backend)
	require.NotNil(t, policy.Spec.Backend.MCP)
	require.NotNil(t, policy.Spec.Backend.MCP.Authorization)
	assert.Equal(t, agwshared.AuthorizationPolicyActionAllow, *policy.Spec.Backend.MCP.Authorization.Action)

	require.NotNil(t, policy.Spec.Backend.MCP.Authorization.Policy)
	exprs := policy.Spec.Backend.MCP.Authorization.Policy.MatchExpressions
	require.Len(t, exprs, 2)

	// Agents sorted alphabetically: github-assistant before multi-tool-agent
	// github-assistant has list_repos + create_issue (sorted: create_issue, list_repos)
	assert.Equal(t, agwshared.CELExpression(
		`source.workload.unverified.serviceAccount == "github-assistant" && mcp.tool.name in ["create_issue", "list_repos"]`,
	), exprs[0])

	// multi-tool-agent has only list_repos
	assert.Equal(t, agwshared.CELExpression(
		`source.workload.unverified.serviceAccount == "multi-tool-agent" && mcp.tool.name == "list_repos"`,
	), exprs[1])
}

func TestToolAccessPolicy_NoAgents_DenyAll(t *testing.T) {
	proj := testProject()

	policy := generate.ToolAccessPolicy(proj, nil)
	require.NotNil(t, policy.Spec.Backend.MCP.Authorization)
	assert.Equal(t, agwshared.AuthorizationPolicyActionDeny, *policy.Spec.Backend.MCP.Authorization.Action)
	require.NotNil(t, policy.Spec.Backend.MCP.Authorization.Policy)
	assert.Equal(t, agwshared.CELExpression("true"), policy.Spec.Backend.MCP.Authorization.Policy.MatchExpressions[0])
}

func TestToolAccessPolicy_AgentsWithNoToolRefs_DenyAll(t *testing.T) {
	proj := testProject()
	agents := []v1alpha1.Agent{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "empty-agent", Namespace: "acme"},
			Spec: v1alpha1.AgentSpec{
				Description:  "Agent with no tools",
				Sandbox:      v1alpha1.SandboxPoolConfig{Image: "i", ShutdownTimeout: "30m"},
				ToolBindings: []v1alpha1.ToolBinding{}, // no tool bindings
			},
		},
	}
	policy := generate.ToolAccessPolicy(proj, agents)
	require.NotNil(t, policy.Spec.Backend.MCP.Authorization)
	assert.Equal(t, agwshared.AuthorizationPolicyActionDeny, *policy.Spec.Backend.MCP.Authorization.Action)
	require.NotNil(t, policy.Spec.Backend.MCP.Authorization.Policy)
	assert.Equal(t, agwshared.CELExpression("true"), policy.Spec.Backend.MCP.Authorization.Policy.MatchExpressions[0])
}
