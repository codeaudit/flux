// Encapsulates the actual remote communication with a registry
package registry

import (
	"github.com/go-kit/kit/log"
	"github.com/weaveworks/flux"
	"net/http"
)

// The remote interface represents calls to a remote registry
type Remote interface {
	Lookup() (_ flux.ImageDescription, err error)
	LookupTag(tag string) (_ flux.ImageDescription, err error)
	Tags() (tags []string, err error)
	Cancel()
}

type remote struct {
	id      flux.ImageID
	client  RemoteClient
	logger  log.Logger
	metrics Metrics
}

func NewRemote(r RemoteClient, id flux.ImageID, l log.Logger, m Metrics) Remote {
	return &remote{
		client:  r,
		id:      id,
		logger:  l,
		metrics: m,
	}
}

type roundtripperFunc func(*http.Request) (*http.Response, error)

func (f roundtripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// Lookup an image using a tag parsed from the ImageID
func (r *remote) Lookup() (flux.ImageDescription, error) {
	_, _, tag := r.id.Components()
	return r.LookupTag(tag)
}

// Lookup an image with the tag explicitly specified. Host and Image is still parsed from ImageID.
func (r *remote) LookupTag(tag string) (flux.ImageDescription, error) {
	return r.client.Manifest(r.id.WithTag(tag))
}

// Return a list of tags for the repository provided in the ImageID
func (r *remote) Tags() (_ []string, err error) {
	return r.client.Tags(r.id)
}

func (r *remote) Cancel() {
	r.client.Cancel()
}
