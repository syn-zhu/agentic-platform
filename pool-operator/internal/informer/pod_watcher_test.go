// pool-operator/internal/informer/pod_watcher_test.go
package informer

import (
	"log/slog"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/siyanzhu/agentic-platform/pool-operator/internal/labels"
	"github.com/siyanzhu/agentic-platform/pool-operator/internal/pool"
)

func TestOnUpdate_PromotesWarmingToAvailable(t *testing.T) {
	registry := pool.NewRegistry()
	p := registry.CreateOrUpdate("test-pool", 3, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})
	p.AddWarming("pod-1")

	watcher := NewPodWatcher(registry, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pod-1",
			CreationTimestamp: metav1.Now(),
			Labels: map[string]string{
				labels.LabelPool:   "test-pool",
				labels.LabelStatus: labels.StatusWarming,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 9090}},
			}},
		},
		Status: corev1.PodStatus{
			PodIP: "10.0.0.1",
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}

	watcher.onUpdate(nil, pod)

	status := p.Status()
	if status.Warming != 0 {
		t.Errorf("expected warming=0, got %d", status.Warming)
	}
	if status.Available != 1 {
		t.Errorf("expected available=1, got %d", status.Available)
	}
}

func TestOnDelete_RemovesPod(t *testing.T) {
	registry := pool.NewRegistry()
	p := registry.CreateOrUpdate("test-pool", 3, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})
	p.AddAvailable(pool.PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})

	watcher := NewPodWatcher(registry, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pod-1",
			Labels: map[string]string{
				labels.LabelPool: "test-pool",
			},
		},
	}

	watcher.onDelete(pod)

	if p.Status().Available != 0 {
		t.Error("expected available=0 after delete")
	}
}
