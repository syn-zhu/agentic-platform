package controller

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/siyanzhu/agentic-platform/pool-operator/internal/pool"
)

func TestReconciler_CreatesPoolInRegistry(t *testing.T) {
	registry := pool.NewRegistry()
	registry.CreateOrUpdate("test-pool", 5, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})

	p := registry.Get("test-pool")
	if p == nil {
		t.Fatal("expected pool to be created in registry")
	}
	status := p.Status()
	if status.Desired != 5 {
		t.Errorf("expected desired=5, got %d", status.Desired)
	}
}

func TestReconciler_UpdatesExistingPool(t *testing.T) {
	registry := pool.NewRegistry()
	registry.CreateOrUpdate("test-pool", 5, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})

	p := registry.Get("test-pool")
	p.AddAvailable(pool.PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})

	registry.CreateOrUpdate("test-pool", 10, 60*time.Second, 10*time.Minute, 20, corev1.PodTemplateSpec{})

	p = registry.Get("test-pool")
	status := p.Status()
	if status.Desired != 10 {
		t.Errorf("expected desired=10, got %d", status.Desired)
	}
	if status.Available != 1 {
		t.Error("expected pod-1 to still be available after update")
	}
}

func TestReconciler_DeleteRemovesFromRegistry(t *testing.T) {
	registry := pool.NewRegistry()
	registry.CreateOrUpdate("test-pool", 5, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})
	registry.Delete("test-pool")
	if registry.Get("test-pool") != nil {
		t.Fatal("expected pool to be removed from registry")
	}
}
