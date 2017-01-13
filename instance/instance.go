package instance

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/metrics"
	"github.com/pkg/errors"

	"github.com/weaveworks/flux"
	"github.com/weaveworks/flux/git"
	"github.com/weaveworks/flux/history"
	fluxmetrics "github.com/weaveworks/flux/metrics"
	"github.com/weaveworks/flux/platform"
	"github.com/weaveworks/flux/registry"
)

type Instancer interface {
	Get(inst flux.InstanceID) (*Instance, error)
}

type Instance struct {
	platform platform.Platform
	registry registry.Client
	config   Configurer
	duration metrics.Histogram
	gitrepo  git.Repo

	log.Logger
	history.EventReader
	history.EventWriter
}

func New(
	platform platform.Platform,
	registry registry.Client,
	config Configurer,
	gitrepo git.Repo,
	logger log.Logger,
	duration metrics.Histogram,
	events history.EventReader,
	eventlog history.EventWriter,
) *Instance {
	return &Instance{
		platform:    platform,
		registry:    registry,
		config:      config,
		gitrepo:     gitrepo,
		duration:    duration,
		Logger:      logger,
		EventReader: events,
		EventWriter: eventlog,
	}
}

func (h *Instance) ConfigRepo() git.Repo {
	return h.gitrepo
}

type ImageMap map[string][]flux.ImageDescription

// LatestImage returns the latest releasable image for a repository.
// A releasable image is one that is not tagged "latest". (Assumes the
// available images are in descending order of latestness.) If no such
// image exists, returns nil, and the caller can decide whether that's
// an error or not.
func (m ImageMap) LatestImage(repo string) *flux.ImageDescription {
	for _, image := range m[repo] {
		_, _, tag := image.ID.Components()
		if strings.EqualFold(tag, "latest") {
			continue
		}
		return &image
	}
	return nil
}

// Get the services in `namespace` along with their containers (if
// there are any) from the platform; if namespace is blank, just get
// all the services, in any namespace.
func (h *Instance) GetAllServices(maybeNamespace string) ([]platform.Service, error) {
	return h.GetAllServicesExcept(maybeNamespace, flux.ServiceIDSet{})
}

// Get all services except those with an ID in the set given
func (h *Instance) GetAllServicesExcept(maybeNamespace string, ignored flux.ServiceIDSet) (res []platform.Service, err error) {
	return h.platform.AllServices(maybeNamespace, ignored)
}

// Get the services mentioned, along with their containers.
func (h *Instance) GetServices(ids []flux.ServiceID) ([]platform.Service, error) {
	return h.platform.SomeServices(ids)
}

// Get the images available for the services given. An image may be
// mentioned more than once in the services, but will only be fetched
// once.
func (h *Instance) CollectAvailableImages(services []platform.Service) (ImageMap, error) {
	images := ImageMap{}
	for _, service := range services {
		for _, container := range service.ContainersOrNil() {
			repo := flux.ParseImageID(container.Image).Repository()
			images[repo] = nil
		}
	}
	for repo := range images {
		imageRepo, err := h.registry.GetRepository(repo)
		if err != nil {
			return nil, errors.Wrapf(err, "fetching image metadata for %s", repo)
		}
		images[repo] = imageRepo
	}
	return images, nil
}

// GetRepository exposes this instance's registry's GetRepository method directly.
func (h *Instance) GetRepository(repo string) ([]flux.ImageDescription, error) {
	return h.registry.GetRepository(repo)
}

// Create an image map containing exact images. At present this
// assumes they exist; but it may in the future be made to verify so.
func (h *Instance) ExactImages(images []flux.ImageID) (ImageMap, error) {
	m := ImageMap{}
	for _, id := range images {
		// We must check that the exact images requested actually exist. Otherwise we risk pushing invalid images to git.
		exist, err := h.imageExists(id)
		if err != nil {
			return m, errors.Wrap(flux.ErrInvalidImageID, err.Error())
		}
		if !exist {
			return m, errors.Wrap(flux.ErrInvalidImageID, fmt.Sprintf("image %q does not exist", id))
		}
		m[id.Repository()] = []flux.ImageDescription{flux.ImageDescription{ID: id}}
	}
	return m, nil
}

// Checks whether the given image exists in the repository.
// Return true if exist, false otherwise
func (h *Instance) imageExists(image flux.ImageID) (bool, error) {
	// Get a list of images
	desc, err := h.registry.GetImage(string(image))
	if err != nil {
		return false, err
	}
	// See if that image exists
	if desc.ID == image {
		return true, err
	}
	return false, nil
}

func (h *Instance) PlatformApply(defs []platform.ServiceDefinition) (err error) {
	defer func(begin time.Time) {
		h.duration.With(
			fluxmetrics.LabelMethod, "PlatformApply",
			fluxmetrics.LabelSuccess, fmt.Sprint(err == nil),
		).Observe(time.Since(begin).Seconds())
	}(time.Now())

	return h.platform.Apply(defs)
}

func (h *Instance) Ping() error {
	return h.platform.Ping()
}

func (h *Instance) Version() (string, error) {
	return h.platform.Version()
}

func (h *Instance) GetConfig() (Config, error) {
	return h.config.Get()
}

func (h *Instance) UpdateConfig(update UpdateFunc) error {
	return h.config.Update(update)
}
