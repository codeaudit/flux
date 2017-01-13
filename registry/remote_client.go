package registry

import (
	"context"
	"encoding/json"
	"fmt"
	dockerregistry "github.com/heroku/docker-registry-client/registry"
	"github.com/weaveworks/flux"
	"strings"
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

func NewRemoteClient(client *dockerregistry.Registry, cancel context.CancelFunc) (_ RemoteClient, err error) {
	return &remoteClient{
		client: client,
		cancel: cancel,
	}, nil
}

func (rc *remoteClient) Tags(id flux.ImageID) (_ []string, err error) {
	_, hostlessImageName, err := parseHost(string(id))
	if err != nil {
		return
	}
	return rc.client.Tags(hostlessImageName)
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
