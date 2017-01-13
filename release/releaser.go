package release

import (
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/metrics"
	"github.com/pkg/errors"

	"github.com/weaveworks/flux"
	"github.com/weaveworks/flux/instance"
	"github.com/weaveworks/flux/jobs"
	fluxmetrics "github.com/weaveworks/flux/metrics"
	"github.com/weaveworks/flux/platform"
	"github.com/weaveworks/flux/platform/kubernetes"
	"github.com/weaveworks/flux/registry/images"
)

const FluxServiceName = "fluxsvc"
const FluxDaemonName = "fluxd"

type Releaser struct {
	instancer instance.Instancer
	metrics   Metrics
}

type Metrics struct {
	ReleaseDuration metrics.Histogram
	ActionDuration  metrics.Histogram
	StageDuration   metrics.Histogram
}

func NewReleaser(
	instancer instance.Instancer,
	metrics Metrics,
) *Releaser {
	return &Releaser{
		instancer: instancer,
		metrics:   metrics,
	}
}

type ReleaseAction struct {
	Name        string                                `json:"name"`
	Description string                                `json:"description"`
	Do          func(*ReleaseContext) (string, error) `json:"-"`
	Result      string                                `json:"result"`
}

func (r *Releaser) Handle(job *jobs.Job, updater jobs.JobUpdater) (followUps []jobs.Job, err error) {
	params := job.Params.(jobs.ReleaseJobParams)

	// Backwards compatibility
	if string(params.ServiceSpec) != "" {
		params.ServiceSpecs = append(params.ServiceSpecs, params.ServiceSpec)
	}

	releaseType := "unknown"
	defer func(begin time.Time) {
		r.metrics.ReleaseDuration.With(
			fluxmetrics.LabelReleaseType, releaseType,
			fluxmetrics.LabelReleaseKind, string(params.Kind),
			fluxmetrics.LabelSuccess, fmt.Sprint(err == nil),
		).Observe(time.Since(begin).Seconds())
	}(time.Now())

	inst, err := r.instancer.Get(job.Instance)
	if err != nil {
		return nil, err
	}

	inst.Logger = log.NewContext(inst.Logger).With("job", job.ID)

	updateJob := func(format string, args ...interface{}) {
		status := fmt.Sprintf(format, args...)
		job.Status = status
		job.Log = append(job.Log, status)
		updater.UpdateJob(*job)
	}

	updateJob("Calculating release actions.")

	var actions []ReleaseAction
	releaseType, actions, err = r.plan(inst, params)
	if err != nil {
		return nil, errors.Wrap(err, "planning release")
	}
	return nil, r.execute(inst, actions, params.Kind, updateJob)
}

func (r *Releaser) plan(inst *instance.Instance, params jobs.ReleaseJobParams) (string, []ReleaseAction, error) {
	releaseType := "unknown"

	images := ImageSelectorForSpec(params.ImageSpec)

	services, err := ServiceSelectorForSpecs(inst, params.ServiceSpecs, params.Excludes)
	if err != nil {
		return releaseType, nil, err
	}

	msg := fmt.Sprintf("Release %v to %v", images, services)
	var actions []ReleaseAction
	switch {
	case params.ServiceSpec == flux.ServiceSpecAll && params.ImageSpec == flux.ImageSpecLatest:
		releaseType = "release_all_to_latest"
		actions, err = r.releaseImages(releaseType, msg, inst, services, images)

	case params.ServiceSpec == flux.ServiceSpecAll && params.ImageSpec == flux.ImageSpecNone:
		releaseType = "release_all_without_update"
		actions, err = r.releaseWithoutUpdate(releaseType, msg, inst, services)

	case params.ServiceSpec == flux.ServiceSpecAll:
		releaseType = "release_all_for_image"
		actions, err = r.releaseImages(releaseType, msg, inst, services, images)

	case params.ImageSpec == flux.ImageSpecLatest:
		releaseType = "release_one_to_latest"
		actions, err = r.releaseImages(releaseType, msg, inst, services, images)

	case params.ImageSpec == flux.ImageSpecNone:
		releaseType = "release_one_without_update"
		actions, err = r.releaseWithoutUpdate(releaseType, msg, inst, services)

	default:
		releaseType = "release_one"
		actions, err = r.releaseImages(releaseType, msg, inst, services, images)
	}
	return releaseType, actions, err
}

func (r *Releaser) releaseImages(method, msg string, inst *instance.Instance, getServices ServiceSelector, getImages ImageSelector) ([]ReleaseAction, error) {
	var res []ReleaseAction
	res = append(res, r.releaseActionPrintf(msg))

	var (
		base  = r.metrics.StageDuration.With("method", method)
		stage *metrics.Timer
	)

	defer func() { stage.ObserveDuration() }()
	stage = metrics.NewTimer(base.With("stage", "fetch_platform_services"))

	services, err := getServices.SelectServices(inst)
	if err != nil {
		return nil, errors.Wrap(err, "fetching platform services")
	}
	if len(services) == 0 {
		res = append(res, r.releaseActionPrintf("No selected services found. Nothing to do."))
		return res, nil
	}

	stage.ObserveDuration()
	stage = metrics.NewTimer(base.With("stage", "calculate_applies"))

	// Each service is running multiple images.
	// Each image may need to be upgraded, and trigger an apply.
	images, err := getImages.SelectImages(inst, services)
	if err != nil {
		return nil, errors.Wrap(err, "collecting available images to calculate applies")
	}

	updateMap := CalculateUpdates(services, images, func(format string, args ...interface{}) {
		res = append(res, r.releaseActionPrintf(format, args...))
	})

	if len(updateMap) <= 0 {
		res = append(res, r.releaseActionPrintf("All selected services are running the requested images. Nothing to do."))
		return res, nil
	}

	stage.ObserveDuration()
	stage = metrics.NewTimer(base.With("stage", "finalize"))

	// We have identified at least 1 release that needs to occur. Releasing
	// means cloning the repo, changing the resource file(s), committing and
	// pushing, and then making the release(s) to the platform.

	res = append(res, r.releaseActionClone())
	for service, applies := range updateMap {
		res = append(res, r.releaseActionUpdatePodController(service, applies))
	}
	res = append(res, r.releaseActionCommitAndPush(msg))
	var servicesToApply []flux.ServiceID
	for service := range updateMap {
		servicesToApply = append(servicesToApply, service)
	}
	res = append(res, r.releaseActionReleaseServices(servicesToApply, msg))

	return res, nil
}

// Release whatever is in the cloned configuration, without changing anything
func (r *Releaser) releaseWithoutUpdate(method, msg string, inst *instance.Instance, getServices ServiceSelector) ([]ReleaseAction, error) {
	var res []ReleaseAction

	var (
		base  = r.metrics.StageDuration.With("method", method)
		stage *metrics.Timer
	)

	defer func() { stage.ObserveDuration() }()
	stage = metrics.NewTimer(base.With("stage", "fetch_platform_services"))

	services, err := getServices.SelectServices(inst)
	if err != nil {
		return nil, errors.Wrap(err, "fetching platform services")
	}
	if len(services) == 0 {
		res = append(res, r.releaseActionPrintf("No selected services found. Nothing to do."))
		return res, nil
	}

	stage.ObserveDuration()
	stage = metrics.NewTimer(base.With("stage", "finalize"))

	res = append(res, r.releaseActionPrintf(msg))
	res = append(res, r.releaseActionClone())

	ids := []flux.ServiceID{}
	for _, service := range services {
		res = append(res, r.releaseActionFindPodController(service.ID))
		ids = append(ids, service.ID)
	}
	res = append(res, r.releaseActionReleaseServices(ids, msg))
	return res, nil
}

func (r *Releaser) execute(inst *instance.Instance, actions []ReleaseAction, kind flux.ReleaseKind, updateJob func(string, ...interface{})) error {
	rc := NewReleaseContext(inst)
	defer rc.Clean()

	for i, action := range actions {
		updateJob(action.Description)
		inst.Log("description", action.Description)
		if action.Do == nil {
			continue
		}

		if kind == flux.ReleaseKindExecute {
			begin := time.Now()
			result, err := action.Do(rc)
			r.metrics.ActionDuration.With(
				fluxmetrics.LabelAction, action.Name,
				fluxmetrics.LabelSuccess, fmt.Sprint(err == nil),
			).Observe(time.Since(begin).Seconds())
			if err != nil {
				updateJob(err.Error())
				inst.Log("err", err)
				actions[i].Result = "Failed: " + err.Error()
				return err
			}
			if result != "" {
				updateJob(result)
			}
			actions[i].Result = result
		}
	}

	return nil
}

func CalculateUpdates(services []platform.Service, images instance.ImageMap, printf func(string, ...interface{})) map[flux.ServiceID][]ContainerUpdate {
	updateMap := map[flux.ServiceID][]ContainerUpdate{}
	for _, service := range services {
		containers, err := service.ContainersOrError()
		if err != nil {
			printf("service %s does not have images associated: %s", service.ID, err)
			continue
		}
		for _, container := range containers {
			currentImageID := image.ParseImageID(container.Image)
			latestImage := images.LatestImage(currentImageID.Repository())
			if latestImage == nil {
				continue
			}

			if currentImageID == latestImage.ID {
				printf("Service %s image %s is already the latest one; skipping.", service.ID, currentImageID)
				continue
			}

			updateMap[service.ID] = append(updateMap[service.ID], ContainerUpdate{
				Container: container.Name,
				Current:   currentImageID,
				Target:    latestImage.ID,
			})
		}
	}
	return updateMap
}

// Release helpers.

type ContainerUpdate struct {
	Container string
	Current   image.ImageID
	Target    image.ImageID
}

// ReleaseAction Do funcs

func (r *Releaser) releaseActionPrintf(format string, args ...interface{}) ReleaseAction {
	return ReleaseAction{
		Name:        "printf",
		Description: fmt.Sprintf(format, args...),
		Do: func(_ *ReleaseContext) (res string, err error) {
			return "", nil
		},
	}
}

func (r *Releaser) releaseActionClone() ReleaseAction {
	return ReleaseAction{
		Name:        "clone",
		Description: "Clone the config repo.",
		Do: func(rc *ReleaseContext) (res string, err error) {
			err = rc.CloneRepo()
			if err != nil {
				return "", errors.Wrap(err, "clone the config repo")
			}
			return "Clone OK.", nil
		},
	}
}

func (r *Releaser) releaseActionFindPodController(service flux.ServiceID) ReleaseAction {
	return ReleaseAction{
		Name:        "find_pod_controller",
		Description: fmt.Sprintf("Load the resource definition file for service %s", service),
		Do: func(rc *ReleaseContext) (res string, err error) {
			resourcePath := rc.RepoPath()
			if fi, err := os.Stat(resourcePath); err != nil || !fi.IsDir() {
				return "", fmt.Errorf("the resource path (%s) is not valid", resourcePath)
			}

			namespace, serviceName := service.Components()
			files, err := kubernetes.FilesFor(resourcePath, namespace, serviceName)

			if err != nil {
				return "", errors.Wrapf(err, "finding resource definition file for %s", service)
			}
			if len(files) <= 0 { // fine; we'll just skip it
				return fmt.Sprintf("no resource definition file found for %s; skipping", service), nil
			}
			if len(files) > 1 {
				return "", fmt.Errorf("multiple resource definition files found for %s: %s", service, strings.Join(files, ", "))
			}

			def, err := ioutil.ReadFile(files[0]) // TODO(mb) not multi-doc safe
			if err != nil {
				return "", err
			}
			rc.PodControllers[service] = def
			return "Found pod controller OK.", nil
		},
	}
}

func (r *Releaser) releaseActionUpdatePodController(service flux.ServiceID, updates []ContainerUpdate) ReleaseAction {
	var actions []string
	for _, update := range updates {
		actions = append(actions, fmt.Sprintf("%s (%s -> %s)", update.Container, update.Current, update.Target))
	}
	actionList := strings.Join(actions, ", ")

	return ReleaseAction{
		Name:        "update_pod_controller",
		Description: fmt.Sprintf("Update %d images(s) in the resource definition file for %s: %s.", len(updates), service, actionList),
		Do: func(rc *ReleaseContext) (res string, err error) {
			resourcePath := rc.RepoPath()
			if fi, err := os.Stat(resourcePath); err != nil || !fi.IsDir() {
				return "", fmt.Errorf("the resource path (%s) is not valid", resourcePath)
			}

			namespace, serviceName := service.Components()
			files, err := kubernetes.FilesFor(resourcePath, namespace, serviceName)
			if err != nil {
				return "", errors.Wrapf(err, "finding resource definition file for %s", service)
			}
			if len(files) <= 0 {
				return fmt.Sprintf("no resource definition file found for %s; skipping", service), nil
			}
			if len(files) > 1 {
				return "", fmt.Errorf("multiple resource definition files found for %s: %s", service, strings.Join(files, ", "))
			}

			def, err := ioutil.ReadFile(files[0])
			if err != nil {
				return "", err
			}
			fi, err := os.Stat(files[0])
			if err != nil {
				return "", err
			}

			for _, update := range updates {
				// Note 1: UpdatePodController parses the target (new) image
				// name, extracts the repository, and only mutates the line(s)
				// in the definition that match it. So for the time being we
				// ignore the current image. UpdatePodController could be
				// updated, if necessary.
				//
				// Note 2: we keep overwriting the same def, to handle multiple
				// images in a single file.
				def, err = kubernetes.UpdatePodController(def, string(update.Target), ioutil.Discard)
				if err != nil {
					return "", errors.Wrapf(err, "updating pod controller for %s", update.Target)
				}
			}

			// Write the file back, so commit/push works.
			if err := ioutil.WriteFile(files[0], def, fi.Mode()); err != nil {
				return "", err
			}

			// Put the def in the map, so release works.
			rc.PodControllers[service] = def
			return "Update pod controller OK.", nil
		},
	}
}

func (r *Releaser) releaseActionCommitAndPush(msg string) ReleaseAction {
	return ReleaseAction{
		Name:        "commit_and_push",
		Description: "Commit and push the config repo.",
		Do: func(rc *ReleaseContext) (res string, err error) {
			if fi, err := os.Stat(rc.WorkingDir); err != nil || !fi.IsDir() {
				return "", fmt.Errorf("the repo path (%s) is not valid", rc.WorkingDir)
			}
			result, err := rc.CommitAndPush(msg)
			if err == nil && result == "" {
				return "Pushed commit: " + msg, nil
			}
			return result, err
		},
	}
}

func service2string(a []flux.ServiceID) []string {
	s := make([]string, len(a))
	for i := range a {
		s[i] = string(a[i])
	}
	return s
}

func (r *Releaser) releaseActionReleaseServices(services []flux.ServiceID, msg string) ReleaseAction {
	return ReleaseAction{
		Name:        "release_services",
		Description: fmt.Sprintf("Release %d service(s): %s.", len(services), strings.Join(service2string(services), ", ")),
		Do: func(rc *ReleaseContext) (res string, err error) {
			cause := strconv.Quote(msg)

			// We'll collect results for each service release.
			results := map[flux.ServiceID]error{}

			// Collect definitions for each service release.
			var defs []platform.ServiceDefinition
			// If we're regrading our own image, we want to do that
			// last, and "asynchronously" (meaning we probably won't
			// see the reply).
			var asyncDefs []platform.ServiceDefinition

			for _, service := range services {
				def, ok := rc.PodControllers[service]
				if !ok {
					results[service] = errors.New("no definition found; skipping release")
					continue
				}

				namespace, serviceName := service.Components()
				switch serviceName {
				case FluxServiceName, FluxDaemonName:
					rc.Instance.LogEvent(namespace, serviceName, "Starting "+cause+". (no result expected)")
					asyncDefs = append(asyncDefs, platform.ServiceDefinition{
						ServiceID:     service,
						NewDefinition: def,
					})
				default:
					rc.Instance.LogEvent(namespace, serviceName, "Starting "+cause)
					defs = append(defs, platform.ServiceDefinition{
						ServiceID:     service,
						NewDefinition: def,
					})
				}
			}

			// Execute the releases as a single transaction.
			// Splat any errors into our results map.
			transactionErr := rc.Instance.PlatformApply(defs)
			if transactionErr != nil {
				switch err := transactionErr.(type) {
				case platform.ApplyError:
					for id, applyErr := range err {
						results[id] = applyErr
					}
				default: // assume everything failed, if there was a coverall error
					for _, service := range services {
						results[service] = transactionErr
					}
				}
			}

			// Report individual service release results.
			for _, service := range services {
				namespace, serviceName := service.Components()
				switch serviceName {
				case FluxServiceName, FluxDaemonName:
					continue
				default:
					if err := results[service]; err == nil { // no entry = nil error
						rc.Instance.LogEvent(namespace, serviceName, msg+". done")
					} else {
						rc.Instance.LogEvent(namespace, serviceName, msg+". error: "+err.Error()+". failed")
					}
				}
			}

			// Lastly, services for which we don't expect a result
			// (i.e., ourselves). This will kick off the release in
			// the daemon, which will cause Kubernetes to restart the
			// service. In the meantime, however, we will have
			// finished recording what happened, as part of a graceful
			// shutdown. So the only thing that goes missing is the
			// result from this release call.
			if len(asyncDefs) > 0 {
				go func() {
					rc.Instance.PlatformApply(asyncDefs)
				}()
			}

			return "", transactionErr
		},
	}
}
