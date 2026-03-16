package pool

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func newTestPool(desired int) *Pool {
	return NewPool("test-pool", desired, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})
}

func TestClaim_Success(t *testing.T) {
	p := newTestPool(3)
	p.AddAvailable(PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})
	p.AddAvailable(PodInfo{Name: "pod-2", IP: "10.0.0.2", Port: 9090, CreatedAt: time.Now()})

	claim, err := p.Claim()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if claim.ClaimID == "" {
		t.Fatal("expected non-empty claim ID")
	}
	if claim.PodInfo.Name == "" {
		t.Fatal("expected pod info in claim")
	}

	status := p.Status()
	if status.Available != 1 {
		t.Errorf("expected 1 available, got %d", status.Available)
	}
	if status.Claimed != 1 {
		t.Errorf("expected 1 claimed, got %d", status.Claimed)
	}
}

func TestClaim_PoolExhausted(t *testing.T) {
	p := newTestPool(3)

	_, err := p.Claim()
	if err != ErrPoolExhausted {
		t.Fatalf("expected ErrPoolExhausted, got %v", err)
	}
}

func TestClaim_UniqueIDs(t *testing.T) {
	p := newTestPool(3)
	p.AddAvailable(PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})
	p.AddAvailable(PodInfo{Name: "pod-2", IP: "10.0.0.2", Port: 9090, CreatedAt: time.Now()})

	c1, _ := p.Claim()
	c2, _ := p.Claim()
	if c1.ClaimID == c2.ClaimID {
		t.Fatal("claim IDs should be unique")
	}
	if c1.PodInfo.Name == c2.PodInfo.Name {
		t.Fatal("should claim different pods")
	}
}

func TestRenew_Success(t *testing.T) {
	p := newTestPool(3)
	p.AddAvailable(PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})

	claim, _ := p.Claim()
	originalExpiry := claim.ExpiresAt

	time.Sleep(10 * time.Millisecond)
	newExpiry, err := p.Renew(claim.ClaimID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !newExpiry.After(originalExpiry) {
		t.Error("expected new expiry to be after original")
	}
}

func TestRenew_NotFound(t *testing.T) {
	p := newTestPool(3)

	_, err := p.Renew("nonexistent")
	if err != ErrClaimNotFound {
		t.Fatalf("expected ErrClaimNotFound, got %v", err)
	}
}

func TestRelease_Success(t *testing.T) {
	p := newTestPool(3)
	p.AddAvailable(PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})

	claim, _ := p.Claim()
	if p.Status().Available != 0 {
		t.Fatal("should have 0 available after claim")
	}

	released, err := p.Release(claim.ClaimID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if released.Name != "pod-1" {
		t.Errorf("expected pod-1, got %s", released.Name)
	}
	if p.Status().Available != 1 {
		t.Fatal("should have 1 available after release")
	}
	if p.Status().Claimed != 0 {
		t.Fatal("should have 0 claimed after release")
	}
}

func TestRelease_NotFound(t *testing.T) {
	p := newTestPool(3)

	_, err := p.Release("nonexistent")
	if err != ErrClaimNotFound {
		t.Fatalf("expected ErrClaimNotFound, got %v", err)
	}
}

func TestRelease_DoubleRelease(t *testing.T) {
	p := newTestPool(3)
	p.AddAvailable(PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})

	claim, _ := p.Claim()
	p.Release(claim.ClaimID)

	_, err := p.Release(claim.ClaimID)
	if err != ErrClaimNotFound {
		t.Fatalf("expected ErrClaimNotFound on double release, got %v", err)
	}
}

func TestSweepExpiredClaims(t *testing.T) {
	p := NewPool("test", 3, 1*time.Millisecond, 5*time.Minute, 10, corev1.PodTemplateSpec{})
	p.AddAvailable(PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})

	claim, _ := p.Claim()
	time.Sleep(5 * time.Millisecond)

	expired := p.SweepExpiredClaims()
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired, got %d", len(expired))
	}
	if expired[0] != claim.PodInfo.Name {
		t.Errorf("expected %s, got %s", claim.PodInfo.Name, expired[0])
	}
	if p.Status().Claimed != 0 {
		t.Error("claimed should be 0 after sweep")
	}
}

func TestSweepStaleWarming(t *testing.T) {
	p := NewPool("test", 3, 30*time.Second, 1*time.Millisecond, 10, corev1.PodTemplateSpec{})
	p.AddWarming("pod-1")

	time.Sleep(5 * time.Millisecond)

	stale := p.SweepStaleWarming()
	if len(stale) != 1 {
		t.Fatalf("expected 1 stale, got %d", len(stale))
	}
	if p.Status().Warming != 0 {
		t.Error("warming should be 0 after sweep")
	}
}

func TestScaleDecision_ScaleUp(t *testing.T) {
	p := newTestPool(3)
	toCreate, toDelete := p.ScaleDecision()
	if toCreate != 3 {
		t.Errorf("expected toCreate=3, got %d", toCreate)
	}
	if len(toDelete) != 0 {
		t.Errorf("expected no deletes, got %d", len(toDelete))
	}
}

func TestScaleDecision_MaxSurge(t *testing.T) {
	p := NewPool("test", 50, 30*time.Second, 5*time.Minute, 10, corev1.PodTemplateSpec{})
	toCreate, _ := p.ScaleDecision()
	if toCreate != 10 {
		t.Errorf("expected toCreate=10 (maxSurge), got %d", toCreate)
	}
}

func TestScaleDecision_ScaleDown(t *testing.T) {
	p := newTestPool(1)
	p.AddAvailable(PodInfo{Name: "pod-old", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now().Add(-10 * time.Minute)})
	p.AddAvailable(PodInfo{Name: "pod-mid", IP: "10.0.0.2", Port: 9090, CreatedAt: time.Now().Add(-5 * time.Minute)})
	p.AddAvailable(PodInfo{Name: "pod-new", IP: "10.0.0.3", Port: 9090, CreatedAt: time.Now()})

	toCreate, toDelete := p.ScaleDecision()
	if toCreate != 0 {
		t.Errorf("expected toCreate=0, got %d", toCreate)
	}
	if len(toDelete) != 2 {
		t.Fatalf("expected 2 deletes, got %d", len(toDelete))
	}
	if toDelete[0] != "pod-old" || toDelete[1] != "pod-mid" {
		t.Errorf("expected oldest pods deleted, got %v", toDelete)
	}
	if p.Status().Available != 1 {
		t.Errorf("expected 1 available after scale down, got %d", p.Status().Available)
	}
}

func TestScaleDecision_Balanced(t *testing.T) {
	p := newTestPool(3)
	p.AddAvailable(PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})
	p.AddWarming("pod-2")
	p.AddWarming("pod-3")
	toCreate, toDelete := p.ScaleDecision()
	if toCreate != 0 || len(toDelete) != 0 {
		t.Errorf("expected no action, got create=%d delete=%d", toCreate, len(toDelete))
	}
}

func TestRemovePod(t *testing.T) {
	p := newTestPool(3)
	p.AddAvailable(PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})
	p.AddWarming("pod-2")

	claim, _ := p.Claim()

	p.RemovePod(claim.PodInfo.Name)
	if p.Status().Claimed != 0 {
		t.Error("claimed pod should be removed")
	}

	p.RemovePod("pod-2")
	if p.Status().Warming != 0 {
		t.Error("warming pod should be removed")
	}
}

func TestPromoteWarming(t *testing.T) {
	p := newTestPool(3)
	p.AddWarming("pod-1")

	ok := p.PromoteWarming("pod-1", PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()})
	if !ok {
		t.Fatal("expected successful promotion")
	}
	if p.Status().Warming != 0 {
		t.Error("warming should be 0 after promotion")
	}
	if p.Status().Available != 1 {
		t.Error("available should be 1 after promotion")
	}

	ok = p.PromoteWarming("pod-nonexistent", PodInfo{})
	if ok {
		t.Fatal("expected false for nonexistent pod")
	}
}

func TestRestoreClaim(t *testing.T) {
	p := newTestPool(3)
	pod := PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()}

	expiresAt := time.Now().Add(30 * time.Second)
	p.RestoreClaim("clm-restored", pod, expiresAt)

	status := p.Status()
	if status.Claimed != 1 {
		t.Errorf("expected 1 claimed, got %d", status.Claimed)
	}

	claim := p.GetClaim("clm-restored")
	if claim == nil {
		t.Fatal("expected restored claim to exist")
	}
	if claim.PodInfo.Name != "pod-1" {
		t.Errorf("expected pod-1, got %s", claim.PodInfo.Name)
	}
	if !claim.ExpiresAt.Equal(expiresAt) {
		t.Error("expected restored ExpiresAt to match")
	}
}

func TestRestoreClaim_EmptyID(t *testing.T) {
	p := newTestPool(3)
	pod := PodInfo{Name: "pod-1", IP: "10.0.0.1", Port: 9090, CreatedAt: time.Now()}

	p.RestoreClaim("", pod, time.Now().Add(30*time.Second))

	status := p.Status()
	if status.Claimed != 1 {
		t.Errorf("expected 1 claimed, got %d", status.Claimed)
	}
}
