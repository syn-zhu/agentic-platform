package krtcontroller

import (
	"context"
	"fmt"

	"istio.io/istio/pkg/cluster"
	"istio.io/istio/pkg/kube"
	"istio.io/istio/pkg/kube/krt"
	"istio.io/istio/pkg/kube/kubetypes"
	"istio.io/istio/pkg/util/identifier"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/pkg/wellknown"
)

// Controller is the KRT-based reactive controller. It uses an Istio kube.Client
// for reactive watching (KRT collections) and a controller-runtime client for
// SSA writes.
type Controller struct {
	kubeClient kube.Client
	ssaClient  client.Client
}

// New creates a KRT Controller. cfg should be the same *rest.Config used by the
// controller-runtime manager so both share the same cluster credentials.
func New(cfg *rest.Config, ssaClient client.Client) (*Controller, error) {
	kubeClient, err := kube.NewClient(kube.NewClientConfigForRestConfig(cfg), cluster.ID(identifier.Undefined))
	if err != nil {
		return nil, fmt.Errorf("creating KRT kube client: %w", err)
	}
	return &Controller{
		kubeClient: kubeClient,
		ssaClient:  ssaClient,
	}, nil
}

// Start runs the KRT controller until ctx is cancelled. It implements
// manager.Runnable so it can be wired in via mgr.Add.
func (c *Controller) Start(ctx context.Context) error {
	stop := ctx.Done()

	// Root collections — one informer per CRD type.
	ecosystems := krt.NewInformer[*v1alpha1.MyceliumEcosystem](c.kubeClient,
		krt.WithName("MyceliumEcosystems"))
	agents := krt.NewInformer[*v1alpha1.MyceliumAgent](c.kubeClient,
		krt.WithName("MyceliumAgents"))
	tools := krt.NewInformer[*v1alpha1.MyceliumTool](c.kubeClient,
		krt.WithName("MyceliumTools"))
	_ = krt.NewInformer[*v1alpha1.MyceliumCredentialProvider](c.kubeClient,
		krt.WithName("MyceliumCredentialProviders"))

	// Observed collections — watch what was actually written so drift correction
	// can detect when the cluster state diverges from desired.
	//
	// TODO: narrow the namespace filter once we can derive the full set of
	// ecosystem namespaces dynamically; for now we watch cluster-wide and rely
	// on the label selector to scope the result set.
	managedByLabel := wellknown.DefaultMyceliumControllerName
	observedNamespaces := krt.NewFilteredInformer[*corev1.Namespace](c.kubeClient,
		kubetypes.Filter{
			LabelSelector: "app.kubernetes.io/managed-by=" + managedByLabel,
		},
		krt.WithName("ObservedEcosystemNamespaces"))
	observedServiceAccounts := krt.NewFilteredInformer[*corev1.ServiceAccount](c.kubeClient,
		kubetypes.Filter{
			LabelSelector: "app.kubernetes.io/managed-by=" + managedByLabel,
		},
		krt.WithName("ObservedAgentServiceAccounts"))

	// Derived collections — each pipeline is a DAG of in-memory types.
	desiredNamespaces := ecosystemNamespaceCollection(ecosystems)
	desiredServiceAccounts := agentServiceAccountCollection(agents)

	// Sinks — registered only on final derived collections.
	registerNamespaceSink(ctx, c.ssaClient, desiredNamespaces)
	registerAgentServiceAccountSink(ctx, c.ssaClient, desiredServiceAccounts)

	// RunAndWait starts all informers and blocks until synced or stop is closed.
	// Run it in a goroutine so we can wait on both it and ctx.
	go c.kubeClient.RunAndWait(stop)

	// Wait for all active pipeline output collections to sync before serving.
	for _, s := range []struct {
		name string
		krt.Syncer
	}{
		{"EcosystemNamespaces", desiredNamespaces},
		{"AgentServiceAccounts", desiredServiceAccounts},
		{"ObservedEcosystemNamespaces", observedNamespaces},
		{"ObservedAgentServiceAccounts", observedServiceAccounts},
	} {
		if !s.WaitUntilSynced(stop) {
			return fmt.Errorf("timed out waiting for %s collection to sync", s.name)
		}
	}

	// tools is wired into future pipelines; silence unused-variable warning until then.
	_ = tools

	<-stop
	return nil
}
