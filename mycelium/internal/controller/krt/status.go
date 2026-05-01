package krtcontroller

import (
	"istio.io/istio/pkg/kube/krt"
	"k8s.io/apimachinery/pkg/types"
	"mycelium.io/mycelium/api/v1alpha1"
)

type StatusCollection struct {
}

// type EcosystemToolAccessPolicy struct {
// 	Ecosystem  EcosystemReference
// 	ToolServer *ObservedObject[*agwv1alpha1.AgentgatewayBackend]

// 	// "resolved state"
// 	ToolAccessRules []ToolAccessRule
// }

type EcosystemReference struct {
	Name string
	UID  types.UID
}

type ToolServerReference struct {
	Name      string
	Ecosystem types.UID
}

type ToolAccessRule struct {
	Agent        AgentReference
	AllowedTools []ToolReference
}

type LocalAgentReference struct {
	Name string
	UID  types.UID
}

type ToolReference struct {
	Name      string
	Ecosystem types.UID
}

// agentsByTool
// tool name + ecosystem UID -> agent name, agent UID

type AgentsByTool = krt.Index[ToolReference, AgentWithTools]

type AgentReference struct {
	Name      string
	UID       types.UID
	Ecosystem types.UID
}

type AgentWithTools struct {
	Name      string
	UID       types.UID
	Ecosystem types.UID
	Tools     []string
}

func localAgentsByTool(
	tools krt.Collection[ToolReference],
	agents krt.Collection[AgentWithTools],
) krt.Index[ToolReference, AgentWithTools] {
	result := krt.NewIndex(agents, "toolsByAgent", func(a AgentWithTools) []ToolReference {
		var refs []ToolReference
		for _, tool := range a.Tools {
			refs = append(refs, ToolReference{
				Name:      tool,
				Ecosystem: a.Ecosystem,
			})
		}
		return refs
	})

	s := krt.NewCollection(tools, func(ctx krt.HandlerContext, tool ToolReference) *AgentWithTools {
		x := krt.Fetch(ctx, agents, krt.FilterIndex(result, tool))
	})

	return result
}

type ResolvedAgentToolBinding struct {
	Agent *AgentReference
	Tool  *ToolReference
}

func agentToolBindings(
	readyAgents krt.Collection[AgentReference],
	readyTools krt.Collection[ToolReference],
) krt.Collection[ResolvedAgentToolBinding] {
	// result := krt.NewIndex(agents, "toolsByAgent", func(a AgentWithTools) []ToolReference {
	// 	var refs []ToolReference
	// 	for _, tool := range a.Tools {
	// 		refs = append(refs, ToolReference{
	// 			Name:      tool,
	// 			Ecosystem: a.Ecosystem,
	// 		})
	// 	}
	// 	return refs
	// })
	var agentsByTool krt.Index[string, AgentReference]

	s := krt.NewManyCollection(readyTools, func(ctx krt.HandlerContext, tool ToolReference) []ResolvedAgentToolBinding {
		indexKey := string(tool.Ecosystem) + ":" + tool.Name
		ag := krt.Fetch(ctx, readyAgents, krt.FilterIndex(agentsByTool, indexKey))
		var res []ResolvedAgentToolBinding
		for _, agent := range ag {
			res = append(res, ResolvedAgentToolBinding{
				Agent: &agent,
				Tool:  &tool,
			})
		}

		return res
	})

	return result
}

type ToolReadyStatus struct {
	Tool *v1alpha1.MyceliumTool
}

func localAgentsByTool(
	tools krt.Collection[ToolReference],
	agents krt.Collection[AgentWithTools],
) krt.Index[ToolReference, AgentWithTools] {
	result := krt.NewIndex(agents, "toolsByAgent", func(a AgentWithTools) []ToolReference {
		var refs []ToolReference
		for _, tool := range a.Tools {
			refs = append(refs, ToolReference{
				Name:      tool,
				Ecosystem: a.Ecosystem,
			})
		}
		return refs
	})

	s := krt.NewCollection(tools, func(ctx krt.HandlerContext, tool ToolReference) *AgentWithTools {
		x := krt.Fetch(ctx, agents, krt.FilterIndex(result, tool))
	})

	return result
}

func (r *ResolvedAgentToolBinding) Equals(a, b ResolvedAgentToolBinding) {
	// TODO:
	// only if both UIDs are equal,
}

// 1. Custom equaler via krt.WithObjectAugmentation / the Equaler interface. If your collection element implements Equals(other) bool, krt uses that instead of reflect.DeepEqual. So you control what "changed" means:
// gotype MyThing struct {
//     Name string
//     Spec MySpec
//     Status MyStatus   // ← don't want updates on status changes
// }

// func (a MyThing) Equals(b MyThing) bool {
//     return a.Name == b.Name && reflect.DeepEqual(a.Spec, b.Spec)
//     // Status deliberately omitted — status-only changes are "equal" → no downstream event
// }
