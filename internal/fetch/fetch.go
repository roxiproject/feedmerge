// Package fetch retrieves feed documents over HTTP with conditional GET
// caching, per-host rate limiting and a bounded worker pool.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/roxiproject/feedmerge/internal/feed"
)

// DefaultUserAgent is sent unless the caller overrides it.
const DefaultUserAgent = "feedmerge/1.0 (+https://github.com/roxiproject/feedmerge)"

// maxBodyBytes caps how much of a response we will read, so a hostile or broken
// upstream cannot exhaust memory.
const maxBodyBytes = 16 << 20

// cacheEntry stores what is needed to revalidate a feed.
type cacheEntry struct {
	etag         string
	lastModified string
	feed         *feed.Feed
	fetchedAt    time.Time
}

// Cache holds validators and parsed documents keyed by feed URL.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
}

// NewCache returns an empty cache.
func NewCache() *Cache { return &Cache{entries: make(map[string]cacheEntry)} }

func (c *Cache) get(url string) (cacheEntry, bool) {
	if c == nil {
		return cacheEntry{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[url]
	return e, ok
}

func (c *Cache) put(url string, e cacheEntry) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[url] = e
}

// Len reports how many feeds are cached.
func (c *Cache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Fetcher fetches and parses feeds.
type Fetcher struct {
	Client    *http.Client
	UserAgent string
	Cache     *Cache
	Limiter   *HostLimiter
	// Timeout bounds a single feed fetch. Zero means no per-feed bound beyond
	// the caller's context.
	Timeout time.Duration
}

// New returns a Fetcher with sensible defaults.
func New(timeout, hostInterval time.Duration) *Fetcher {
	return &Fetcher{
		Client: &http.Client{
			Timeout: 0, // per-request contexts carry the deadline
			Transport: &http.Transport{
				Proxy:               http.ProxyFromEnvironment,
				MaxIdleConns:        64,
				MaxIdleConnsPerHost: 4,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		UserAgent: DefaultUserAgent,
		Cache:     NewCache(),
		Limiter:   NewHostLimiter(hostInterval),
		Timeout:   timeout,
	}
}

// Result is the outcome of fetching one feed.
type Result struct {
	URL string
	// Name is the configured display name of the source, if any.
	Name string
	Feed *feed.Feed
	// NotModified is true when the upstream answered 304 and the cached copy
	// was reused.
	NotModified bool
	Status      int
	Err         error
	Duration    time.Duration
}

// HTTPError reports a non-success status from an upstream feed.
type HTTPError struct {
	URL    string
	Status int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("fetch %s: unexpected status %d %s", e.URL, e.Status, http.StatusText(e.Status))
}

// Fetch retrieves a single feed. On 304 it returns the cached parsed feed. All
// failures are returned as errors; the function never panics on malformed
// input.
func (f *Fetcher) Fetch(ctx context.Context, url string) (*feed.Feed, bool, int, error) {
	if f.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.Timeout)
		defer cancel()
	}
	if f.Limiter != nil {
		release, err := f.Limiter.Acquire(ctx, url)
		if err != nil {
			return nil, false, 0, fmt.Errorf("fetch %s: %w", url, err)
		}
		defer release()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, 0, fmt.Errorf("fetch %s: %w", url, err)
	}
	req.Header.Set("User-Agent", f.UserAgent)
	req.Header.Set("Accept", "application/atom+xml, application/rss+xml, application/xml;q=0.9, text/xml;q=0.8, */*;q=0.5")

	cached, hasCached := f.Cache.get(url)
	if hasCached {
		if cached.etag != "" {
			req.Header.Set("If-None-Match", cached.etag)
		}
		if cached.lastModified != "" {
			req.Header.Set("If-Modified-Since", cached.lastModified)
		}
	}

	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, 0, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer func() {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNotModified {
		if hasCached && cached.feed != nil {
			return cached.feed, true, resp.StatusCode, nil
		}
		return nil, true, resp.StatusCode, fmt.Errorf("fetch %s: got 304 without a cached copy", url)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, false, resp.StatusCode, &HTTPError{URL: url, Status: resp.StatusCode}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, false, resp.StatusCode, fmt.Errorf("fetch %s: read body: %w", url, err)
	}
	if len(body) > maxBodyBytes {
		return nil, false, resp.StatusCode, fmt.Errorf("fetch %s: body exceeds %d bytes", url, maxBodyBytes)
	}

	parsed, err := feed.ParseBytes(body, resp.Request.URL.String())
	if err != nil {
		return nil, false, resp.StatusCode, fmt.Errorf("fetch %s: %w", url, err)
	}
	f.Cache.put(url, cacheEntry{
		etag:         resp.Header.Get("ETag"),
		lastModified: resp.Header.Get("Last-Modified"),
		feed:         parsed,
		fetchedAt:    time.Now(),
	})
	return parsed, false, resp.StatusCode, nil
}

// Source is one configured feed to fetch.
type Source struct {
	URL  string
	Name string
}

// FetchAll fetches every source concurrently with at most `workers` requests in
// flight. A failing source produces a Result with Err set; it never aborts the
// others. Results are returned in the order the sources were given.
func (f *Fetcher) FetchAll(ctx context.Context, sources []Source, workers int) []Result {
	results := make([]Result, len(sources))
	if len(sources) == 0 {
		return results
	}
	if workers <= 0 {
		workers = 8
	}
	if workers > len(sources) {
		workers = len(sources)
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := range jobs {
				src := sources[i]
				start := time.Now()
				fd, notMod, status, err := f.Fetch(ctx, src.URL)
				results[i] = Result{
					URL:         src.URL,
					Name:        src.Name,
					Feed:        fd,
					NotModified: notMod,
					Status:      status,
					Err:         err,
					Duration:    time.Since(start),
				}
			}
		}()
	}

	func() {
		defer close(jobs)
		for i := range sources {
			select {
			case jobs <- i:
			case <-ctx.Done():
				// Mark the remaining sources as cancelled rather than leaving
				// zero-valued results behind.
				for j := i; j < len(sources); j++ {
					results[j] = Result{
						URL:  sources[j].URL,
						Name: sources[j].Name,
						Err:  fmt.Errorf("fetch %s: %w", sources[j].URL, ctx.Err()),
					}
				}
				return
			}
		}
	}()
	wg.Wait()
	return results
}

// IsTimeout reports whether err was caused by a deadline or cancellation.
func IsTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var ne interface{ Timeout() bool }
	if errors.As(err, &ne) {
		return ne.Timeout()
	}
	return strings.Contains(err.Error(), "timeout")
}
