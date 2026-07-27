package fetch

import (
	"context"
	"net/url"
	"sync"
	"time"
)

// HostLimiter serializes requests per host and enforces a minimum interval
// between them, so that fetching twenty feeds from one site does not look like
// an attack. It is safe for concurrent use.
type HostLimiter struct {
	interval time.Duration

	mu    sync.Mutex
	gates map[string]chan struct{}
	next  map[string]time.Time
	// now is swappable in tests.
	now func() time.Time
}

// NewHostLimiter returns a limiter that allows at most one request per host per
// interval. A non-positive interval disables waiting but still serializes.
func NewHostLimiter(interval time.Duration) *HostLimiter {
	return &HostLimiter{
		interval: interval,
		gates:    make(map[string]chan struct{}),
		next:     make(map[string]time.Time),
		now:      time.Now,
	}
}

// hostKey extracts the host used for limiting. Unparseable URLs share a single
// bucket keyed by the raw string, which is conservative but harmless.
func hostKey(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	return u.Host
}

// Acquire blocks until the caller may issue a request to rawURL's host, or the
// context is done. The returned release function must be called when the
// request completes.
func (l *HostLimiter) Acquire(ctx context.Context, rawURL string) (release func(), err error) {
	if l == nil {
		return func() {}, nil
	}
	key := hostKey(rawURL)

	l.mu.Lock()
	gate, ok := l.gates[key]
	if !ok {
		gate = make(chan struct{}, 1)
		l.gates[key] = gate
	}
	l.mu.Unlock()

	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	l.mu.Lock()
	wait := time.Duration(0)
	if t, ok := l.next[key]; ok {
		if d := t.Sub(l.now()); d > 0 {
			wait = d
		}
	}
	l.mu.Unlock()

	if wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			<-gate
			return nil, ctx.Err()
		}
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			l.next[key] = l.now().Add(l.interval)
			l.mu.Unlock()
			<-gate
		})
	}, nil
}
