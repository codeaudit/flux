// Encapsulates the actual remote communication with a registry
package registry

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-kit/kit/log"
	dockerregistry "github.com/heroku/docker-registry-client/registry"

	"github.com/weaveworks/flux"
	fluxmetrics "github.com/weaveworks/flux/metrics"
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
func (r *remote) LookupTag(tag string) (_ flux.ImageDescription, err error) {
	repository := r.id.Repository()

	_, hostlessImageName, err := parseHost(repository)
	if err != nil {
		return
	}

	return r.lookupImage(r.client.Registry(), hostlessImageName, repository, tag)
}

// Return a list of tags for the repository provided in the ImageID
func (r *remote) Tags() (_ []string, err error) {
	repository := r.id.Repository()

	_, hostlessImageName, err := parseHost(repository)
	if err != nil {
		return
	}

	return r.client.Registry().Tags(hostlessImageName)
}

func (r *remote) Cancel() {
	r.client.Cancel()
}

// TODO: This should be in a generic image parsing class with all the other image parsers
func parseHost(repository string) (string, string, error) {
	var host, org, image string
	parts := strings.Split(repository, "/")
	switch len(parts) {
	case 1:
		host = dockerHubHost
		org = dockerHubLibrary
		image = parts[0]
	case 2:
		host = dockerHubHost
		org = parts[0]
		image = parts[1]
	case 3:
		host = parts[0]
		org = parts[1]
		image = parts[2]
	default:
		return "", "", fmt.Errorf(`expected image name as either "<host>/<org>/<image>", "<org>/<image>", or "<image>"`)
	}

	hostlessImageName := fmt.Sprintf("%s/%s", org, image)
	return host, hostlessImageName, nil
}

func (c *remote) lookupImage(client *dockerregistry.Registry, lookupName, imageName, tag string) (flux.ImageDescription, error) {
	// Minor cheat: this will give the correct result even if the
	// imageName includes a host
	id := flux.MakeImageID("", imageName, tag)
	img := flux.ImageDescription{ID: id}

	start := time.Now()
	meta, err := client.Manifest(lookupName, tag)
	c.metrics.RequestDuration.With(
		LabelRepository, imageName,
		LabelRequestKind, RequestKindMetadata,
		fluxmetrics.LabelSuccess, strconv.FormatBool(err == nil),
	).Observe(time.Since(start).Seconds())
	if err != nil {
		return img, err
	}
	// the manifest includes some v1-backwards-compatibility data,
	// oddly called "History", which are layer metadata as JSON
	// strings; these appear most-recent (i.e., topmost layer) first,
	// so happily we can just decode the first entry to get a created
	// time.
	type v1image struct {
		Created time.Time `json:"created"`
	}
	var topmost v1image
	if err = json.Unmarshal([]byte(meta.History[0].V1Compatibility), &topmost); err == nil {
		if !topmost.Created.IsZero() {
			img.CreatedAt = &topmost.Created
		}
	}

	return img, err
}

// Log requests as they go through, and responses as they come back.
// transport = logTransport{
// 	transport: transport,
// 	log: func(format string, args ...interface{}) {
// 		c.Logger.Log("registry-client-log", fmt.Sprintf(format, args...))
// 	},
// }
type logTransport struct {
	log       func(string, ...interface{})
	transport http.RoundTripper
}

func (t logTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.log("Request %s %#v", req.URL, req)
	res, err := t.transport.RoundTrip(req)
	t.log("Response %#v", res)
	if err != nil {
		t.log("Error %s", err)
	}
	return res, err
}
