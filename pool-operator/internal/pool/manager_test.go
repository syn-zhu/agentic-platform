// pool-operator/internal/pool/manager_test.go
package pool

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type mockPodClient struct {
	created []string
	deleted []string
	patched map[string]map[string]string
	counter int
}

func newMockClient() *mockPodClient {
	return &mockPodClient{patched: make(map[string]map[string]string)}
}

func (m *mockPodClient) CreatePod(ctx context.Context, namespace string, pod *corev1.Pod) (*corev1.Pod, error) {
	m.counter++
	name := fmt.Sprintf("%s%d", pod.GenerateName, m.counter)
	m.created = append(m.created, name)
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}, nil
}

func (m *mockPodClient) DeletePod(ctx context.Context, namespace, name string) error {
	m.deleted = append(m.deleted, name)
	return nil
}

func (m *mockPodClient) PatchPodLabelsAndAnnotations(ctx context.Context, namespace, name string, labels map[string]string, annotations map[string]string) error {
	m.patched[name] = labels
	return nil
}

func TestManager_Reconcile_ScaleUp(t *testing.T) {
	p := NewPool("test", 3, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})
	client := newMockClient()
	mgr := NewPoolManager(p, client, "default", slog.New(slog.NewTextHandler(os.Stderr, nil)))

	mgr.Reconcile(context.Background())

	if len(client.created) != 3 {
		t.Errorf("expected 3 pods created, got %d", len(client.created))
	}
	if p.Status().Warming != 3 {
		t.Errorf("expected 3 warming, got %d", p.Status().Warming)
	}
}

func TestManager_Reconcile_ScaleDown(t *testing.T) {
	p := NewPool("test", 1, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})
	p.AddAvailable(PodInfo{Name: "pod-old", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now().Add(-10 * time.Minute)})
	p.AddAvailable(PodInfo{Name: "pod-new", IP: "10.0.0.2", Port: 9090, CreatedAt: time.Now()})

	client := newMockClient()
	mgr := NewPoolManager(p, client, "default", slog.New(slog.NewTextHandler(os.Stderr, nil)))

	mgr.Reconcile(context.Background())

	if len(client.deleted) != 1 {
		t.Fatalf("expected 1 pod deleted, got %d", len(client.deleted))
	}
	if client.deleted[0] != "pod-old" {
		t.Errorf("expected oldest pod deleted, got %s", client.deleted[0])
	}
}

func TestManager_Reconcile_SweepExpired(t *testing.T) {
	p := NewPool("test", 3, 1*time.Millisecond, 5*time.Minute, 10, corev1.PodTemplateSpec{})
	p.AddAvailable(PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})
	p.Claim()

	time.Sleep(5 * time.Millisecond)

	client := newMockClient()
	mgr := NewPoolManager(p, client, "default", slog.New(slog.NewTextHandler(os.Stderr, nil)))

	mgr.Reconcile(context.Background())

	foundExpiredDelete := false
	for _, name := range client.deleted {
		if name == "pod-1" {
			foundExpiredDelete = true
		}
	}
	if !foundExpiredDelete {
		t.Error("expected expired pod to be deleted")
	}
}
