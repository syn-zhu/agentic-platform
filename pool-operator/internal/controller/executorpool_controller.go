// pool-operator/internal/controller/executorpool_controller.go
package controller

import (
	"context"
	"log/slog"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	v1alpha1 "github.com/siyanzhu/agentic-platform/pool-operator/api/v1alpha1"
	"github.com/siyanzhu/agentic-platform/pool-operator/internal/pool"
)

const finalizerName = "pool.agentic.dev/pool-cleanup"

type ExecutorPoolReconciler struct {
	client.Client
	registry *pool.Registry
	logger   *slog.Logger
}

func NewExecutorPoolReconciler(c client.Client, registry *pool.Registry, logger *slog.Logger) *ExecutorPoolReconciler {
	return &ExecutorPoolReconciler{
		Client:   c,
		registry: registry,
		logger:   logger,
	}
}

func (r *ExecutorPoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ExecutorPool{}).
		Complete(r)
}

func (r *ExecutorPoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var ep v1alpha1.ExecutorPool
	if err := r.Get(ctx, req.NamespacedName, &ep); err != nil {
		if client.IgnoreNotFound(err) == nil {
			r.registry.Delete(req.Name)
			r.logger.Info("pool removed from registry (CR deleted)", "pool", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion with finalizer
	if !ep.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&ep, finalizerName) {
			r.logger.Info("pool CR being deleted, cleaning up", "pool", ep.Name)

			p := r.registry.Get(ep.Name)
			if p != nil {
				p.UpdateConfig(0, ep.Spec.LeaseTTL.Duration, ep.Spec.WarmingTimeout.Duration, int(ep.Spec.MaxSurge), ep.Spec.PodTemplate)
			}
			r.registry.Delete(ep.Name)

			controllerutil.RemoveFinalizer(&ep, finalizerName)
			if err := r.Update(ctx, &ep); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure finalizer
	if !controllerutil.ContainsFinalizer(&ep, finalizerName) {
		controllerutil.AddFinalizer(&ep, finalizerName)
		if err := r.Update(ctx, &ep); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Create or update pool in registry
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

	r.registry.CreateOrUpdate(
		ep.Name,
		int(ep.Spec.Desired),
		leaseTTL,
		warmingTimeout,
		maxSurge,
		ep.Spec.PodTemplate,
	)

	r.logger.Info("pool config synced", "pool", ep.Name, "desired", ep.Spec.Desired)

	// Update status
	p := r.registry.Get(ep.Name)
	if p != nil {
		status := p.Status()
		ep.Status.Available = int32(status.Available)
		ep.Status.Claimed = int32(status.Claimed)
		ep.Status.Warming = int32(status.Warming)

		readyCondition := metav1.Condition{
			Type:               "PoolReady",
			LastTransitionTime: metav1.Now(),
		}
		if status.Available > 0 {
			readyCondition.Status = metav1.ConditionTrue
			readyCondition.Reason = "PodsAvailable"
			readyCondition.Message = "Pool has available pods"
		} else {
			readyCondition.Status = metav1.ConditionFalse
			readyCondition.Reason = "NoPodsAvailable"
			readyCondition.Message = "Pool has no available pods"
		}
		meta.SetStatusCondition(&ep.Status.Conditions, readyCondition)

		if err := r.Status().Update(ctx, &ep); err != nil {
			r.logger.Error("failed to update status", "pool", ep.Name, "err", err)
		}
	}

	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}
