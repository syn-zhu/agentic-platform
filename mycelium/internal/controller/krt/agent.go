package krtcontroller

import (
	"context"
	"fmt"

	"istio.io/istio/pkg/kube/controllers"
	"istio.io/istio/pkg/kube/krt"
	"k8s.io/apimachinery/pkg/types"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	metav1ac "k8s.io/client-go/applyconfigurations/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/pkg/wellknown"
)

const agentServiceAccountFieldManager = "mycelium-agent-serviceaccount"

// AgentServiceAccount is an in-memory KRT type representing the desired
// ServiceAccount for a MyceliumAgent. It is never written as a CRD; it is
// the intermediate representation that feeds the SA sink.
type AgentServiceAccount struct {
	AgentName      string
	AgentNamespace string
	AgentUID       types.UID
}

// ResourceName satisfies krt.ResourceNamer. Namespace-qualified because
// ServiceAccounts are namespaced — two agents in different namespaces would
// otherwise collide in the collection.
func (a AgentServiceAccount) ResourceName() string {
	return a.AgentNamespace + "/" + a.AgentName
}

// agentServiceAccountCollection derives the desired ServiceAccount for every
// MyceliumAgent. 1:1 derivation — one agent yields exactly one ServiceAccount.
func agentServiceAccountCollection(
	agents krt.Collection[*v1alpha1.MyceliumAgent],
) krt.Collection[AgentServiceAccount] {
	return krt.NewCollection(
		agents,
		func(_ krt.HandlerContext, agent *v1alpha1.MyceliumAgent) *AgentServiceAccount {
			// Exclude deleting agents. Returning nil removes this entry from the
			// derived collection, which the sink observes as a delete event.
			if agent.DeletionTimestamp != nil {
				return nil
			}
			return &AgentServiceAccount{
				AgentName:      agent.Name,
				AgentNamespace: agent.Namespace,
				AgentUID:       agent.UID,
			}
		},
		krt.WithName("AgentServiceAccounts"),
	)
}

// registerAgentServiceAccountSink is the output sink for the agent SA pipeline.
// It is registered on the derived AgentServiceAccount collection. On delete,
// the OwnerReference on the ServiceAccount causes Kubernetes GC to clean up
// automatically.
func registerAgentServiceAccountSink(
	ctx context.Context,
	ssaClient client.Client,
	serviceAccounts krt.Collection[AgentServiceAccount],
) {
	serviceAccounts.RegisterBatch(func(events []krt.Event[AgentServiceAccount]) {
		for _, e := range events {
			switch e.Event {
			case controllers.EventDelete:
				// ServiceAccount is garbage-collected via OwnerReference — no
				// explicit delete needed.
				continue
			case controllers.EventAdd, controllers.EventUpdate:
				sa := e.Latest()
				if err := applyAgentServiceAccount(ctx, ssaClient, &sa); err != nil {
					// TODO: surface via agent status condition.
					fmt.Printf("%s: failed to apply ServiceAccount for agent %q: %v\n",
						agentServiceAccountFieldManager, sa.AgentNamespace+"/"+sa.AgentName, err)
				}
			}
		}
	}, true /* runExistingState */)
}

// applyAgentServiceAccount SSA-applies the ServiceAccount for the given desired state.
func applyAgentServiceAccount(
	ctx context.Context,
	c client.Client,
	sa *AgentServiceAccount,
) error {
	ownerRef := metav1ac.OwnerReference().
		WithAPIVersion(v1alpha1.MyceliumAgentGVK.GroupVersion().String()).
		WithKind(v1alpha1.MyceliumAgentGVK.Kind).
		WithName(sa.AgentName).
		WithUID(sa.AgentUID).
		WithController(true).
		WithBlockOwnerDeletion(true)

	desired := corev1ac.ServiceAccount(sa.AgentName, sa.AgentNamespace).
		WithOwnerReferences(ownerRef).
		WithLabels(map[string]string{
			"mycelium.io/agent":            sa.AgentName,
			"app.kubernetes.io/managed-by": wellknown.DefaultMyceliumControllerName,
		})

	return c.Apply(ctx, desired,
		client.FieldOwner(agentServiceAccountFieldManager),
		client.ForceOwnership,
	)
}
