package registry

import (
	"net/http"
	"sync"
	"time"
)

type hostRateLimitedRoundTripper struct {
	roundTripper http.RoundTripper
	limits       map[string]limit
	now          func() time.Time // for testing.
	sync.RWMutex
}

type limit struct {
	maxRequestsPerSecond int // 0 means no limit
	nextRequestAt        time.Time
}

func HostRateLimitedRoundTripper(r http.RoundTripper, limits map[string]int) http.RoundTripper {
	rlc := &hostRateLimitedRoundTripper{
		roundTripper: r,
		limits:       map[string]limit{},
		now:          time.Now,
	}
	for reg, max := range limits {
		rlc.limits[reg] = limit{maxRequestsPerSecond: max}
	}
	return rlc
}

func (c *hostRateLimitedRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	host := r.URL.Host

	var sleep time.Duration
	c.RLock()
	limit := c.limits[host]
	c.RUnlock()
	if limit.maxRequestsPerSecond > 0 {
		c.Lock()
		now := c.now()
		if limit.nextRequestAt.Before(now) {
			limit.nextRequestAt = now
		}
		sleep = limit.nextRequestAt.Sub(now)
		limit.nextRequestAt = limit.nextRequestAt.Add(1 * time.Second / time.Duration(limit.maxRequestsPerSecond))
		c.limits[host] = limit
		c.Unlock()
	}

	time.Sleep(sleep)

	return c.roundTripper.RoundTrip(r)
}
