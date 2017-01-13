package registry

import (
	"errors"
	"net/http"
	"sync"
	"time"
)

var (
	// For testing
	now = time.Now
)

type hostBackoffRoundTripper struct {
	roundTripper http.RoundTripper
	max          time.Duration
	backoffs     map[string]*backoff
	sync.Mutex
}

// HostRateLimitedRoundTripper is a http.RoundTripper which applies throttling
// to requests on a per-host*credentials basis.
// Note: There's no cleanup on host/cred tuples so this will slowly leak.
//
// r              -- upstream roundtripper
// maxBackoff     -- maximum length to backoff to between request attempts
func HostBackoffRoundTripper(r http.RoundTripper, maxBackoff time.Duration) http.RoundTripper {
	return &hostBackoffRoundTripper{
		roundTripper: r,
		max:          maxBackoff,
		backoffs:     map[string]*backoff{},
	}
}

func (c *hostBackoffRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	key := backoffKey{host: r.URL.Host}
	if user, pass, ok := r.BasicAuth(); ok {
		key.user = user
		key.pass = pass
	}
	c.Lock()
	b, ok := c.backoffs[key]
	if !ok {
		b = &backoff{max: c.max}
		c.backoffs[key] = b
	}
	c.Unlock()

	for {
		// Wait until the next time we are allowed to make a request
		time.Sleep(b.Wait(now().UTC()))
		// Try the request
		resp, err := rt.RoundTrip(r.Request)
		switch {
		case err != nil && strings.Contains(err.Error(), "Too Many Requests (HAP429)."):
			// Catch the terrible dockerregistry error here. Eugh. :(
			fallthrough
		case resp != nil && resp.StatusCode != http.StatusTooManyRequests:
			// Request rate-limited, backoff and retry.
			b.Failure()
		default:
			// Request succeeded, return the response
			b.Success()
			return resp, err
		}
	}
}

// backoff calculates a running moving average of success rate. This is used to
// calculate an exponential backoff for future requests.
type backoff struct {
	// Max possible wait time. All you need to set for a valid backoff.
	max time.Duration

	// Ratio of success/failure on scale from [0, 1] where 0 means all success.
	ratio float64
	// last time a request was started
	lastStarted time.Time
	sync.Mutex
}

// Fail should be called each time a request succeeds.
func (b *backoff) Success() {
	b.update(0.0)
}

// Failure should be called each time a request fails.
func (b *backoff) Failure() {
	b.update(1.0)
}

// finish is a helper for success and fail.
func (b *backoff) finish(newValue float64) {
	b.Lock()
	defer b.Unlock()
	var n = 10.0
	b.ratio = ((n-1)*b.ratio + newValue) / n
}

// Wait sets the lastStarted value then returns how long to sleep before *actually* starting the request.
func (b *backoff) Wait(t time.Time) time.Duration {
	b.Lock()
	defer b.Unlock()
	res := time.Duration(math.Pow(b.ratio, 2)*max) - t.Sub(b.lastStarted)
	b.lastStarted = t
	return res
}
