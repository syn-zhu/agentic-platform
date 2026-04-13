package generate

import (
	"fmt"
	"sort"
	"strings"

	v1alpha1 "github.com/mongodb/mycelium/api/v1alpha1"

	agwv1alpha1 "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"
	"github.com/agentgateway/agentgateway/controller/api/v1alpha1/shared"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// MCPBackend generates an AgentgatewayBackend for the engine as an MCP server.
func MCPBackend(p *v1alpha1.Project) *agwv1alpha1.AgentgatewayBackend {
	port := int32(8080)
	protocol := agwv1alpha1.MCPProtocolStreamableHTTP
	return &agwv1alpha1.AgentgatewayBackend{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "agentgateway.dev/v1alpha1",
			Kind:       "AgentgatewayBackend",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mycelium-engine",
			Namespace: p.Name,
			Labels:    ManagedLabels(),
		},
		Spec: agwv1alpha1.AgentgatewayBackendSpec{
			MCP: &agwv1alpha1.MCPBackend{
				Targets: []agwv1alpha1.McpTargetSelector{{
					Name: "mycelium-engine",
					// TODO: migrate to UDS backend once https://github.com/agentgateway/agentgateway/pull/1533 merges
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
func MCPRoute(p *v1alpha1.Project) *gwv1.HTTPRoute {
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
			Namespace: p.Name,
			Labels:    ManagedLabels(),
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
func JWTPolicy(p *v1alpha1.Project) *agwv1alpha1.AgentgatewayPolicy {
	idp := p.Spec.IdentityProvider
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
			Namespace: p.Name,
			Labels:    ManagedLabels(),
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
func SourceContextPolicy(p *v1alpha1.Project) *agwv1alpha1.AgentgatewayPolicy {
	sectionName := gwv1.SectionName("internal")
	phase := agwv1alpha1.PolicyPhasePreRouting

	return &agwv1alpha1.AgentgatewayPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "agentgateway.dev/v1alpha1",
			Kind:       "AgentgatewayPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "internal-source-context",
			Namespace: p.Name,
			Labels:    ManagedLabels(),
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
// for tool-level access control based on agent identity. It computes CEL expressions
// from the agent→tool mapping. If there are no agents (or none with tool refs), it
// generates a deny-all policy (Deny action with a catch-all expression).
func ToolAccessPolicy(p *v1alpha1.Project, agents []v1alpha1.Agent) *agwv1alpha1.AgentgatewayPolicy {
	namespace := p.Name

	var authz *shared.Authorization

	exprs := toolAccessCEL(agents)
	if len(exprs) == 0 {
		// Deny all — no agents, no tools, or no valid mappings means no access
		authz = &shared.Authorization{
			Action: shared.AuthorizationPolicyActionDeny,
			Policy: shared.AuthorizationPolicy{
				MatchExpressions: []shared.CELExpression{"true"},
			},
		}
	} else {
		authz = &shared.Authorization{
			Action: shared.AuthorizationPolicyActionAllow,
			Policy: shared.AuthorizationPolicy{
				MatchExpressions: exprs,
			},
		}
	}

	return &agwv1alpha1.AgentgatewayPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "agentgateway.dev/v1alpha1",
			Kind:       "AgentgatewayPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mcp-tool-access",
			Namespace: namespace,
			Labels:    ManagedLabels(),
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
					Authorization: authz,
				},
			},
		},
	}
}

// toolAccessCEL generates CEL match expressions from agents.
// Returns one expression per agent, sorted by agent name for deterministic output.
// Returns nil if there are no agents or no valid agent→tool mappings.
func toolAccessCEL(agents []v1alpha1.Agent) []shared.CELExpression {
	if len(agents) == 0 {
		return nil
	}

	// Build one CEL expression per agent.
	// We don't validate that tool refs point to existing Tools here — that's
	// enforced by the ValidatingWebhook on Agent create/update (tool refs must
	// exist) and on Tool delete (rejected if Agents reference it).
	var exprs []shared.CELExpression
	for _, agent := range agents {
		if len(agent.Spec.Tools) == 0 {
			continue
		}

		var toolExpr string
		if len(agent.Spec.Tools) == 1 {
			toolExpr = fmt.Sprintf(`mcp.tool.name == "%s"`, MCPToolName(agent.Spec.Tools[0].Ref.Name))
		} else {
			var toolNames []string
			for _, ref := range agent.Spec.Tools {
				toolNames = append(toolNames, MCPToolName(ref.Ref.Name))
			}
			sort.Strings(toolNames)
			quoted := make([]string, len(toolNames))
			for i, t := range toolNames {
				quoted[i] = fmt.Sprintf(`"%s"`, t)
			}
			toolExpr = fmt.Sprintf(`mcp.tool.name in [%s]`, strings.Join(quoted, ", "))
		}

		exprs = append(exprs, shared.CELExpression(fmt.Sprintf(
			`source.workload.unverified.serviceAccount == "%s" && %s`, agent.Name, toolExpr,
		)))
	}

	// Sort for deterministic output
	sort.Slice(exprs, func(i, j int) bool { return exprs[i] < exprs[j] })
	return exprs
}

func toShortStrings(ss []string) []agwv1alpha1.ShortString {
	result := make([]agwv1alpha1.ShortString, len(ss))
	for i, s := range ss {
		result[i] = agwv1alpha1.ShortString(s)
	}
	return result
}
