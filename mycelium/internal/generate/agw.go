package generate

import (
	"fmt"
	"sort"
	"strings"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"

	agwac "github.com/agentgateway/agentgateway/controller/api/applyconfiguration/v1alpha1/agentgateway"
	sharedac "github.com/agentgateway/agentgateway/controller/api/applyconfiguration/v1alpha1/shared"
	agwv1alpha1 "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"
	agwshared "github.com/agentgateway/agentgateway/controller/api/v1alpha1/shared"
	corev1 "k8s.io/api/core/v1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1ac "sigs.k8s.io/gateway-api/applyconfiguration/apis/v1"
)

// MCPBackend generates an AgentgatewayBackend apply configuration for the engine as an MCP server.
func MCPBackend(p *v1alpha1.Project) *agwac.AgentgatewayBackendApplyConfiguration {
	port := int32(8080)
	protocol := agwv1alpha1.MCPProtocolStreamableHTTP

	return agwac.AgentgatewayBackend("mycelium-engine", p.Name).
		WithLabels(ManagedLabels()).
		WithSpec(agwac.AgentgatewayBackendSpec().
			WithMCP(agwac.MCPBackend().
				WithTargets(agwac.McpTargetSelector().
					WithName(gwv1.SectionName("mycelium-engine")).
					// TODO: migrate to UDS backend once https://github.com/agentgateway/agentgateway/pull/1533 merges
					WithStatic(agwac.McpTarget().
						WithBackendRef(corev1.LocalObjectReference{Name: "mycelium-engine"}).
						WithPort(port).
						WithProtocol(protocol)))))
}

// MCPRoute generates an HTTPRoute apply configuration routing /mcp to the engine MCP backend.
func MCPRoute(p *v1alpha1.Project) *gwv1ac.HTTPRouteApplyConfiguration {
	pathPrefix := gwv1.PathMatchPathPrefix
	mcpPath := "/mcp"
	sectionName := gwv1.SectionName("internal")
	agwGroup := gwv1.Group("agentgateway.dev")
	agwKind := gwv1.Kind("AgentgatewayBackend")

	return gwv1ac.HTTPRoute("mcp-route", p.Name).
		WithLabels(ManagedLabels()).
		WithSpec(gwv1ac.HTTPRouteSpec().
			WithParentRefs(gwv1ac.ParentReference().
				WithName("tenant-gateway").
				WithSectionName(sectionName)).
			WithRules(gwv1ac.HTTPRouteRule().
				WithMatches(gwv1ac.HTTPRouteMatch().
					WithPath(gwv1ac.HTTPPathMatch().
						WithType(pathPrefix).
						WithValue(mcpPath))).
				WithBackendRefs(gwv1ac.HTTPBackendRef().
					WithGroup(agwGroup).
					WithKind(agwKind).
					WithName("mycelium-engine"))))
}

// JWTPolicy generates an AgentgatewayPolicy apply configuration for JWT validation on the external listener.
func JWTPolicy(p *v1alpha1.Project) *agwac.AgentgatewayPolicyApplyConfiguration {
	idp := p.Spec.IdentityProvider
	sectionName := gwv1.SectionName("external")

	return agwac.AgentgatewayPolicy("jwt-auth", p.Name).
		WithLabels(ManagedLabels()).
		WithSpec(agwac.AgentgatewayPolicySpec().
			WithTargetRefs(sharedac.LocalPolicyTargetReferenceWithSectionName().
				WithGroup("gateway.networking.k8s.io").
				WithKind("Gateway").
				WithName("tenant-gateway").
				WithSectionName(sectionName)).
			WithTraffic(agwac.Traffic().
				WithJWTAuthentication(agwac.JWTAuthentication().
					WithMode(agwv1alpha1.JWTAuthenticationModeStrict).
					WithProviders(agwac.JWTProvider().
						WithIssuer(idp.Issuer).
						WithAudiences(idp.Audiences...)))))
}

// SourceContextPolicy generates a PreRouting transformation apply configuration on the internal
// listener that injects source identity headers.
func SourceContextPolicy(p *v1alpha1.Project) *agwac.AgentgatewayPolicyApplyConfiguration {
	sectionName := gwv1.SectionName("internal")
	phase := agwv1alpha1.PolicyPhasePreRouting

	return agwac.AgentgatewayPolicy("internal-source-context", p.Name).
		WithLabels(ManagedLabels()).
		WithSpec(agwac.AgentgatewayPolicySpec().
			WithTargetRefs(sharedac.LocalPolicyTargetReferenceWithSectionName().
				WithGroup("gateway.networking.k8s.io").
				WithKind("Gateway").
				WithName("tenant-gateway").
				WithSectionName(sectionName)).
			WithTraffic(agwac.Traffic().
				WithPhase(phase).
				WithTransformation(agwac.Transformation().
					WithRequest(agwac.Transform().
						WithSet(
							agwac.HeaderTransformation().
								WithName("X-Source-Pod-IP").
								WithValue("source.address"),
							agwac.HeaderTransformation().
								WithName("X-Source-Service-Account").
								WithValue("source.workload.unverified.serviceAccount"))))))
}

// ToolAccessPolicy generates an AgentgatewayPolicy apply configuration with backend.mcp.authorization
// for tool-level access control based on agent identity. It computes CEL expressions
// from the agent→tool mapping. If there are no agents (or none with tool refs), it
// generates a deny-all policy (Deny action with a catch-all expression).
func ToolAccessPolicy(p *v1alpha1.Project, agents []v1alpha1.Agent) *agwac.AgentgatewayPolicyApplyConfiguration {
	exprs := toolAccessCEL(agents)

	var action agwshared.AuthorizationPolicyAction
	if len(exprs) == 0 {
		action = agwshared.AuthorizationPolicyActionDeny
		exprs = []agwshared.CELExpression{"true"}
	} else {
		action = agwshared.AuthorizationPolicyActionAllow
	}

	return agwac.AgentgatewayPolicy("mcp-tool-access", p.Name).
		WithLabels(ManagedLabels()).
		WithSpec(agwac.AgentgatewayPolicySpec().
			WithTargetRefs(sharedac.LocalPolicyTargetReferenceWithSectionName().
				WithGroup("agentgateway.dev").
				WithKind("AgentgatewayBackend").
				WithName("mycelium-engine")).
			WithBackend(agwac.BackendFull().
				WithMCP(agwac.BackendMCP().
					WithAuthorization(sharedac.Authorization().
						WithAction(action).
						WithPolicy(sharedac.AuthorizationPolicy().
							WithMatchExpressions(exprs...))))))
}

// toolAccessCEL generates CEL match expressions from agents.
// Returns one expression per agent, sorted by agent name for deterministic output.
// Returns nil if there are no agents or no valid agent→tool mappings.
func toolAccessCEL(agents []v1alpha1.Agent) []agwshared.CELExpression {
	if len(agents) == 0 {
		return nil
	}

	// Build one CEL expression per agent.
	// We don't validate that tool refs point to existing Tools here — that's
	// enforced by the ValidatingWebhook on Agent create/update (tool refs must
	// exist) and on Tool delete (rejected if Agents reference it).
	var exprs []agwshared.CELExpression
	for _, agent := range agents {
		if len(agent.Spec.ToolBindings) == 0 {
			continue
		}

		var toolExpr string
		if len(agent.Spec.ToolBindings) == 1 {
			toolExpr = fmt.Sprintf(`mcp.tool.name == "%s"`, MCPToolName(agent.Spec.ToolBindings[0].Tool.Name))
		} else {
			var toolNames []string
			for _, tb := range agent.Spec.ToolBindings {
				toolNames = append(toolNames, MCPToolName(tb.Tool.Name))
			}
			sort.Strings(toolNames)
			quoted := make([]string, len(toolNames))
			for i, t := range toolNames {
				quoted[i] = fmt.Sprintf(`"%s"`, t)
			}
			toolExpr = fmt.Sprintf(`mcp.tool.name in [%s]`, strings.Join(quoted, ", "))
		}

		exprs = append(exprs, agwshared.CELExpression(fmt.Sprintf(
			`source.workload.unverified.serviceAccount == "%s" && %s`, agent.Name, toolExpr,
		)))
	}

	// Sort for deterministic output
	sort.Slice(exprs, func(i, j int) bool { return exprs[i] < exprs[j] })
	return exprs
}

