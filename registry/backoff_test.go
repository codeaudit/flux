package registry

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestRateLimitedRoundTripper_BacksOffPerHostAndCredentialsWhenRateLimited(t *testing.T) {
	t.Parallel()
	// It should only allow max requests/second to a registry
	startTime := time.Now()
	var offset time.Duration
	now = func() time.Time { return startTime.Add(offset) }
	requests := []time.Time{}
	limit := 3
	var rt http.RoundTripper = roundtripperFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, now())
		if len(requests) < limit {
			offset += len(requests) * time.Millisecond
			return &http.Response{
				Status:     "429 Too Many Requests",
				StatusCode: http.StatusTooManyRequests,
			}, nil
		}
		return &http.Response{
			Status:     "200 OK",
			StatusCode: http.StatusOK,
		}, nil
	})
	username, password, host := "user1", "pa55word", "example.local"
	rt = HostRateLimitedRoundTripper(rt, 1*time.Millisecond, 1*time.Second)
	for i := 0; i < limit+1; i++ {
		request, err := http.NewRequest("GET", "http://"+host+"/image/foo", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.SetBasicAuth(username, password)
		_, err = rt.RoundTrip(request)
		if err != nil {
			t.Fatal(err)
		}
	}

	if len(requests) != limit+1 {
		t.Errorf("Expected %d requests, got %d", limit+1, len(requests))
	}

	for i, reqTime := range []time.Time{
		startTime,
		startTime.Add(1 * time.Millisecond),
		startTime.Add(2 * time.Millisecond),
		startTime.Add(2 * time.Millisecond),
		startTime.Add(2 * time.Millisecond),
	} {
		if !requests[i].Equal(reqTime) {
			t.Errorf("Expected requests[%d] to equal: %v, got %v", i, reqTime, requests[i])
		}
	}
}

func TestRateLimitedRoundTripper_UncredentialedRequestsBackOffPerHostWhenRateLimited(t *testing.T) {
	t.Parallel()
	t.Error("TODO")
}

func TestRateLimitedRoundTripper_StopsBackoffAtMax(t *testing.T) {
	t.Parallel()
	t.Error("TODO")
}

func TestRateLimitedRoundTripper_DifferentHostsEnforcedSeparately(t *testing.T) {
	t.Parallel()
	// Separate registries should have be enforced separately
	var lock sync.Mutex
	requests := map[string][]time.Time{}
	var rt http.RoundTripper = roundtripperFunc(func(r *http.Request) (*http.Response, error) {
		lock.Lock()
		defer lock.Unlock()
		requests[r.URL.Host] = append(requests[r.URL.Host], time.Now())
		return nil, nil
	})
	limits := map[string]int{
		"host1": 1,
		"host2": 2,
		"host3": 3,
	}
	rt = HostRateLimitedRoundTripper(rt, 0, limits)

	var wg sync.WaitGroup
	wg.Add(len(limits))
	for host, limit := range limits {
		go func(h string, lim int) {
			for i := 0; i < lim+1; i++ {
				request, err := http.NewRequest("GET", "http://"+h+"/image/foo", nil)
				if err != nil {
					t.Fatal(err)
				}
				_, err = rt.RoundTrip(request)
				if err != nil {
					t.Fatal(err)
				}
			}
			wg.Done()
		}(host, limit)
	}
	wg.Wait()

	for host, limit := range limits {
		buckets := map[int64]int{}
		for _, ts := range requests[host] {
			buckets[ts.Unix()]++
			if buckets[ts.Unix()] > limit {
				t.Error("Too many requests/second to " + host)
			}
		}
	}
}

func TestRateLimitedRoundTripper_Timeout(t *testing.T) {
	t.Parallel()
	// If the backlog is too high, an error should be returned.
	var rt http.RoundTripper = roundtripperFunc(func(r *http.Request) (*http.Response, error) {
		return nil, nil
	})
	host := "example.local"
	limit := 1
	maxBacklog := 2 * time.Second
	rt = HostRateLimitedRoundTripper(rt, maxBacklog, map[string]int{host: limit})

	// Lock now, so it will be like all the requests arrive at the same time.
	currentTime := time.Now()
	now = func() time.Time { return currentTime }

	var errs []error
	for i := 0; i < int(maxBacklog/time.Second)+1; i++ {
		request, err := http.NewRequest("GET", "http://"+host+"/image/foo", nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rt.RoundTrip(request); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) < 1 {
		t.Errorf("Expected >=1 error, got %d", len(errs))
	}
	for _, err := range errs {
		if err != ErrTooManyPendingRequests {
			t.Errorf("Expected ErrTooManyPendingRequests error, got %q", err)
		}
	}
}
