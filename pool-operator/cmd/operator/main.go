// pool-operator/cmd/operator/main.go
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/siyanzhu/agentic-platform/pool-operator/api/v1alpha1"
	"github.com/siyanzhu/agentic-platform/pool-operator/internal/controller"
	"github.com/siyanzhu/agentic-platform/pool-operator/internal/informer"
	"github.com/siyanzhu/agentic-platform/pool-operator/internal/labels"
	"github.com/siyanzhu/agentic-platform/pool-operator/internal/pool"
	"github.com/siyanzhu/agentic-platform/pool-operator/internal/server"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	listenAddr := envOrDefault("LISTEN_ADDR", ":8080")
	metricsAddr := envOrDefault("METRICS_ADDR", ":9090")
	namespace := envOrDefault("NAMESPACE", "agentic-platform")

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		logger.Error("failed to add client-go scheme", "err", err)
		os.Exit(1)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		logger.Error("failed to add v1alpha1 scheme", "err", err)
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                  scheme,
		LeaderElection:          true,
		LeaderElectionID:        "pool-operator-leader",
		LeaderElectionNamespace: namespace,
		LeaseDuration:           ptr(15 * time.Second),
		RenewDeadline:           ptr(10 * time.Second),
		RetryPeriod:             ptr(2 * time.Second),
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				namespace: {},
			},
		},
	})
	if err != nil {
		logger.Error("unable to create manager", "err", err)
		os.Exit(1)
	}

	registry := pool.NewRegistry()

	reconciler := controller.NewExecutorPoolReconciler(mgr.GetClient(), registry, logger)
	if err := reconciler.SetupWithManager(mgr); err != nil {
		logger.Error("unable to setup controller", "err", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go func() {
		if err := mgr.Start(ctx); err != nil {
			logger.Error("manager exited with error", "err", err)
			os.Exit(1)
		}
	}()

	if !mgr.GetCache().WaitForCacheSync(ctx) {
		logger.Error("failed to sync caches")
		os.Exit(1)
	}

	rebuildState(ctx, mgr.GetClient(), registry, namespace, logger)

	podWatcher := informer.NewPodWatcher(registry, logger)
	podInformer, err := mgr.GetCache().GetInformer(ctx, &corev1.Pod{})
	if err != nil {
		logger.Error("failed to get pod informer", "err", err)
		os.Exit(1)
	}
	podInformer.AddEventHandler(podWatcher.EventHandler())

	k8sClient := &k8sPodClient{client: mgr.GetClient()}
	persister := &labelPersister{
		managers:  make(map[string]*pool.PoolManager),
		registry:  registry,
		podClient: k8sClient,
		namespace: namespace,
		logger:    logger,
	}
	srv := server.New(registry, persister)

	go runPoolManagers(ctx, registry, k8sClient, namespace, srv.Metrics(), logger)

	mux := http.NewServeMux()
	mux.Handle("/", srv.Handler())

	httpServer := &http.Server{Addr: listenAddr, Handler: mux}
	go func() {
		logger.Info("starting HTTP server", "addr", listenAddr)
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			logger.Error("HTTP server error", "err", err)
		}
	}()

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsServer := &http.Server{Addr: metricsAddr, Handler: metricsMux}
	go func() {
		logger.Info("starting metrics server", "addr", metricsAddr)
		if err := metricsServer.ListenAndServe(); err != http.ErrServerClosed {
			logger.Error("metrics server error", "err", err)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	httpServer.Shutdown(shutdownCtx)
	metricsServer.Shutdown(shutdownCtx)
}

func rebuildState(ctx context.Context, c client.Client, registry *pool.Registry, namespace string, logger *slog.Logger) {
	// Step 1: List all ExecutorPool CRs and populate registry
	var epList v1alpha1.ExecutorPoolList
	if err := c.List(ctx, &epList, client.InNamespace(namespace)); err != nil {
		logger.Error("failed to list ExecutorPool CRs for state rebuild", "err", err)
		return
	}
	for i := range epList.Items {
		ep := &epList.Items[i]
		leaseTTL := 30 * time.Second
		if ep.Spec.LeaseTTL.Duration > 0 {
			leaseTTL = ep.Spec.LeaseTTL.Duration
		}
		warmingTimeout := 5 * time.Minute
		if ep.Spec.WarmingTimeout.Duration > 0 {
			warmingTimeout = ep.Spec.WarmingTimeout.Duration
		}
		maxSurge := 10
		if ep.Spec.MaxSurge > 0 {
			maxSurge = int(ep.Spec.MaxSurge)
		}
		registry.CreateOrUpdate(ep.Name, int(ep.Spec.Desired), leaseTTL, warmingTimeout, maxSurge, ep.Spec.PodTemplate)
		logger.Info("rebuilt pool from CR", "pool", ep.Name, "desired", ep.Spec.Desired)
	}

	// Step 2: List all pods with pool label
	var pods corev1.PodList
	if err := c.List(ctx, &pods, client.InNamespace(namespace), client.HasLabels{labels.LabelPool}); err != nil {
		logger.Error("failed to list pods for state rebuild", "err", err)
		return
	}

	for i := range pods.Items {
		pod := &pods.Items[i]
		poolName := pod.Labels[labels.LabelPool]
		p := registry.Get(poolName)
		if p == nil {
			continue
		}

		status := pod.Labels[labels.LabelStatus]
		podInfo := pool.PodInfoFromPod(pod)

		switch status {
		case labels.StatusAvailable:
			p.AddAvailable(podInfo)
		case labels.StatusClaimed:
			expiresAt := time.Now().Add(p.LeaseTTL())
			if ann, ok := pod.Annotations[labels.AnnotationLeaseExpiresAt]; ok {
				if t, err := time.Parse(time.RFC3339, ann); err == nil {
					expiresAt = t
				}
			}
			claimID := pod.Labels[labels.LabelClaimID]
			p.RestoreClaim(claimID, podInfo, expiresAt)
		default:
			p.AddWarming(pod.Name)
		}

		logger.Info("rebuilt pod state", "pool", poolName, "pod", pod.Name, "status", status)
	}
}

func runPoolManagers(ctx context.Context, registry *pool.Registry, podClient pool.PodClient, namespace string, metrics *server.Metrics, logger *slog.Logger) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	managers := make(map[string]*pool.PoolManager)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			currentPools := make(map[string]bool)
			for _, p := range registry.List() {
				currentPools[p.Name()] = true
				mgr, ok := managers[p.Name()]
				if !ok {
					mgr = pool.NewPoolManager(p, podClient, namespace, logger)
					managers[p.Name()] = mgr
				}
				mgr.Reconcile(ctx)

				status := p.Status()
				metrics.UpdateGauges(p.Name(), status.Available, status.Claimed, status.Warming)
			}
			// Clean up stale managers for deleted pools
			for name := range managers {
				if !currentPools[name] {
					delete(managers, name)
				}
			}
		}
	}
}

type k8sPodClient struct {
	client client.Client
}

func (c *k8sPodClient) CreatePod(ctx context.Context, namespace string, pod *corev1.Pod) (*corev1.Pod, error) {
	pod.Namespace = namespace
	if err := c.client.Create(ctx, pod); err != nil {
		return nil, err
	}
	return pod, nil
}

func (c *k8sPodClient) DeletePod(ctx context.Context, namespace, name string) error {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	return client.IgnoreNotFound(c.client.Delete(ctx, pod))
}

func (c *k8sPodClient) PatchPodLabelsAndAnnotations(ctx context.Context, namespace, name string, lbls map[string]string, anns map[string]string) error {
	pod := &corev1.Pod{}
	if err := c.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, pod); err != nil {
		return err
	}

	patch := client.MergeFrom(pod.DeepCopy())
	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}
	for k, v := range lbls {
		pod.Labels[k] = v
	}
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	for k, v := range anns {
		pod.Annotations[k] = v
	}
	return c.client.Patch(ctx, pod, patch)
}

// labelPersister implements server.LabelPersister by delegating to PoolManager methods.
type labelPersister struct {
	managers  map[string]*pool.PoolManager
	registry  *pool.Registry
	podClient pool.PodClient
	namespace string
	logger    *slog.Logger
}

func (lp *labelPersister) getOrCreateManager(poolName string) *pool.PoolManager {
	if mgr, ok := lp.managers[poolName]; ok {
		return mgr
	}
	p := lp.registry.Get(poolName)
	if p == nil {
		return nil
	}
	mgr := pool.NewPoolManager(p, lp.podClient, lp.namespace, lp.logger)
	lp.managers[poolName] = mgr
	return mgr
}

func (lp *labelPersister) PersistClaimLabels(ctx context.Context, poolName, podName, claimID string, expiresAt time.Time) {
	mgr := lp.getOrCreateManager(poolName)
	if mgr == nil {
		lp.logger.Error("cannot persist claim labels: pool not found", "pool", poolName)
		return
	}
	mgr.PersistClaimLabels(ctx, podName, claimID, expiresAt)
}

func (lp *labelPersister) PersistReleaseLabels(ctx context.Context, poolName, podName string) {
	mgr := lp.getOrCreateManager(poolName)
	if mgr == nil {
		lp.logger.Error("cannot persist release labels: pool not found", "pool", poolName)
		return
	}
	mgr.PersistReleaseLabels(ctx, podName)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func ptr[T any](v T) *T {
	return &v
}
