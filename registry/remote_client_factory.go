// We can't inject a remote client directly because each repository request might be to a different
// registry provider. E.g. both docker hub and quay containers. So a new remote client must be
// created for each new image. This factory provides that and can be mocked out.
package registry

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strings"

	dockerregistry "github.com/heroku/docker-registry-client/registry"
	"golang.org/x/net/publicsuffix"

	"github.com/weaveworks/flux"
	"github.com/weaveworks/flux/registry/images"
)

type creds struct {
	username, password string
}

// Credentials to a (Docker) registry.
type Credentials struct {
	m map[string]creds
}

type RemoteClientFactory interface {
	Create(id image.ImageID) (RemoteClient, error)
}

func NewRemoteClientFactory(c Credentials) RemoteClientFactory {
	return &remoteClientFactory{
		creds: c,
	}
}

type remoteClientFactory struct {
	creds Credentials
}

func (f *remoteClientFactory) Create(id image.ImageID) (_ RemoteClient, err error) {
	client, cancel, err := newRegistryClient(id.Host(), f.creds)
	if err != nil {
		return
	}
	return newRemoteClient(client, cancel)
}

func newRegistryClient(host string, creds Credentials) (client *dockerregistry.Registry, cancel context.CancelFunc, err error) {
	httphost := "https://" + host

	// quay.io wants us to use cookies for authorisation, so we have
	// to construct one (the default client has none). This means a
	// bit more constructing things to be able to make a registry
	// client literal, rather than calling .New()
	jar, err := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if err != nil {
		return
	}
	auth := creds.credsFor(host)

	// A context we'll use to cancel requests on error
	ctx, cancel := context.WithCancel(context.Background())

	// Use the wrapper to fix headers for quay.io, and remember bearer tokens
	var transport http.RoundTripper = &wwwAuthenticateFixer{transport: http.DefaultTransport}
	// Now the auth-handling wrappers that come with the library
	transport = dockerregistry.WrapTransport(transport, httphost, auth.username, auth.password)

	client = &dockerregistry.Registry{
		URL: httphost,
		Client: &http.Client{
			Transport: roundtripperFunc(func(r *http.Request) (*http.Response, error) {
				return transport.RoundTrip(r.WithContext(ctx))
			}),
			Jar: jar,
		},
		Logf: dockerregistry.Quiet,
	}
	return
}

// --- Credentials

func CredentialsFromConfig(config flux.UnsafeInstanceConfig) (Credentials, error) {
	m := map[string]creds{}
	for host, entry := range config.Registry.Auths {
		decodedAuth, err := base64.StdEncoding.DecodeString(entry.Auth)
		if err != nil {
			return Credentials{}, err
		}
		authParts := strings.SplitN(string(decodedAuth), ":", 2)
		m[host] = creds{
			username: authParts[0],
			password: authParts[1],
		}
	}
	return Credentials{m: m}, nil
}

// For yields an authenticator for a specific host.
func (cs Credentials) credsFor(host string) creds {
	if cred, found := cs.m[host]; found {
		return cred
	}
	if cred, found := cs.m[fmt.Sprintf("https://%s/v1/", host)]; found {
		return cred
	}
	return creds{}
}

// Hosts returns all of the hosts available in these credentials.
func (cs Credentials) Hosts() []string {
	hosts := []string{}
	for host := range cs.m {
		hosts = append(hosts, host)
	}
	return hosts
}
