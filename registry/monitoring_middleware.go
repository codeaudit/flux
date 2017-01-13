// Monitoring middlewares for registry interfaces
package registry

import (
	"strconv"
	"time"

	"github.com/weaveworks/flux"
	fluxmetrics "github.com/weaveworks/flux/metrics"
)

type RegistryMonitoringMiddleware func(Client) Client

type registryMonitoringMiddleware struct {
	next    Client
	metrics Metrics
}

func NewRegistryMonitoringMiddleware(metrics Metrics) RegistryMonitoringMiddleware {
	return func(next Client) Client {
		return &registryMonitoringMiddleware{
			next:    next,
			metrics: metrics,
		}
	}
}

func (m *registryMonitoringMiddleware) GetRepository(repository string) (res []flux.ImageDescription, err error) {
	start := time.Now()
	res, err = m.next.GetRepository(repository)
	m.metrics.FetchDuration.With(
		LabelRepository, repository,
		fluxmetrics.LabelSuccess, strconv.FormatBool(err == nil),
	).Observe(time.Since(start).Seconds())
	return
}

func (m *registryMonitoringMiddleware) GetImage(repository string) (res flux.ImageDescription, err error) {
	start := time.Now()
	res, err = m.next.GetImage(repository)
	m.metrics.FetchDuration.With(
		LabelRepository, repository,
		fluxmetrics.LabelSuccess, strconv.FormatBool(err == nil),
	).Observe(time.Since(start).Seconds())
	return
}

type RemoteMonitoringMiddleware func(Remote) Remote

type remoteMonitoringMiddleware struct {
	next    Remote
	metrics Metrics
	id      flux.ImageID
}

func NewRemoteMonitoringMiddleware(metrics Metrics, id flux.ImageID) RemoteMonitoringMiddleware {
	return func(next Remote) Remote {
		return &remoteMonitoringMiddleware{
			next:    next,
			metrics: metrics,
			id:      id,
		}
	}
}

func (m *remoteMonitoringMiddleware) Lookup() (flux.ImageDescription, error) {
	return m.next.Lookup()
}

func (m *remoteMonitoringMiddleware) LookupTag(tag string) (res flux.ImageDescription, err error) {
	start := time.Now()
	res, err = m.next.LookupTag(tag)
	m.metrics.RequestDuration.With(
		LabelRepository, m.id.Repository(),
		LabelRequestKind, RequestKindMetadata,
		fluxmetrics.LabelSuccess, strconv.FormatBool(err == nil),
	).Observe(time.Since(start).Seconds())
	return
}

func (m *remoteMonitoringMiddleware) Tags() (res []string, err error) {
	start := time.Now()
	res, err = m.next.Tags()
	m.metrics.RequestDuration.With(
		LabelRepository, m.id.Repository(),
		LabelRequestKind, RequestKindTags,
		fluxmetrics.LabelSuccess, strconv.FormatBool(err == nil),
	).Observe(time.Since(start).Seconds())
	return
}

func (m *remoteMonitoringMiddleware) Cancel() {
	m.next.Cancel()
}
