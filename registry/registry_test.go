package registry

import (
	"github.com/weaveworks/flux/registry/images"
	"testing"
)

type testImage struct {
	Registry, Name, Tag string
}

var imageParsingExamples = map[string]testImage{
	"foo/bar": {
		Registry: "",
		Name:     "foo/bar",
		Tag:      "",
	},
	"foo/bar:baz": {
		Registry: "",
		Name:     "foo/bar",
		Tag:      "baz",
	},
	"reg:123/foo/bar:baz": {
		Registry: "reg:123",
		Name:     "foo/bar",
		Tag:      "baz",
	},
	"docker-registry.domain.name:5000/repo/image1:ver": {
		Registry: "docker-registry.domain.name:5000",
		Name:     "repo/image1",
		Tag:      "ver",
	},
	"shortreg/repo/image1": {
		Registry: "shortreg",
		Name:     "repo/image1",
		Tag:      "",
	},
	"foo": {
		Registry: "",
		Name:     "foo",
		Tag:      "",
	},
}

func TestParseImage(t *testing.T) {
	for in, want := range imageParsingExamples {
		outReg, outName, outTag := image.ParseImageID(in).Components()
		if outReg != want.Registry ||
			outName != want.Name ||
			outTag != want.Tag {
			t.Fatalf("%s: %v != %v", in, testImage{outReg, outName, outTag}, want)
		}
	}
}

func TestMakeImage(t *testing.T) {
	for want, in := range imageParsingExamples {
		out := image.MakeImageID(in.Registry, in.Name, in.Tag)
		if string(out) != want {
			t.Fatalf("%#v.String(): %s != %s", in, out, want)
		}
	}
}

func TestImageRepository(t *testing.T) {
	for in, want := range map[string]string{
		"foo/bar":                                          "foo/bar",
		"foo/bar:baz":                                      "foo/bar",
		"reg:123/foo/bar:baz":                              "reg:123/foo/bar",
		"docker-registry.domain.name:5000/repo/image1:ver": "docker-registry.domain.name:5000/repo/image1",
		"shortreg/repo/image1":                             "shortreg/repo/image1",
		"foo": "foo",
	} {
		out := image.ParseImageID(in).Repository()
		if out != want {
			t.Fatalf("%#v.Repository(): %s != %s", in, out, want)
		}
	}
}
