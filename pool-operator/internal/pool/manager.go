// pool-operator/internal/pool/manager.go
package pool

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/siyanzhu/agentic-platform/pool-operator/internal/labels"
)

// PodClient abstracts K8s pod operations for testability.
type PodClient interface {
	CreatePod(ctx context.Context, namespace string, pod *corev1.Pod) (*corev1.Pod, error)
	DeletePod(ctx context.Context, namespace, name string) error
	PatchPodLabelsAndAnnotations(ctx context.Context, namespace, name string, labels map[string]string, annotations map[string]string) error
}

// PoolManager handles the reconcile loop for a single pool.
type PoolManager struct {
	pool      *Pool
	client    PodClient
	namespace string
	logger    *slog.Logger
}

func NewPoolManager(p *Pool, client PodClient, namespace string, logger *slog.Logger) *PoolManager {
	return &PoolManager{
		pool:      p,
		client:    client,
		namespace: namespace,
		logger:    logger,
	}
}

// Reconcile runs one cycle of pool management: scale up/down + sweeps.
func (m *PoolManager) Reconcile(ctx context.Context) {
	// 1. Sweep expired claims
	expired := m.pool.SweepExpiredClaims()
	for _, podName := range expired {
		m.logger.Warn("lease expired, deleting pod", "pool", m.pool.Name(), "pod", podName)
		if err := m.client.DeletePod(ctx, m.namespace, podName); err != nil {
			m.logger.Error("failed to delete expired pod", "pod", podName, "err", err)
		}
	}

	// 2. Sweep stale warming
	stale := m.pool.SweepStaleWarming()
	for _, podName := range stale {
		m.logger.Warn("warming timeout, deleting pod", "pool", m.pool.Name(), "pod", podName)
		if err := m.client.DeletePod(ctx, m.namespace, podName); err != nil {
			m.logger.Error("failed to delete stale warming pod", "pod", podName, "err", err)
		}
	}

	// 3. Scale decision
	toCreate, toDelete := m.pool.ScaleDecision()

	for _, podName := range toDelete {
		m.logger.Info("scaling down, deleting pod", "pool", m.pool.Name(), "pod", podName)
		if err := m.client.DeletePod(ctx, m.namespace, podName); err != nil {
			m.logger.Error("failed to delete pod for scale down", "pod", podName, "err", err)
		}
	}

	for i := 0; i < toCreate; i++ {
		pod := m.buildPod()
		created, err := m.client.CreatePod(ctx, m.namespace, pod)
		if err != nil {
			m.logger.Error("failed to create pod", "pool", m.pool.Name(), "err", err)
			continue
		}
		m.pool.AddWarming(created.Name)
		m.logger.Info("created pool pod", "pool", m.pool.Name(), "pod", created.Name)
	}
}

// PersistClaimLabels asynchronously updates pod labels/annotations after a claim.
func (m *PoolManager) PersistClaimLabels(ctx context.Context, podName, claimID string, expiresAt time.Time) {
	lbls := map[string]string{
		labels.LabelStatus:  labels.StatusClaimed,
		labels.LabelClaimID: claimID,
	}
	anns := map[string]string{
		labels.AnnotationLeaseExpiresAt: expiresAt.Format(time.RFC3339),
	}
	if err := m.client.PatchPodLabelsAndAnnotations(ctx, m.namespace, podName, lbls, anns); err != nil {
		m.logger.Error("failed to persist claim labels", "pod", podName, "err", err)
	}
}

// PersistReleaseLabels asynchronously updates pod labels after a release.
func (m *PoolManager) PersistReleaseLabels(ctx context.Context, podName string) {
	lbls := map[string]string{
		labels.LabelStatus:  labels.StatusAvailable,
		labels.LabelClaimID: "",
	}
	anns := map[string]string{
		labels.AnnotationLeaseExpiresAt: "",
	}
	if err := m.client.PatchPodLabelsAndAnnotations(ctx, m.namespace, podName, lbls, anns); err != nil {
		m.logger.Error("failed to persist release labels", "pod", podName, "err", err)
	}
}

func (m *PoolManager) buildPod() *corev1.Pod {
	m.pool.mu.Lock()
	template := m.pool.podTemplate.DeepCopy()
	poolName := m.pool.name
	m.pool.mu.Unlock()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-", poolName),
			Namespace:    m.namespace,
			Labels: mergeLabels(template.Labels, map[string]string{
				labels.LabelPool:   poolName,
				labels.LabelStatus: labels.StatusWarming,
			}),
		},
		Spec: template.Spec,
	}
	return pod
}

func mergeLabels(base, override map[string]string) map[string]string {
	result := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range override {
		result[k] = v
	}
	return result
}
