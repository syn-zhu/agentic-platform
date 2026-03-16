// pool-operator/internal/pool/pool.go
package pool

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
)

var (
	ErrPoolExhausted = errors.New("no available pods")
	ErrClaimNotFound = errors.New("claim not found")
)

// Claim pops a pod from available and returns a new Claim.
func (p *Pool) Claim() (*Claim, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.available) == 0 {
		return nil, ErrPoolExhausted
	}

	// Pop last element (O(1))
	pod := p.available[len(p.available)-1]
	p.available = p.available[:len(p.available)-1]

	claimID := generateClaimID()
	claim := &Claim{
		ClaimID:   claimID,
		PodInfo:   pod,
		ExpiresAt: time.Now().Add(p.leaseTTL),
		LeaseTTL:  p.leaseTTL,
	}
	p.claimed[claimID] = claim

	copy := *claim
	return &copy, nil
}

// Renew extends the lease on an existing claim.
func (p *Pool) Renew(claimID string) (time.Time, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	claim, ok := p.claimed[claimID]
	if !ok {
		return time.Time{}, ErrClaimNotFound
	}

	claim.ExpiresAt = time.Now().Add(claim.LeaseTTL)
	return claim.ExpiresAt, nil
}

// Release returns a claimed pod to the available pool.
func (p *Pool) Release(claimID string) (PodInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	claim, ok := p.claimed[claimID]
	if !ok {
		return PodInfo{}, ErrClaimNotFound
	}

	delete(p.claimed, claimID)
	p.available = append(p.available, claim.PodInfo)
	return claim.PodInfo, nil
}

// Status returns a snapshot of the pool state.
func (p *Pool) Status() PoolStatus {
	p.mu.Lock()
	defer p.mu.Unlock()

	return PoolStatus{
		Desired:   p.desired,
		Available: len(p.available),
		Claimed:   len(p.claimed),
		Warming:   len(p.warming),
	}
}

// AddAvailable adds a pod to the available list.
func (p *Pool) AddAvailable(pod PodInfo) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.available = append(p.available, pod)
}

// AddWarming adds a pod to the warming set.
func (p *Pool) AddWarming(podName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.warming[podName] = time.Now()
}

// PromoteWarming moves a pod from warming to available.
func (p *Pool) PromoteWarming(podName string, pod PodInfo) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.warming[podName]; !ok {
		return false
	}
	delete(p.warming, podName)
	p.available = append(p.available, pod)
	return true
}

// RemovePod removes a pod from whichever set it's in.
func (p *Pool) RemovePod(podName string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.warming, podName)

	for i, pod := range p.available {
		if pod.Name == podName {
			p.available = append(p.available[:i], p.available[i+1:]...)
			break
		}
	}

	for id, claim := range p.claimed {
		if claim.PodInfo.Name == podName {
			delete(p.claimed, id)
			break
		}
	}
}

// SweepExpiredClaims returns pod names of expired claims and removes them.
func (p *Pool) SweepExpiredClaims() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	var expired []string

	for id, claim := range p.claimed {
		if now.After(claim.ExpiresAt) {
			expired = append(expired, claim.PodInfo.Name)
			delete(p.claimed, id)
		}
	}
	return expired
}

// SweepStaleWarming returns pod names that have been warming longer than the timeout.
func (p *Pool) SweepStaleWarming() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	var stale []string

	for podName, createdAt := range p.warming {
		if now.Sub(createdAt) > p.warmingTimeout {
			stale = append(stale, podName)
			delete(p.warming, podName)
		}
	}
	return stale
}

// ScaleDecision returns how many pods to create (positive) or which to delete (names).
// It does NOT modify the available list; callers must call RemoveAvailable after
// successfully deleting each pod.
func (p *Pool) ScaleDecision() (scaleUp int, scaleDown []string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	supply := len(p.available) + len(p.warming)
	deficit := p.desired - supply

	if deficit > 0 {
		if deficit > p.maxSurge {
			deficit = p.maxSurge
		}
		return deficit, nil
	}

	if deficit < 0 {
		removeCount := -deficit
		if removeCount > len(p.available) {
			removeCount = len(p.available)
		}

		if removeCount == 0 {
			return 0, nil
		}

		sort.Slice(p.available, func(i, j int) bool {
			return p.available[i].CreatedAt.Before(p.available[j].CreatedAt)
		})

		// Return names to remove but do NOT modify p.available here
		toRemove := make([]string, removeCount)
		for i := 0; i < removeCount; i++ {
			toRemove[i] = p.available[i].Name
		}
		return 0, toRemove
	}

	return 0, nil
}

// RemoveAvailable removes a pod from the available list. Called after successful deletion.
func (p *Pool) RemoveAvailable(podName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, pod := range p.available {
		if pod.Name == podName {
			p.available = append(p.available[:i], p.available[i+1:]...)
			return
		}
	}
}

// UpdateConfig updates the pool's configuration.
func (p *Pool) UpdateConfig(desired int, leaseTTL, warmingTimeout time.Duration, maxSurge int, podTemplate corev1.PodTemplateSpec) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.desired = desired
	p.leaseTTL = leaseTTL
	p.warmingTimeout = warmingTimeout
	p.maxSurge = maxSurge
	p.podTemplate = podTemplate
}

// Name returns the pool name. Safe to call without lock since name is immutable after construction.
func (p *Pool) Name() string {
	return p.name
}

// LeaseTTL returns the pool's lease TTL.
func (p *Pool) LeaseTTL() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.leaseTTL
}

// GetClaim returns a claim by ID, or nil if not found.
func (p *Pool) GetClaim(claimID string) *Claim {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.claimed[claimID]
	if !ok {
		return nil
	}
	copy := *c
	return &copy
}

// RestoreClaim adds a claim directly (used for state rebuild after failover).
func (p *Pool) RestoreClaim(claimID string, pod PodInfo, expiresAt time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if claimID == "" {
		claimID = generateClaimID()
	}
	p.claimed[claimID] = &Claim{
		ClaimID:   claimID,
		PodInfo:   pod,
		ExpiresAt: expiresAt,
		LeaseTTL:  p.leaseTTL,
	}
}

func generateClaimID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "clm-" + hex.EncodeToString(b)
}
