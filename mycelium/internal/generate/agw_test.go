package generate_test

import (
	"testing"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"
	"github.com/mongodb/mycelium/internal/generate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMCPBackend(t *testing.T) {
	tc := &v1alpha1.TenantConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "tenant-a"},
	}

	obj := generate.MCPBackend(tc)
	assert.Equal(t, "mycelium-engine", obj.GetName())
	assert.Equal(t, "tenant-a", obj.GetNamespace())
	assert.Equal(t, "controller", obj.GetLabels()["mycelium.io/managed-by"])
	assert.Equal(t, "agentgateway.dev/v1alpha1", obj.GetAPIVersion())
	assert.Equal(t, "AgentgatewayBackend", obj.GetKind())

	// Verify MCP target structure
	spec, ok := obj.Object["spec"].(map[string]interface{})
	require.True(t, ok)
	mcp, ok := spec["mcp"].(map[string]interface{})
	require.True(t, ok)
	targets, ok := mcp["targets"].([]interface{})
	require.True(t, ok)
	require.Len(t, targets, 1)
	target := targets[0].(map[string]interface{})
	assert.Equal(t, "mycelium-engine", target["name"])
	assert.Equal(t, "StreamableHTTP", target["protocol"])
}

func TestMCPRoute(t *testing.T) {
	tc := &v1alpha1.TenantConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "tenant-a"},
	}

	obj := generate.MCPRoute(tc)
	assert.Equal(t, "mcp-route", obj.GetName())
	assert.Equal(t, "tenant-a", obj.GetNamespace())
	assert.Equal(t, "gateway.networking.k8s.io/v1", obj.GetAPIVersion())
	assert.Equal(t, "HTTPRoute", obj.GetKind())
}

func TestJWTPolicy(t *testing.T) {
	tc := &v1alpha1.TenantConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "tenant-a"},
		Spec: v1alpha1.TenantConfigSpec{
			IdentityProvider: v1alpha1.IdentityProviderConfig{
				Issuer:         "https://accounts.google.com",
				Audiences:      []string{"mycelium-tenant-a"},
				AllowedClients: []string{"client-id-1"},
				AllowedScopes:  []string{"openid", "profile"},
			},
		},
	}

	obj := generate.JWTPolicy(tc)
	assert.Equal(t, "jwt-auth", obj.GetName())
	assert.Equal(t, "tenant-a", obj.GetNamespace())
	assert.Equal(t, "agentgateway.dev/v1alpha1", obj.GetAPIVersion())
	assert.Equal(t, "AgentgatewayPolicy", obj.GetKind())

	// Verify issuer is in the spec
	spec, ok := obj.Object["spec"].(map[string]interface{})
	require.True(t, ok)
	traffic, ok := spec["traffic"].(map[string]interface{})
	require.True(t, ok)
	jwtAuth, ok := traffic["jwtAuthentication"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "Strict", jwtAuth["mode"])
}

func TestSourceContextPolicy(t *testing.T) {
	tc := &v1alpha1.TenantConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "tenant-a"},
	}

	obj := generate.SourceContextPolicy(tc)
	assert.Equal(t, "internal-source-context", obj.GetName())
	assert.Equal(t, "tenant-a", obj.GetNamespace())
	assert.Equal(t, "AgentgatewayPolicy", obj.GetKind())

	// Verify PreRouting phase
	spec := obj.Object["spec"].(map[string]interface{})
	traffic := spec["traffic"].(map[string]interface{})
	assert.Equal(t, "PreRouting", traffic["phase"])
}

func TestToolAccessPolicy(t *testing.T) {
	celExprs := []string{
		`source.workload.unverified.serviceAccount == "github-assistant" && mcp.tool.name == "list_repos"`,
	}

	obj := generate.ToolAccessPolicy("tenant-a", celExprs)
	assert.Equal(t, "mcp-tool-access", obj.GetName())
	assert.Equal(t, "tenant-a", obj.GetNamespace())
	assert.Equal(t, "AgentgatewayPolicy", obj.GetKind())

	// Verify CEL expressions are in the spec
	spec := obj.Object["spec"].(map[string]interface{})
	backend := spec["backend"].(map[string]interface{})
	mcp := backend["mcp"].(map[string]interface{})
	authz := mcp["authorization"].(map[string]interface{})
	assert.Equal(t, "Allow", authz["action"])
	policy := authz["policy"].(map[string]interface{})
	exprs := policy["matchExpressions"].([]interface{})
	assert.Len(t, exprs, 1)
}
