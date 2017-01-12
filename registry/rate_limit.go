package registry

import (
	"errors"
	"net/http"
	"sync"
	"time"
)

var (
	ErrTooManyPendingRequests = errors.New("too many pending requests")

	// For testing
	now = time.Now
)

type hostRateLimitedRoundTripper struct {
	roundTripper http.RoundTripper
	maxBacklog   time.Duration
	limits       map[string]limit
	sync.RWMutex
}

type limit struct {
	maxRequestsPerSecond int // 0 means no limit
	nextRequestAt        time.Time
}

// HostRateLimitedRoundTripper is a http.RoundTripper which applies throttling
// to requests on a per-host basis.
// * r          -- upstream roundtripper
// * maxBacklog -- 1 return ErrTooManyPendingRequests if a request would be kept waiting longer than this. (<= 0 is no limit)
// * limits     -- the maximum request/second for each host. If <= 0 or unset, no limit for this host.
func HostRateLimitedRoundTripper(r http.RoundTripper, maxBacklog time.Duration, limits map[string]int) http.RoundTripper {
	rlc := &hostRateLimitedRoundTripper{
		roundTripper: r,
		maxBacklog:   maxBacklog,
		limits:       map[string]limit{},
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
		t := now()
		if limit.nextRequestAt.Before(t) {
			limit.nextRequestAt = t
		}
		sleep = limit.nextRequestAt.Sub(t)
		newNextRequest := limit.nextRequestAt.Add(1 * time.Second / time.Duration(limit.maxRequestsPerSecond))
		if c.maxBacklog > time.Duration(0) && newNextRequest.After(t.Add(c.maxBacklog)) {
			c.Unlock()
			return nil, ErrTooManyPendingRequests
		}
		limit.nextRequestAt = newNextRequest
		c.limits[host] = limit
		c.Unlock()
	}

	time.Sleep(sleep)

	return c.roundTripper.RoundTrip(r)
}
