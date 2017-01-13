// Package registry provides domain abstractions over container registries.
package registry

import (
	"sort"
	"strconv"
	"time"

	"github.com/go-kit/kit/log"

	"github.com/weaveworks/flux"
	fluxmetrics "github.com/weaveworks/flux/metrics"
)

// Client is a handle to a bunch of registries.
type Client interface {
	GetRepository(repository string) ([]flux.ImageDescription, error)
	GetImage(repository string) (flux.ImageDescription, error)
}

// client is a handle to a registry.
type client struct {
	Credentials Credentials
	Logger      log.Logger
	Metrics     Metrics
}

// NewClient creates a new registry client, to use when fetching repositories.
func NewClient(c Credentials, l log.Logger, m Metrics) Client {
	return &client{
		Credentials: c,
		Logger:      l,
		Metrics:     m,
	}
}

// GetRepository yields a repository matching the given name, if any exists.
// Repository may be of various forms, in which case omitted elements take
// assumed defaults.
//
//   helloworld             -> index.docker.io/library/helloworld
//   foo/helloworld         -> index.docker.io/foo/helloworld
//   quay.io/foo/helloworld -> quay.io/foo/helloworld
//
func (c *client) GetRepository(repository string) (_ []flux.ImageDescription, err error) {
	defer func(start time.Time) {
		c.Metrics.FetchDuration.With(
			LabelRepository, repository,
			fluxmetrics.LabelSuccess, strconv.FormatBool(err == nil),
		).Observe(time.Since(start).Seconds())
	}(time.Now())

	id := flux.ParseImageID(repository)
	remoteClient, err := NewRemoteClient(c.Credentials, id)
	if err != nil {
		return
	}
	remote := NewRemote(remoteClient, id, c.Logger, c.Metrics)
	start := time.Now()
	tags, err := remote.Tags()
	c.Metrics.RequestDuration.With(
		LabelRepository, repository,
		LabelRequestKind, RequestKindTags,
		fluxmetrics.LabelSuccess, strconv.FormatBool(err == nil),
	).Observe(time.Since(start).Seconds())
	if err != nil {
		remote.Cancel()
		return nil, err
	}

	// the hostlessImageName is canonicalised, in the sense that it
	// includes "library" as the org, if unqualified -- e.g.,
	// `library/nats`. We need that to fetch the tags etc. However, we
	// want the results to use the *actual* name of the images to be
	// as supplied, e.g., `nats`.
	return c.tagsToRepository(remote, tags)
}

// Get a single image from the registry if it exists
func (c *client) GetImage(repoImageTag string) (_ flux.ImageDescription, err error) {
	defer func(start time.Time) {
		c.Metrics.FetchDuration.With(
			LabelRepository, repoImageTag,
			fluxmetrics.LabelSuccess, strconv.FormatBool(err == nil),
		).Observe(time.Since(start).Seconds())
	}(time.Now())
	id := flux.ParseImageID(repoImageTag)
	remoteClient, err := NewRemoteClient(c.Credentials, id)
	if err != nil {
		return
	}
	remote := NewRemote(remoteClient, id, c.Logger, c.Metrics)

	return remote.Lookup()
}

func (c *client) tagsToRepository(remote Remote, tags []string) ([]flux.ImageDescription, error) {
	// one way or another, we'll be finishing all requests
	defer remote.Cancel()

	type result struct {
		image flux.ImageDescription
		err   error
	}

	fetched := make(chan result, len(tags))

	for _, tag := range tags {
		go func(t string) {
			img, err := remote.LookupTag(t)
			if err != nil {
				c.Logger.Log("registry-metadata-err", err)
			}
			fetched <- result{img, err}
		}(tag)
	}

	images := make([]flux.ImageDescription, cap(fetched))
	for i := 0; i < cap(fetched); i++ {
		res := <-fetched
		if res.err != nil {
			return nil, res.err
		}
		images[i] = res.image
	}

	sort.Sort(byCreatedDesc(images))
	return images, nil
}

// -----

type byCreatedDesc []flux.ImageDescription

func (is byCreatedDesc) Len() int      { return len(is) }
func (is byCreatedDesc) Swap(i, j int) { is[i], is[j] = is[j], is[i] }
func (is byCreatedDesc) Less(i, j int) bool {
	if is[i].CreatedAt == nil {
		return true
	}
	if is[j].CreatedAt == nil {
		return false
	}
	if is[i].CreatedAt.Equal(*is[j].CreatedAt) {
		return is[i].ID < is[j].ID
	}
	return is[i].CreatedAt.After(*is[j].CreatedAt)
}
