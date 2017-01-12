package registry

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestRateLimitedRoundTripper_OnlyAllowsMaxRequestsPerSecondToARegistry(t *testing.T) {
	// It should only allow max requests/second to a registry
	requests := []time.Time{}
	var rt http.RoundTripper = roundtripperFunc(func(r *http.Request) (*http.Response, error) {
		requests = append(requests, time.Now())
		return nil, nil
	})
	host := "example.local"
	limit := 3
	rt = HostRateLimitedRoundTripper(rt, map[string]int{host: limit})
	var wg sync.WaitGroup
	wg.Add(limit + 2)
	for i := 0; i < limit+2; i++ {
		go func() {
			request, err := http.NewRequest("GET", "http://"+host+"/image/foo", nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = rt.RoundTrip(request)
			if err != nil {
				t.Fatal(err)
			}
			wg.Done()
		}()
	}
	wg.Wait()

	buckets := map[int64]int{}
	for _, ts := range requests {
		buckets[ts.Unix()]++
		if buckets[ts.Unix()] > 3 {
			t.Error("Too many requests/second to " + host)
		}
	}
}

func TestRateLimitedRoundTripper_DifferentReposShareALimite(t *testing.T) {
	// Different repos (on the same registry) should share a limit
	t.Error("TODO")
}

func TestRateLimitedRoundTripper_DifferentRegistriesEnforcedSeparately(t *testing.T) {
	// Separate registries should have be enforced separately
	t.Error("TODO")
}

func TestRateLimitedRoundTripper_UnlimitedRegistries(t *testing.T) {
	// Unlimited registries should be unlimited
	t.Error("TODO")
}

func TestRateLimitedRoundTripper_BacklogTooHigh(t *testing.T) {
	// If the backlog is too high, an error should be returned.
	t.Error("TODO")
}
