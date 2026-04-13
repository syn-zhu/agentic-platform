package generate

import (
	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"

	agwv1alpha1 "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"
	"github.com/agentgateway/agentgateway/controller/api/v1alpha1/shared"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const managedBy = "mycelium-controller"

func managedLabels() map[string]string {
	return map[string]string{"app.kubernetes.io/managed-by": managedBy}
}

// MCPBackend generates an AgentgatewayBackend for the engine as an MCP server.
func MCPBackend(tc *v1alpha1.TenantConfig) *agwv1alpha1.AgentgatewayBackend {
	port := int32(8080)
	protocol := agwv1alpha1.MCPProtocolStreamableHTTP
	return &agwv1alpha1.AgentgatewayBackend{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "agentgateway.dev/v1alpha1",
			Kind:       "AgentgatewayBackend",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mycelium-engine",
			Namespace: tc.Namespace,
			Labels:    managedLabels(),
		},
		Spec: agwv1alpha1.AgentgatewayBackendSpec{
			MCP: &agwv1alpha1.MCPBackend{
				Targets: []agwv1alpha1.McpTargetSelector{{
					Name: "mycelium-engine",
					Static: &agwv1alpha1.McpTarget{
						BackendRef: &corev1.LocalObjectReference{Name: "mycelium-engine"},
						Port:       port,
						Protocol:   &protocol,
					},
				}},
			},
		},
	}
}

// MCPRoute generates an HTTPRoute routing /mcp to the engine MCP backend.
func MCPRoute(tc *v1alpha1.TenantConfig) *gwv1.HTTPRoute {
	pathPrefix := gwv1.PathMatchPathPrefix
	mcpPath := "/mcp"
	sectionName := gwv1.SectionName("internal")
	agwGroup := gwv1.Group("agentgateway.dev")
	agwKind := gwv1.Kind("AgentgatewayBackend")

	return &gwv1.HTTPRoute{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "gateway.networking.k8s.io/v1",
			Kind:       "HTTPRoute",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mcp-route",
			Namespace: tc.Namespace,
			Labels:    managedLabels(),
		},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{
				ParentRefs: []gwv1.ParentReference{{
					Name:        "tenant-gateway",
					SectionName: &sectionName,
				}},
			},
			Rules: []gwv1.HTTPRouteRule{{
				Matches: []gwv1.HTTPRouteMatch{{
					Path: &gwv1.HTTPPathMatch{
						Type:  &pathPrefix,
						Value: &mcpPath,
					},
				}},
				BackendRefs: []gwv1.HTTPBackendRef{{
					BackendRef: gwv1.BackendRef{
						BackendObjectReference: gwv1.BackendObjectReference{
							Group: &agwGroup,
							Kind:  &agwKind,
							Name:  "mycelium-engine",
						},
					},
				}},
			}},
		},
	}
}

// JWTPolicy generates an AgentgatewayPolicy for JWT validation on the external listener.
func JWTPolicy(tc *v1alpha1.TenantConfig) *agwv1alpha1.AgentgatewayPolicy {
	idp := tc.Spec.IdentityProvider
	sectionName := gwv1.SectionName("external")

	provider := agwv1alpha1.JWTProvider{
		Issuer:    agwv1alpha1.ShortString(idp.Issuer),
		Audiences: toShortStrings(idp.Audiences),
	}

	return &agwv1alpha1.AgentgatewayPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "agentgateway.dev/v1alpha1",
			Kind:       "AgentgatewayPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "jwt-auth",
			Namespace: tc.Namespace,
			Labels:    managedLabels(),
		},
		Spec: agwv1alpha1.AgentgatewayPolicySpec{
			TargetRefs: []shared.LocalPolicyTargetReferenceWithSectionName{{
				LocalPolicyTargetReference: shared.LocalPolicyTargetReference{
					Group: "gateway.networking.k8s.io",
					Kind:  "Gateway",
					Name:  "tenant-gateway",
				},
				SectionName: &sectionName,
			}},
			Traffic: &agwv1alpha1.Traffic{
				JWTAuthentication: &agwv1alpha1.JWTAuthentication{
					Mode:      agwv1alpha1.JWTAuthenticationModeStrict,
					Providers: []agwv1alpha1.JWTProvider{provider},
				},
			},
		},
	}
}

// SourceContextPolicy generates a PreRouting transformation policy on the internal
// listener that injects source identity headers.
func SourceContextPolicy(tc *v1alpha1.TenantConfig) *agwv1alpha1.AgentgatewayPolicy {
	sectionName := gwv1.SectionName("internal")
	phase := agwv1alpha1.PolicyPhasePreRouting

	return &agwv1alpha1.AgentgatewayPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "agentgateway.dev/v1alpha1",
			Kind:       "AgentgatewayPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "internal-source-context",
			Namespace: tc.Namespace,
			Labels:    managedLabels(),
		},
		Spec: agwv1alpha1.AgentgatewayPolicySpec{
			TargetRefs: []shared.LocalPolicyTargetReferenceWithSectionName{{
				LocalPolicyTargetReference: shared.LocalPolicyTargetReference{
					Group: "gateway.networking.k8s.io",
					Kind:  "Gateway",
					Name:  "tenant-gateway",
				},
				SectionName: &sectionName,
			}},
			Traffic: &agwv1alpha1.Traffic{
				Phase: &phase,
				Transformation: &agwv1alpha1.Transformation{
					Request: &agwv1alpha1.Transform{
						Set: []agwv1alpha1.HeaderTransformation{
							{Name: "X-Source-Pod-IP", Value: "source.address"},
							{Name: "X-Source-Service-Account", Value: "source.workload.unverified.serviceAccount"},
						},
					},
				},
			},
		},
	}
}

// ToolAccessPolicy generates an AgentgatewayPolicy with backend.mcp.authorization
// for tool-level access control based on agent identity.
func ToolAccessPolicy(namespace string, celExpressions []string) *agwv1alpha1.AgentgatewayPolicy {
	exprs := make([]shared.CELExpression, len(celExpressions))
	for i, e := range celExpressions {
		exprs[i] = shared.CELExpression(e)
	}

	return &agwv1alpha1.AgentgatewayPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "agentgateway.dev/v1alpha1",
			Kind:       "AgentgatewayPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mcp-tool-access",
			Namespace: namespace,
			Labels:    managedLabels(),
		},
		Spec: agwv1alpha1.AgentgatewayPolicySpec{
			TargetRefs: []shared.LocalPolicyTargetReferenceWithSectionName{{
				LocalPolicyTargetReference: shared.LocalPolicyTargetReference{
					Group: "agentgateway.dev",
					Kind:  "AgentgatewayBackend",
					Name:  "mycelium-engine",
				},
			}},
			Backend: &agwv1alpha1.BackendFull{
				MCP: &agwv1alpha1.BackendMCP{
					Authorization: &shared.Authorization{
						Action: shared.AuthorizationPolicyActionAllow,
						Policy: shared.AuthorizationPolicy{
							MatchExpressions: exprs,
						},
					},
				},
			},
		},
	}
}

func toShortStrings(ss []string) []agwv1alpha1.ShortString {
	result := make([]agwv1alpha1.ShortString, len(ss))
	for i, s := range ss {
		result[i] = agwv1alpha1.ShortString(s)
	}
	return result
}
