package collect

import (
	"context"
	"net"
	"testing"
	"time"
)

// A nameserver that never answers is a normal failure mode: an expired
// domain, a partitioned network, a misconfigured internal resolver. Guard
// performs name lookups, so it must never let one park a goroutine.
//
// CheckURL is called synchronously from webCollector.Collect, which runs on
// the scheduler's one goroutine per target. That goroutine's context is
// cancelled when the target is deleted, disabled, or the hub shuts down. If
// the lookup ignored that context, the goroutine would outlive all three
// and Scheduler.Run's wg.Wait() could never return, so shutdown would hang
// forever.
func TestSecurity_CheckURLHonoursCallerCancellation(t *testing.T) {
	block := make(chan struct{}) // deliberately never closed
	restore := swapDefaultResolver(&net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			select {
			case <-block:
			case <-ctx.Done():
			}
			return nil, ctx.Err()
		},
	})
	defer restore()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- NewGuard().CheckURL(ctx, "http://stalls.invalid/")
	}()

	// Nothing should have completed while the resolver is stalled.
	select {
	case err := <-done:
		t.Fatalf("CheckURL returned %v before the resolver answered or the context was cancelled", err)
	case <-time.After(100 * time.Millisecond):
	}

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("CheckURL reported success for a lookup that never completed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CheckURL ignored its caller's cancellation: a target with an unresponsive " +
			"nameserver would strand its scheduler goroutine past target deletion and block shutdown")
	}
}

// Even a caller that supplies no deadline of its own must not be able to
// block forever, so Guard applies its own bound.
func TestSecurity_CheckURLBoundsAnUndeadlinedCaller(t *testing.T) {
	if resolveTimeout > 30*time.Second {
		t.Fatalf("resolveTimeout is %v, too long to bound a stalled lookup usefully", resolveTimeout)
	}

	block := make(chan struct{})
	restore := swapDefaultResolver(&net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			select {
			case <-block:
			case <-ctx.Done():
			}
			return nil, ctx.Err()
		},
	})
	defer restore()

	// context.Background() carries no deadline, so only Guard's own
	// timeout can end this.
	done := make(chan error, 1)
	go func() {
		done <- NewGuard().CheckURL(context.Background(), "http://stalls.invalid/")
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("CheckURL reported success for a lookup that never completed")
		}
	case <-time.After(resolveTimeout + 3*time.Second):
		t.Fatalf("CheckURL did not return within its own %v timeout", resolveTimeout)
	}
}
