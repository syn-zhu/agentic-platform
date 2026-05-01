package krtcontroller

import (
	"istio.io/istio/pkg/config/schema/kubetypes"
	"istio.io/istio/pkg/kube/krt"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agwv1alpha1 "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"
	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
)

const (
	toolAccessPolicyFieldManager = "mycelium-tool-access-policy"
	toolAccessPolicySuffix       = "-tool-access-policy"
)

type ObjectReference struct {
	Namespace string
	Name      string
	UID       types.UID
}

// func (t *ToolAccessRule) ResourceName() string {
// 	return string(t.Ecosystem.UID)
// }

// TODO rename this

func getControllerOfType[T interface {
	client.Object
	kubetypes.RegisterType[T]
}](obj metav1.Object) *metav1.OwnerReference {
	var zero T
	gvk := zero.GetGVK()
	owner := metav1.GetControllerOf(obj)

	if owner == nil || owner.APIVersion != gvk.GroupVersion() || owner.Kind != gvk.Kind {
		return nil
	}

	return owner
}

func newOwnerIndex[T OwnedObject](collection krt.Collection[T], name string) krt.Index[types.UID, T] {
	return krt.NewIndex(collection, name, func(t T) []types.UID {
		return []types.UID{t.GetOwner().UID}
	})
}

func toolAccessRulesByEcosystemIndex(rules krt.Collection[ToolAccessRule]) krt.Index[types.UID, ToolAccessRule] {
	return newOwnerIndex(rules, "ecosystem-toolAccessRules")
}

type EcosystemToolAccessPolicy struct {
	// TODO: things that need to exist
	Ecosystem  *ObjectReference
	ToolServer *ObservedObject[*agwv1alpha1.AgentgatewayBackend]

	// "resolved state"
	ToolAccessRules []ToolAccessRule
}

func desiredEcosystemToolAccessPolicyCollection(
	observedToolServers krt.Collection[ObservedObject[*agwv1alpha1.AgentgatewayBackend]],
	desiredToolAccessRules krt.Collection[ToolAccessRule],
	desiredToolAccessRulesByEcosystem krt.Index[types.UID, ToolAccessRule],
) krt.Collection[EcosystemToolAccessPolicy] {
	return krt.NewCollection(observedToolServers, func(ctx krt.HandlerContext, toolServer ObservedObject[*agwv1alpha1.AgentgatewayBackend]) *EcosystemToolAccessPolicy {
		ecosystem := toolServer.Owner
		toolAccessRules := krt.Fetch(ctx, desiredToolAccessRules, krt.FilterIndex(desiredToolAccessRulesByEcosystem, ecosystem.UID))
		return &EcosystemToolAccessPolicy{
			// TODO: use the OwnerRef
			Ecosystem:       ecosystem,
			ToolServer:      &toolServer,
			ToolAccessRules: toolAccessRules,
		}
	})
}

type ToolReference = ObjectReference

func readyTools(
	observedTools krt.Collection[ObservedObject[*v1alpha1.MyceliumTool]],
) krt.Collection[ToolReference] {
	return krt.NewCollection(observedTools, func(ctx krt.HandlerContext, tool ObservedObject[*v1alpha1.MyceliumTool]) *ToolReference {
		toolReady := meta.IsStatusConditionTrue(tool.Object.GetConditions(), v1alpha1.ToolReadyCondition)
		if !toolReady {
			return nil
		}

		return &ToolReference{
			Namespace: tool.Object.Namespace,
			Name:      tool.Object.Name,
			UID:       tool.Object.UID,
		}
	})
}

type AgentReference struct {
	ObjectReference
	Ecosystem *ObjectReference
}

type AgentReferenceWithTools struct {
	AgentReference
	ToolNames []string
}

// TODO figure out the right word
func serviceAccountReadyAgents(
	observedAgents krt.Collection[ObservedObject[*v1alpha1.MyceliumAgent]],
) krt.Collection[AgentReference] {
	return krt.NewCollection(observedAgents, func(ctx krt.HandlerContext, agent ObservedObject[*v1alpha1.MyceliumAgent]) *AgentReference {
		// TODO: not sure this is really the right way, idk if we should consider agent ready if tools unresolved?
		serviceAccountReady := meta.IsStatusConditionTrue(agent.Object.GetConditions(), v1alpha1.AgentServiceAccountReadyReason)
		if !serviceAccountReady {
			return nil
		}

		return &AgentReference{
			ObjectReference: ObjectReference{
				Namespace: agent.Object.Namespace,
				Name:      agent.Object.Name,
				UID:       agent.Object.UID,
			},
			Ecosystem: agent.Owner,
		}
	})
}

func sandboxPoolReadyAgents(
	serviceAccountReadyAgents krt.Collection[ObservedObject[*v1alpha1.MyceliumAgent]],
) krt.Collection[ObservedObject[*v1alpha1.MyceliumAgent]] {
	// TODO
	return nil
}

func desiredToolAccessRules(
	serviceAccountAndSandboxPoolReadyAgents krt.Collection[AgentReference],
	readyTools krt.Collection[ToolReference],

) krt.Collection[ToolAccessRule] {
	// TODO:
	// First, we should have an index by (ecosystem (name, UID) + toolname -> agent)
	// For each agent, for each of its
	return nil
}

type EcosystemAndToolKey struct {
	EcosystemUID types.UID
	ToolName     string
}

func toolsByNameAndEcosystemUID(collection krt.Collection[AgentReferenceWithTools]) krt.Index[EcosystemAndToolKey, AgentReferenceWithTools] {
	return krt.NewIndex(collection, "tools-by-ecosystem-name-and-uid", func(agent AgentReferenceWithTools) []EcosystemAndToolKey {
		ecosystemToolKeys := make([]EcosystemAndToolKey, 0, len(agent.ToolNames))
		for _, toolName := range agent.ToolNames {
			ecosystemToolKeys = append(ecosystemToolKeys, EcosystemAndToolKey{
				EcosystemUID: agent.Ecosystem.UID,
				ToolName:     toolName,
			})
		}
		return ecosystemToolKeys
	})

}

type ObservedObject[T any] struct {
	Owner  *metav1.OwnerReference
	Object T
}

func (o *ObservedObject[T]) GetOwner() *metav1.OwnerReference {
	return o.Owner
}

type OwnedObject interface {
	GetOwner() *metav1.OwnerReference
}

// TODO: example, we should proably move to controlle file
// func observedEcosystemNamespaceCollection(
// 	observedNamespaces krt.Collection[*corev1.Namespace],
// ) krt.Collection[ObservedObject[*corev1.Namespace]] {
// 	return krt.NewCollection(observedNamespaces, func(ctx krt.HandlerContext, namespace *corev1.Namespace) *ObservedObject[*corev1.Namespace] {
// 		owner := getControllerOfType[*v1alpha1.MyceliumEcosystem](namespace)
// 		return &ObservedObject[*corev1.Namespace]{
// 			Object: namespace,
// 			Owner:  owner,
// 		}
// 	})
// }

// func (e EcosystemToolAccessPolicy) ResourceName() string { return e.Ecosystem.Name }

// func applyToolAccessPolicy(ctx context.Context, c client.Client, ecosystemToolAccessPolicy *EcosystemToolAccessPolicy) error {

// 	desired := agwac.AgentgatewayPolicy(
// 		ecosystemToolAccessPolicy.Ecosystem.Name+toolAccessPolicySuffix,
// 		ecosystemToolAccessPolicy.Ecosystem.Name,
// 	).
// 		WithOwnerReferences(ownerReferenceAC(ecosystemToolAccessPolicy.Ecosystem)).
// 		WithLabels(map[string]string{
// 			"mycelium.io/ecosystem":        ecosystemToolAccessPolicy.Ecosystem.Name,
// 			"app.kubernetes.io/managed-by": wellknown.DefaultMyceliumControllerName,
// 		}).
// 		WithSpec(agwac.AgentgatewayPolicySpec().
// 			WithTargetRefs(agwshared.LocalPolicyTargetReferenceWithSectionName{
// 				LocalPolicyTargetReference: agwshared.LocalPolicyTargetReference{
// 					// TODO: Import GVK for AgentgatewayBackend
// 					Group: gwv1.Group(agwwellknown.AgentgatewayBackendGroup),
// 					Kind:  gwv1.Kind(agwwellknown.AgentgatewayBackendKind),
// 					Name:  gwv1.ObjectName(ecosystemToolAccessPolicy.ToolServer.Name),
// 				},
// 			}).
// 			WithBackend(agwac.BackendFull().
// 				WithMCP(agwac.BackendMCP().
// 					WithAuthorization(agwshared.Authorization{
// 						Action: agwshared.AuthorizationPolicyActionAllow,
// 						Policy: agwshared.AuthorizationPolicy{
// 							MatchExpressions: celExpressionsFor(ecosystemToolAccessPolicy),
// 						},
// 					}),
// 				),
// 			),
// 		)

// 	return c.Apply(ctx, desired,
// 		client.FieldOwner(toolAccessPolicyFieldManager),
// 		client.ForceOwnership,
// 	)
// }

// func celExpressionsFor(desiredEcosystemToolAccessPolicy *EcosystemToolAccessPolicy) []agwshared.CELExpression {
// 	if len(desiredEcosystemToolAccessPolicy.ToolAccessRules) == 0 {
// 		return []agwshared.CELExpression{"false"}
// 	}

// 	exprs := make([]agwshared.CELExpression, 0, len(desiredEcosystemToolAccessPolicy.ToolAccessRules))
// 	for _, toolAccessRule := range desiredEcosystemToolAccessPolicy.ToolAccessRules {
// 		quotedToolNames := make([]string, len(toolAccessRule.AllowedTools))
// 		for i, tool := range toolAccessRule.AllowedTools {
// 			quotedToolNames[i] = fmt.Sprintf("%q", tool.Name)
// 		}
// 		sort.Strings(quotedToolNames)
// 		exprs = append(exprs, agwshared.CELExpression(
// 			fmt.Sprintf(`source.identity.serviceAccount == %q && mcp.tool.name in [%s]`, toolAccessRule.Agent.Status.ServiceAccount.ResourceRef.Name, strings.Join(quotedToolNames, ", ")),
// 		))
// 	}
// 	sort.Slice(exprs, func(i, j int) bool { return exprs[i] < exprs[j] })
// 	return exprs
// }
