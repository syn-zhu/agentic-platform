package engine

import (
	"context"
	"sync"
)

var waiters sync.Map // key: "ns/name" -> *waiter

type waiter struct{ ch chan *v1.SandboxClaim }

// gRPC handler: "give me a ready sandbox"
func (s *Server) Acquire(ctx context.Context, req *pb.AcquireRequest) (*pb.AcquireResponse, error) {
	sc := buildSandboxClaim(req)
	key := sc.Namespace + "/" + sc.Name

	w := &waiter{ch: make(chan *v1.SandboxClaim, 1)}
	waiters.Store(key, w)
	defer waiters.Delete(key)

	if _, err := s.client.Apply(ctx, sc, applyOpts); err != nil {
		return nil, err
	}

	// Race-safe: check the cache *after* registering.
	if cur, err := s.lister.SandboxClaims(sc.Namespace).Get(sc.Name); err == nil && isReady(cur) {
		return toResp(cur), nil
	}

	select {
	case ready := <-w.ch:
		return toResp(ready), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Informer handler, registered once at startup
func onUpdate(_, newObj interface{}) {
	sc := newObj.(*v1.SandboxClaim)
	if !isReady(sc) {
		return
	}
	if v, ok := waiters.Load(sc.Namespace + "/" + sc.Name); ok {
		select {
		case v.(*waiter).ch <- sc:
		default: // buffered 1 + default = already signaled, drop
		}
	}
}
