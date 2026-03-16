// pool-operator/internal/informer/pod_watcher.go
package informer

import (
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/siyanzhu/agentic-platform/pool-operator/internal/labels"
	"github.com/siyanzhu/agentic-platform/pool-operator/internal/pool"
)

// PodWatcher handles pod events and updates pool state accordingly.
type PodWatcher struct {
	registry *pool.Registry
	logger   *slog.Logger
}

func NewPodWatcher(registry *pool.Registry, logger *slog.Logger) *PodWatcher {
	return &PodWatcher{registry: registry, logger: logger}
}

// EventHandler returns a cache.ResourceEventHandlerFuncs for the pod informer.
func (w *PodWatcher) EventHandler() cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		UpdateFunc: w.onUpdate,
		DeleteFunc: w.onDelete,
	}
}

func (w *PodWatcher) onUpdate(oldObj, newObj interface{}) {
	pod, ok := newObj.(*corev1.Pod)
	if !ok {
		return
	}

	poolName := pod.Labels[labels.LabelPool]
	if poolName == "" {
		return
	}

	p := w.registry.Get(poolName)
	if p == nil {
		return
	}

	status := pod.Labels[labels.LabelStatus]

	// If pod is warming and now Ready, promote to available
	if status == labels.StatusWarming && isPodReady(pod) {
		podInfo := pool.PodInfoFromPod(pod)
		if p.PromoteWarming(pod.Name, podInfo) {
			w.logger.Info("promoted warming pod to available",
				"pool", poolName, "pod", pod.Name)
		}
	}
}

func (w *PodWatcher) onDelete(obj interface{}) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			return
		}
		pod, ok = tombstone.Obj.(*corev1.Pod)
		if !ok {
			return
		}
	}

	poolName := pod.Labels[labels.LabelPool]
	if poolName == "" {
		return
	}

	p := w.registry.Get(poolName)
	if p == nil {
		return
	}

	p.RemovePod(pod.Name)
	w.logger.Info("removed deleted pod from pool",
		"pool", poolName, "pod", pod.Name)
}

func isPodReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

