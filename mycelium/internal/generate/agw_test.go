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
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func testProject() *v1alpha1.MyceliumEcosystem {
	return &v1alpha1.MyceliumEcosystem{
		ObjectMeta: metav1.ObjectMeta{Name: "acme"},
		Spec: v1alpha1.MyceliumEcosystemSpec{
			UserVerifierEndpoint: "https://app.acme.com/verify",
			IdentityProviders: []v1alpha1.IdentityProviderConfig{
				{
					Name:           "google",
					Issuer:         "https://accounts.google.com",
					Audiences:      []string{"mycelium-tenant-a"},
					AllowedClients: []string{"client-id-1"},
					AllowedScopes:  []string{"openid", "profile"},
					JWKSEndpoint:   "https://www.googleapis.com/oauth2/v3/certs",
				},
			},
		},
	}
}

func TestMCPBackend(t *testing.T) {
	backend := generate.ToolServer(testProject())

	assert.Equal(t, "mycelium-engine", backend.Name)
	assert.Equal(t, "acme", backend.Namespace)
	assert.Equal(t, "agentgateway.dev/v1alpha1", backend.APIVersion)
	assert.Equal(t, "AgentgatewayBackend", backend.Kind)

	require.NotNil(t, backend.Spec.MCP)
	require.Len(t, backend.Spec.MCP.Targets, 1)
	assert.Equal(t, gwv1.SectionName("mycelium-engine"), backend.Spec.MCP.Targets[0].Name)
	require.NotNil(t, backend.Spec.MCP.Targets[0].Static)
	require.NotNil(t, backend.Spec.MCP.Targets[0].Static.Host)
	assert.Equal(t, "mycelium-engine", *backend.Spec.MCP.Targets[0].Static.Host)
	assert.Equal(t, int32(8080), backend.Spec.MCP.Targets[0].Static.Port)
}

func TestMCPRoute(t *testing.T) {
	route := generate.ToolServerRoute(testProject())

	assert.Equal(t, "mcp-route", route.Name)
	assert.Equal(t, "acme", route.Namespace)
	assert.Equal(t, "gateway.networking.k8s.io/v1", route.APIVersion)
	assert.Equal(t, "HTTPRoute", route.Kind)

	require.Len(t, route.Spec.ParentRefs, 1)
	assert.Equal(t, gwv1.ObjectName("tenant-gateway"), route.Spec.ParentRefs[0].Name)
	assert.Equal(t, gwv1.SectionName("internal"), *route.Spec.ParentRefs[0].SectionName)

	require.Len(t, route.Spec.Rules, 1)
	require.Len(t, route.Spec.Rules[0].Matches, 1)
	assert.Equal(t, "/mcp", *route.Spec.Rules[0].Matches[0].Path.Value)

	require.Len(t, route.Spec.Rules[0].BackendRefs, 1)
	assert.Equal(t, gwv1.ObjectName("mycelium-engine"), route.Spec.Rules[0].BackendRefs[0].Name)
	assert.Equal(t, gwv1.Group("agentgateway.dev"), *route.Spec.Rules[0].BackendRefs[0].Group)
	assert.Equal(t, gwv1.Kind("AgentgatewayBackend"), *route.Spec.Rules[0].BackendRefs[0].Kind)
}

func TestJWTPolicy(t *testing.T) {
	policy := generate.JWTPolicy(testProject())

	assert.Equal(t, "jwt-auth", policy.Name)
	assert.Equal(t, "acme", policy.Namespace)
	assert.Equal(t, "agentgateway.dev/v1alpha1", policy.APIVersion)
	assert.Equal(t, "AgentgatewayPolicy", policy.Kind)

	require.Len(t, policy.Spec.TargetRefs, 1)
	assert.Equal(t, gwv1.Kind("Gateway"), policy.Spec.TargetRefs[0].Kind)
	assert.Equal(t, gwv1.ObjectName("tenant-gateway"), policy.Spec.TargetRefs[0].Name)
	assert.Equal(t, gwv1.SectionName("external"), *policy.Spec.TargetRefs[0].SectionName)

	require.NotNil(t, policy.Spec.Traffic)
	require.NotNil(t, policy.Spec.Traffic.JWTAuthentication)
	assert.Equal(t, agwv1alpha1.JWTAuthenticationModeStrict, policy.Spec.Traffic.JWTAuthentication.Mode)
	require.Len(t, policy.Spec.Traffic.JWTAuthentication.Providers, 1)
	provider := policy.Spec.Traffic.JWTAuthentication.Providers[0]
	assert.Equal(t, "https://accounts.google.com", provider.Issuer)
	assert.Equal(t, []string{"mycelium-tenant-a"}, provider.Audiences)

	require.NotNil(t, provider.JWKS.Remote)
	// Service name is derived from IdP name
	assert.Equal(t, gwv1.ObjectName("google-jwks"), provider.JWKS.Remote.BackendRef.Name)
	assert.Equal(t, gwv1.PortNumber(443), *provider.JWKS.Remote.BackendRef.Port)
	// Path is parsed from the JWKSEndpoint URL
	assert.Equal(t, "/oauth2/v3/certs", provider.JWKS.Remote.JwksPath)
}

func TestJWKSService(t *testing.T) {
	proj := testProject()
	idp := proj.Spec.IdentityProviders[0]
	svc := generate.JWKSService(proj, idp)

	assert.Equal(t, "google-jwks", svc.Name)
	assert.Equal(t, "acme", svc.Namespace)
	assert.Equal(t, "ExternalName", string(svc.Spec.Type))
	assert.Equal(t, "www.googleapis.com", svc.Spec.ExternalName)
	require.Len(t, svc.Spec.Ports, 1)
	assert.Equal(t, int32(443), svc.Spec.Ports[0].Port)
}

func TestSourceContextPolicy(t *testing.T) {
	policy := generate.SourceContextPolicy(testProject())

	assert.Equal(t, "internal-source-context", policy.Name)
	assert.Equal(t, "acme", policy.Namespace)
	assert.Equal(t, "AgentgatewayPolicy", policy.Kind)

	require.Len(t, policy.Spec.TargetRefs, 1)
	assert.Equal(t, gwv1.SectionName("internal"), *policy.Spec.TargetRefs[0].SectionName)

	require.NotNil(t, policy.Spec.Traffic)
	assert.Equal(t, agwv1alpha1.PolicyPhasePreRouting, *policy.Spec.Traffic.Phase)
	require.NotNil(t, policy.Spec.Traffic.Transformation)
	require.NotNil(t, policy.Spec.Traffic.Transformation.Request)
	require.Len(t, policy.Spec.Traffic.Transformation.Request.Set, 2)
	assert.Equal(t, agwv1alpha1.HeaderName("X-Source-Pod-IP"), policy.Spec.Traffic.Transformation.Request.Set[0].Name)
	assert.Equal(t, agwshared.CELExpression("source.address"), policy.Spec.Traffic.Transformation.Request.Set[0].Value)
	assert.Equal(t, agwv1alpha1.HeaderName("X-Source-Service-Account"), policy.Spec.Traffic.Transformation.Request.Set[1].Name)
}

func TestToolAccessPolicy_WithAgentsAndTools(t *testing.T) {
	proj := testProject()
	agents := []v1alpha1.MyceliumAgent{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "github-assistant", Namespace: "acme"},
			Spec: v1alpha1.MyceliumAgentSpec{
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
			Spec: v1alpha1.MyceliumAgentSpec{
				Description: "Multi agent",
				ToolBindings: []v1alpha1.ToolBinding{
					{Tool: v1alpha1.ToolRef{Name: "list-repos"}},
				},
				Sandbox: v1alpha1.SandboxPoolConfig{Image: "img", ShutdownTimeout: "30m"},
			},
		},
	}
	policy := generate.ToolAccessPolicy(proj, agents)

	assert.Equal(t, "mcp-tool-access", policy.Name)
	assert.Equal(t, "acme", policy.Namespace)
	assert.Equal(t, "AgentgatewayPolicy", policy.Kind)

	require.Len(t, policy.Spec.TargetRefs, 1)
	assert.Equal(t, gwv1.Kind("AgentgatewayBackend"), policy.Spec.TargetRefs[0].Kind)
	assert.Equal(t, gwv1.ObjectName("mycelium-engine"), policy.Spec.TargetRefs[0].Name)

	require.NotNil(t, policy.Spec.Backend)
	require.NotNil(t, policy.Spec.Backend.MCP)
	require.NotNil(t, policy.Spec.Backend.MCP.Authorization)
	assert.Equal(t, agwshared.AuthorizationPolicyActionAllow, policy.Spec.Backend.MCP.Authorization.Action)

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
	assert.Equal(t, agwshared.AuthorizationPolicyActionDeny, policy.Spec.Backend.MCP.Authorization.Action)
	assert.Equal(t, agwshared.CELExpression("true"), policy.Spec.Backend.MCP.Authorization.Policy.MatchExpressions[0])
}

func TestToolAccessPolicy_AgentsWithNoToolRefs_DenyAll(t *testing.T) {
	proj := testProject()
	agents := []v1alpha1.MyceliumAgent{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "empty-agent", Namespace: "acme"},
			Spec: v1alpha1.MyceliumAgentSpec{
				Description:  "Agent with no tools",
				Sandbox:      v1alpha1.SandboxPoolConfig{Image: "i", ShutdownTimeout: "30m"},
				ToolBindings: []v1alpha1.ToolBinding{}, // no tool bindings
			},
		},
	}
	policy := generate.ToolAccessPolicy(proj, agents)
	require.NotNil(t, policy.Spec.Backend.MCP.Authorization)
	assert.Equal(t, agwshared.AuthorizationPolicyActionDeny, policy.Spec.Backend.MCP.Authorization.Action)
	assert.Equal(t, agwshared.CELExpression("true"), policy.Spec.Backend.MCP.Authorization.Policy.MatchExpressions[0])
}
