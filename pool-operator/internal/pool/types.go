// pool-operator/internal/pool/types.go
package pool

import (
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// PodInfo represents a pod in the pool.
type PodInfo struct {
	Name      string
	IP        string
	Port      int32
	CreatedAt time.Time
}

// Claim represents an active claim on a pod.
type Claim struct {
	ClaimID   string
	PodInfo   PodInfo
	ExpiresAt time.Time
	LeaseTTL  time.Duration
}

// Pool manages the state for a single ExecutorPool.
type Pool struct {
	mu sync.Mutex

	name           string
	desired        int
	leaseTTL       time.Duration
	warmingTimeout time.Duration
	maxSurge       int
	podTemplate    corev1.PodTemplateSpec

	available []PodInfo
	claimed   map[string]*Claim    // keyed by claim ID
	warming   map[string]time.Time // keyed by pod name, value is creation time
}

// PoolStatus is a snapshot of pool state for observability.
type PoolStatus struct {
	Desired   int `json:"desired"`
	Available int `json:"available"`
	Claimed   int `json:"claimed"`
	Warming   int `json:"warming"`
}

// PodInfoFromPod extracts PodInfo from a Kubernetes Pod object.
// This is the single source of truth for pod-to-PodInfo conversion.
func PodInfoFromPod(pod *corev1.Pod) PodInfo {
	var port int32 = 9090 // default
	if len(pod.Spec.Containers) > 0 {
		for _, p := range pod.Spec.Containers[0].Ports {
			if p.Name == "http" {
				port = p.ContainerPort
				break
			}
		}
	}
	return PodInfo{
		Name:      pod.Name,
		IP:        pod.Status.PodIP,
		Port:      port,
		CreatedAt: pod.CreationTimestamp.Time,
	}
}

// NewPool creates a new Pool with the given configuration.
func NewPool(name string, desired int, leaseTTL, warmingTimeout time.Duration, maxSurge int, podTemplate corev1.PodTemplateSpec) *Pool {
	return &Pool{
		name:           name,
		desired:        desired,
		leaseTTL:       leaseTTL,
		warmingTimeout: warmingTimeout,
		maxSurge:       maxSurge,
		podTemplate:    podTemplate,
		available:      make([]PodInfo, 0),
		claimed:        make(map[string]*Claim),
		warming:        make(map[string]time.Time),
	}
}
