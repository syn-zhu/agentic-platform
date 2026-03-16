package pool

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func TestRegistry_CreateAndGet(t *testing.T) {
	r := NewRegistry()
	r.CreateOrUpdate("test-pool", 5, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})

	p := r.Get("test-pool")
	if p == nil {
		t.Fatal("expected pool to exist")
	}
	if p.Name() != "test-pool" {
		t.Errorf("expected name test-pool, got %s", p.Name())
	}
	status := p.Status()
	if status.Desired != 5 {
		t.Errorf("expected desired 5, got %d", status.Desired)
	}
}

func TestRegistry_GetNonexistent(t *testing.T) {
	r := NewRegistry()
	if r.Get("nope") != nil {
		t.Fatal("expected nil for nonexistent pool")
	}
}

func TestRegistry_Delete(t *testing.T) {
	r := NewRegistry()
	r.CreateOrUpdate("test-pool", 5, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})
	r.Delete("test-pool")
	if r.Get("test-pool") != nil {
		t.Fatal("expected pool to be deleted")
	}
}

func TestRegistry_List(t *testing.T) {
	r := NewRegistry()
	r.CreateOrUpdate("pool-a", 3, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})
	r.CreateOrUpdate("pool-b", 5, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})

	pools := r.List()
	if len(pools) != 2 {
		t.Fatalf("expected 2 pools, got %d", len(pools))
	}
}

func TestRegistry_UpdateExisting(t *testing.T) {
	r := NewRegistry()
	r.CreateOrUpdate("test-pool", 5, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})

	p := r.Get("test-pool")
	p.AddAvailable(PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})

	r.CreateOrUpdate("test-pool", 10, 60*time.Second, 10*time.Minute, 20, corev1.PodTemplateSpec{})

	p = r.Get("test-pool")
	status := p.Status()
	if status.Desired != 10 {
		t.Errorf("expected desired 10, got %d", status.Desired)
	}
	if status.Available != 1 {
		t.Error("expected pod-1 to still be available after config update")
	}
}

func TestRegistry_UpdatePodTemplate(t *testing.T) {
	r := NewRegistry()
	oldTemplate := corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "old"}},
		},
	}
	r.CreateOrUpdate("test-pool", 5, 30*time.Second, 5*time.Minute, 10, oldTemplate)

	newTemplate := corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "new"}},
		},
	}
	r.CreateOrUpdate("test-pool", 5, 30*time.Second, 5*time.Minute, 10, newTemplate)

	p := r.Get("test-pool")
	p.mu.Lock()
	containerName := p.podTemplate.Spec.Containers[0].Name
	p.mu.Unlock()
	if containerName != "new" {
		t.Errorf("expected updated template container name 'new', got '%s'", containerName)
	}
}
