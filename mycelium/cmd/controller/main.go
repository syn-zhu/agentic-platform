package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	v1alpha1 "mycelium.io/mycelium/api/v1alpha1"
	krtcontroller "mycelium.io/mycelium/internal/controller/krt"
	"mycelium.io/mycelium/pkg/wellknown"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	cfg := ctrl.GetConfigOrDie()

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:           scheme,
		LeaderElection:   true,
		LeaderElectionID: wellknown.LeaderElectionID,
	})
	if err != nil {
		ctrl.Log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	krtCtrl, err := krtcontroller.New(cfg, mgr.GetClient())
	if err != nil {
		ctrl.Log.Error(err, "unable to create KRT controller")
		os.Exit(1)
	}
	if err := mgr.Add(krtCtrl); err != nil {
		ctrl.Log.Error(err, "unable to register KRT controller with manager")
		os.Exit(1)
	}

	ctrl.Log.Info("starting controller manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		ctrl.Log.Error(err, "problem running manager")
		os.Exit(1)
	}
}
