package registry

import (
	"github.com/docker/distribution/manifest/schema1"
	dockerregistry "github.com/heroku/docker-registry-client/registry"
	"github.com/pkg/errors"
	"github.com/weaveworks/flux/registry/images"
	"testing"
	"time"
)

const testTagStr = "tag"
const testImageStr = "test/image:" + testTagStr
const constTime = "2017-01-13T16:22:58.009923189Z"

// Need to create a dummy manifest here
func TestParseManifest(t *testing.T) {
	man := schema1.SignedManifest{
		Manifest: schema1.Manifest{
			History: []schema1.History{
				{
					V1Compatibility: `{"created":"` + constTime + `"}`,
				},
			},
		},
	}
	c := remoteClient{
		client: NewMockDockerClient(man, nil, nil),
	}
	desc, err := c.Manifest(image.ParseImageID(testImageStr))
	if err != nil {
		t.Fatal(err.Error())
	}
	if string(desc.ID) != testImageStr {
		t.Fatalf("Expecting %q but got %q", testImageStr, string(desc.ID))
	}
	if desc.CreatedAt.Format(time.RFC3339Nano) != constTime {
		t.Fatalf("Expecting %q but got %q", constTime, desc.CreatedAt.Format(time.RFC3339Nano))
	}
}

// Just a simple pass through.
func TestGetTags(t *testing.T) {
	c := remoteClient{
		client: NewMockDockerClient(schema1.SignedManifest{}, []string{
			testTagStr,
		}, nil),
	}
	tags, err := c.Tags(image.ParseImageID(testImageStr))
	if err != nil {
		t.Fatal(err.Error())
	}
	if tags[0] != testTagStr {
		t.Fatalf("Expecting %q but got %q", testTagStr, tags[0])
	}
}

func TestCancelIsCalled(t *testing.T) {
	var didCancel bool
	r := remoteClient{
		cancel: func() { didCancel = true },
	}
	r.Cancel()
	if !didCancel {
		t.Fatal("Expected it to call the cancel func")
	}
}

func TestErrorsForCoverage(t *testing.T) {
	c := remoteClient{
		client: NewMockDockerClient(schema1.SignedManifest{}, []string{
			testTagStr,
		}, errors.New("dummy")),
	}
	_, err := c.Tags(image.ParseImageID(testImageStr))
	if err == nil {
		t.Fatal("Expected error")
	}
	_, err = c.Manifest(image.ParseImageID(testImageStr))
	if err == nil {
		t.Fatal("Expected error")
	}
}

func TestNew(t *testing.T) {
	r := &dockerregistry.Registry{}
	var flag bool
	f := func() { flag = true }
	c := newRemoteClient(r, f)
	if c.(*remoteClient).client != r {
		t.Log("Client was not set")
	}
	c.(*remoteClient).cancel()
	if !flag {
		t.Fatal("Expected it to call the cancel func")
	}
}
