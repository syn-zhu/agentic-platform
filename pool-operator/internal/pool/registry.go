package pool

import (
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// Registry manages multiple named pools.
//
// Lock ordering: Registry.mu is always acquired before Pool.mu.
// Never acquire a Pool lock then call a Registry method.
type Registry struct {
	mu    sync.RWMutex
	pools map[string]*Pool
}

func NewRegistry() *Registry {
	return &Registry{
		pools: make(map[string]*Pool),
	}
}

func (r *Registry) Get(name string) *Pool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.pools[name]
}

func (r *Registry) CreateOrUpdate(name string, desired int, leaseTTL, warmingTimeout time.Duration, maxSurge int, podTemplate corev1.PodTemplateSpec) *Pool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if p, ok := r.pools[name]; ok {
		p.UpdateConfig(desired, leaseTTL, warmingTimeout, maxSurge, podTemplate)
		return p
	}

	p := NewPool(name, desired, leaseTTL, warmingTimeout, maxSurge, podTemplate)
	r.pools[name] = p
	return p
}

func (r *Registry) Delete(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pools, name)
}

func (r *Registry) List() []*Pool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*Pool, 0, len(r.pools))
	for _, p := range r.pools {
		result = append(result, p)
	}
	return result
}
