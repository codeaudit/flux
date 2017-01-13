package registry

import (
	"context"
	"encoding/json"
	dockerregistry "github.com/heroku/docker-registry-client/registry"
	"github.com/weaveworks/flux"
	"time"
)

type RemoteClient interface {
	Tags(id flux.ImageID) ([]string, error)
	Manifest(id flux.ImageID) (flux.ImageDescription, error)
	Cancel()
}

type remoteClient struct {
	client *dockerregistry.Registry
	cancel context.CancelFunc
}

func newRemoteClient(client *dockerregistry.Registry, cancel context.CancelFunc) (_ RemoteClient, err error) {
	return &remoteClient{
		client: client,
		cancel: cancel,
	}, nil
}

func (rc *remoteClient) Tags(id flux.ImageID) (_ []string, err error) {
	return rc.client.Tags(id.Name())
}

func (rc *remoteClient) Manifest(id flux.ImageID) (flux.ImageDescription, error) {
	_, lookupName, tag := id.Components()
	img := flux.ImageDescription{ID: id}
	meta, err := rc.client.Manifest(lookupName, tag)
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

func (rc *remoteClient) Cancel() {
	rc.cancel()
}
