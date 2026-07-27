package fetch

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestHostKey(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://example.com/feed.xml", "example.com"},
		{"https://example.com:8080/feed", "example.com:8080"},
		{"not a url", "not a url"},
	}
	for _, tc := range tests {
		if got := hostKey(tc.in); got != tc.want {
			t.Errorf("hostKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHostLimiterSerializesPerHost(t *testing.T) {
	l := NewHostLimiter(0)
	var (
		mu    sync.Mutex
		peak  int
		cur   int
		wg    sync.WaitGroup
		ctx   = context.Background()
		total = 8
	)
	wg.Add(total)
	for i := 0; i < total; i++ {
		go func() {
			defer wg.Done()
			release, err := l.Acquire(ctx, "https://same.example/feed")
			if err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			mu.Lock()
			cur++
			if cur > peak {
				peak = cur
			}
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
			mu.Lock()
			cur--
			mu.Unlock()
			release()
		}()
	}
	wg.Wait()
	if peak != 1 {
		t.Errorf("peak concurrency for one host = %d, want 1", peak)
	}
}

func TestHostLimiterEnforcesInterval(t *testing.T) {
	l := NewHostLimiter(40 * time.Millisecond)
	ctx := context.Background()
	start := time.Now()
	for i := 0; i < 3; i++ {
		release, err := l.Acquire(ctx, "https://slow.example/feed")
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		release()
	}
	// The first acquisition is free; the next two each wait one interval.
	if elapsed := time.Since(start); elapsed < 70*time.Millisecond {
		t.Errorf("three acquisitions took %v, expected at least two intervals", elapsed)
	}
}

func TestHostLimiterDoesNotBlockOtherHosts(t *testing.T) {
	l := NewHostLimiter(500 * time.Millisecond)
	ctx := context.Background()
	r1, err := l.Acquire(ctx, "https://a.example/feed")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		r2, err := l.Acquire(ctx, "https://b.example/feed")
		if err != nil {
			t.Errorf("Acquire b: %v", err)
			return
		}
		r2()
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Error("a different host was blocked by the first host's slot")
	}
	r1()
}

func TestHostLimiterRespectsContext(t *testing.T) {
	l := NewHostLimiter(time.Hour)
	ctx := context.Background()
	release, err := l.Acquire(ctx, "https://busy.example/feed")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release() // schedules the next slot an hour out

	ctx2, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := l.Acquire(ctx2, "https://busy.example/feed"); err == nil {
		t.Fatal("expected the second acquisition to be cancelled")
	}

	// The gate must have been handed back so later callers are not deadlocked.
	ctx3, cancel3 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel3()
	if _, err := l.Acquire(ctx3, "https://busy.example/feed"); err == nil {
		t.Fatal("expected the third acquisition to be cancelled too")
	}
}

func TestNilLimiterIsUsable(t *testing.T) {
	var l *HostLimiter
	release, err := l.Acquire(context.Background(), "https://x.example/f")
	if err != nil {
		t.Fatalf("Acquire on a nil limiter: %v", err)
	}
	release()
}

func TestReleaseIsIdempotent(t *testing.T) {
	l := NewHostLimiter(0)
	release, err := l.Acquire(context.Background(), "https://x.example/f")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()
	release() // must not panic or corrupt the gate

	done := make(chan struct{})
	go func() {
		defer close(done)
		r, err := l.Acquire(context.Background(), "https://x.example/f")
		if err == nil {
			r()
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the limiter deadlocked after a double release")
	}
}
