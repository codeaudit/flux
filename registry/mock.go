package registry

import (
	"github.com/docker/distribution/manifest/schema1"
	"github.com/weaveworks/flux"
)

type mockRegistry struct {
	descriptions []flux.ImageDescription
	err          error
}

func NewMockRegistry(descriptions []flux.ImageDescription, err error) Client {
	return &mockRegistry{
		descriptions: descriptions,
		err:          err,
	}
}

func (r *mockRegistry) GetRepository(repository string) ([]flux.ImageDescription, error) {
	return r.descriptions, r.err
}

func (r *mockRegistry) GetImage(repository string) (flux.ImageDescription, error) {
	return r.descriptions[0], r.err
}

type mockDockerClient struct {
	manifest schema1.SignedManifest
	tags     []string
	err      error
}

func NewMockDockerClient(manifest schema1.SignedManifest, tags []string, err error) dockerRegistryInterface {
	return &mockDockerClient{
		manifest: manifest,
		tags:     tags,
		err:      err,
	}
}

func (m *mockDockerClient) Manifest(repository, reference string) (*schema1.SignedManifest, error) {
	return &m.manifest, m.err
}

func (m *mockDockerClient) Tags(repository string) ([]string, error) {
	return m.tags, m.err
}
