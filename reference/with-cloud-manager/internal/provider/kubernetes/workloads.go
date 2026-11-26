package kubernetes

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"with-cloud-manager/internal/provider"
)

const (
	pageSize      = 100
	maxPageBytes  = 4 << 20
	maxReadBytes  = 64 << 20
	maxListCalls  = 512
	maxCursorSize = 4096
)

type collection struct {
	version, resource, kind string
}

var workloadCollections = [...]collection{
	{"apps/v1", "deployments", "Deployment"},
	{"apps/v1", "statefulsets", "StatefulSet"},
	{"apps/v1", "daemonsets", "DaemonSet"},
	{"batch/v1", "jobs", "Job"},
	{"batch/v1", "cronjobs", "CronJob"},
}

type typeMeta struct {
	Version string `json:"apiVersion"`
	Kind    string `json:"kind"`
}

type objectMeta struct {
	Name            string `json:"name"`
	Namespace       string `json:"namespace"`
	UID             string `json:"uid"`
	ResourceVersion string `json:"resourceVersion"`
}

type listPage struct {
	typeMeta
	Metadata struct {
		ResourceVersion string `json:"resourceVersion"`
		Continue        string `json:"continue"`
	} `json:"metadata"`
	Items []workloadObject `json:"items"`
}

type workloadObject struct {
	typeMeta
	Metadata objectMeta `json:"metadata"`
	Spec     struct {
		Replicas    *int  `json:"replicas"`
		Completions *int  `json:"completions"`
		Suspend     *bool `json:"suspend"`
	} `json:"spec"`
	StatusFields struct {
		ReadyReplicas *int        `json:"readyReplicas"`
		DesiredNodes  *int        `json:"desiredNumberScheduled"`
		ReadyNodes    *int        `json:"numberReady"`
		Succeeded     *int        `json:"succeeded"`
		Failed        *int        `json:"failed"`
		Active        activeCount `json:"active"`
	} `json:"status"`
}

func (w workloadObject) workload(kind string) provider.ClusterWorkload {
	item := provider.ClusterWorkload{UID: w.Metadata.UID, Namespace: w.Metadata.Namespace, Kind: kind, Name: w.Metadata.Name}
	status := w.StatusFields
	switch kind {
	case "Deployment", "StatefulSet":
		item.Desired, item.Ready = w.Spec.Replicas, status.ReadyReplicas
	case "DaemonSet":
		item.Desired, item.Ready = status.DesiredNodes, status.ReadyNodes
	case "Job":
		item.Desired, item.Active = w.Spec.Completions, status.Active.number
		item.Succeeded, item.Failed, item.Suspended = status.Succeeded, status.Failed, w.Spec.Suspend
	case "CronJob":
		item.Active, item.Suspended = status.Active.references, w.Spec.Suspend
	}
	return item
}

type workloadSource struct {
	namespace string
	collection
}

func (s workloadSource) path() string {
	return "/apis/" + s.version + "/namespaces/" + s.namespace + "/" + s.resource
}

func (s workloadSource) project(object workloadObject) (provider.ClusterWorkload, bool) {
	item := object.workload(s.kind)
	return item, item.Namespace == s.namespace && (object.Kind == "" || object.Kind == s.kind) &&
		(object.Version == "" || object.Version == s.version) && object.StatusFields.Active.valid(s.kind) && item.Valid()
}

func (c *Client) sources() []workloadSource {
	sources := make([]workloadSource, 0, len(c.config.Namespaces)*len(workloadCollections))
	for _, namespace := range c.config.Namespaces {
		for _, source := range workloadCollections {
			sources = append(sources, workloadSource{namespace, source})
		}
	}
	return sources
}

func (c *Client) ReadWorkloads(ctx context.Context) ([]provider.ClusterWorkload, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := c.checkIdentity(ctx); err != nil {
		return nil, err
	}
	budget := newObservationBudget()
	items := make([]provider.ClusterWorkload, 0)
	for _, source := range c.sources() {
		fresh, err := c.list(ctx, source, budget)
		if err != nil {
			return nil, err
		}
		if len(items)+len(fresh.items) > provider.MaxClusterWorkloads {
			return nil, &provider.Failure{Kind: provider.InvalidResponse}
		}
		items = append(items, fresh.items...)
	}
	if err := c.checkIdentity(ctx); err != nil {
		return nil, err
	}
	if err := provider.NormalizeClusterWorkloads(items); err != nil {
		return nil, err
	}
	return items, nil
}

func (c *Client) checkIdentity(ctx context.Context) error {
	var ns struct {
		typeMeta
		Metadata objectMeta `json:"metadata"`
	}
	err := c.get(ctx, "/api/v1/namespaces/"+c.config.IdentityNamespace, nil, 128<<10, nil, &ns)
	if err != nil {
		return err
	}
	if ns.Version != "v1" || ns.Kind != "Namespace" || ns.Metadata.Name != c.config.IdentityNamespace || ns.Metadata.UID != c.config.IdentityUID {
		return &provider.Failure{Kind: provider.Unauthorized}
	}
	return nil
}

type collectionSnapshot struct {
	version string
	items   []provider.ClusterWorkload
}

func (c *Client) list(ctx context.Context, source workloadSource, budget *observationBudget) (collectionSnapshot, error) {
	query := url.Values{"limit": {strconv.Itoa(pageSize)}}
	seen := make(map[string]struct{})
	var result collectionSnapshot
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	for {
		if budget.lists.Add(1) > maxListCalls {
			return collectionSnapshot{}, invalid
		}
		var page listPage
		if err := c.get(ctx, source.path(), query, maxPageBytes, budget, &page); err != nil {
			return collectionSnapshot{}, err
		}
		version := page.Metadata.ResourceVersion
		if page.Version != source.version || page.Kind != source.kind+"List" || !validResourceVersion(version) ||
			(result.version != "" && version != result.version) || page.Items == nil || len(page.Items) > pageSize ||
			len(result.items)+len(page.Items) > provider.MaxClusterWorkloads {
			return collectionSnapshot{}, invalid
		}
		result.version = version
		for _, object := range page.Items {
			item, valid := source.project(object)
			if !valid {
				return collectionSnapshot{}, invalid
			}
			result.items = append(result.items, item)
		}
		cursor := page.Metadata.Continue
		if cursor == "" {
			return result, nil
		}
		if _, exists := seen[cursor]; exists || !validOpaque(cursor, maxCursorSize) {
			return collectionSnapshot{}, invalid
		}
		seen[cursor] = struct{}{}
		query.Set("continue", cursor)
	}
}

func validResourceVersion(version string) bool {
	return version != "0" && validOpaque(version, 256)
}

func validOpaque(value string, limit int) bool {
	return value != "" && len(value) <= limit && !strings.ContainsFunc(value, unicode.IsControl)
}
