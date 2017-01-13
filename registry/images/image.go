package image

import (
	"fmt"
	"strings"
)

const (
	dockerHubHost    = "index.docker.io"
	dockerHubLibrary = "library"
)

type ImageID string // "quay.io/weaveworks/helloworld:v1"

func ParseImageID(s string) ImageID {
	return ImageID(s) // technically all strings are valid
}

func MakeImageID(registry, name, tag string) ImageID {
	result := name
	if registry != "" {
		result = registry + "/" + name
	}
	if tag != "" {
		result = result + ":" + tag
	}
	return ImageID(result)
}

func (id ImageID) WithTag(tag string) ImageID {
	r, n, _ := id.Components()
	return MakeImageID(r, n, tag)
}

func (id ImageID) Components() (registry, name, tag string) {
	s := string(id)
	toks := strings.SplitN(s, "/", 3)
	if len(toks) == 3 {
		registry = toks[0]
		s = fmt.Sprintf("%s/%s", toks[1], toks[2])
	}
	toks = strings.SplitN(s, ":", 2)
	if len(toks) == 2 {
		tag = toks[1]
	}
	name = toks[0]
	return registry, name, tag
}

func (id ImageID) Repository() string {
	registry, name, _ := id.Components()
	if registry != "" && name != "" {
		return registry + "/" + name
	}
	if name != "" {
		return name
	}
	return ""
}

func (id ImageID) Host() string {
	host, _, _ := id.Components()
	if host == "" {
		return dockerHubHost
	}
	return host
}

func (id ImageID) Name() string {
	_, name, _ := id.Components()
	if name == "" {
		return dockerHubLibrary
	}
	return name
}
