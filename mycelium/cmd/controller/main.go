package main

import (
	"flag"
	"os"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	"mycelium.io/mycelium/internal/controller"
	"mycelium.io/mycelium/internal/indexes"
	"mycelium.io/mycelium/internal/webhook"

	agwv1alpha1 "github.com/agentgateway/agentgateway/controller/api/v1alpha1/agentgateway"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	knservingv1 "knative.dev/serving/pkg/apis/serving/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(gwv1.Install(scheme))
	utilruntime.Must(agwv1alpha1.Install(scheme))
	utilruntime.Must(knservingv1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true))) // TODO

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:           scheme,
		LeaderElection:   true,
		LeaderElectionID: "mycelium-controller",
	})
	if err != nil {
		ctrl.Log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()

	if err := indexes.SetupIndexes(ctx, mgr); err != nil {
		ctrl.Log.Error(err, "unable to setup field indexes")
		os.Exit(1)
	}

	base := &controller.Base{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}

	if err := (&controller.EcosystemReconciler{Base: base}).SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create Project controller")
		os.Exit(1)
	}

	if err := (&controller.IdentityProviderReconciler{Base: base}).SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create IdentityProvider controller")
		os.Exit(1)
	}

	if err := (&controller.ToolReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create Tool controller")
		os.Exit(1)
	}

	if err := (&controller.CredentialProviderReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create CredentialProvider controller")
		os.Exit(1)
	}

	if err := (&controller.AgentReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		ctrl.Log.Error(err, "unable to create Agent controller")
		os.Exit(1)
	}

	// Register validating webhooks
	if err := webhook.SetupWebhooks(mgr); err != nil {
		ctrl.Log.Error(err, "unable to setup webhooks")
		os.Exit(1)
	}

	ctrl.Log.Info("starting controller manager")
	if err := mgr.Start(ctx); err != nil {
		ctrl.Log.Error(err, "problem running manager")
		os.Exit(1)
	}
}
