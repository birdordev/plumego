package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"with-cloud-manager/internal/provider"
)

const maxWatchEvents = 256

type watchEvent struct {
	Type   string          `json:"type"`
	Object json.RawMessage `json:"object"`
}

func (c *Client) watch(ctx context.Context, source workloadSource, base collectionSnapshot, budget *observationBudget) (collectionSnapshot, error) {
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	items := make(map[string]provider.ClusterWorkload, len(base.items))
	for _, item := range base.items {
		if _, exists := items[item.Name]; exists {
			return collectionSnapshot{}, invalid
		}
		items[item.Name] = item
	}
	query := url.Values{"watch": {"true"}, "allowWatchBookmarks": {"true"}, "resourceVersion": {base.version}, "timeoutSeconds": {"5"}}
	resp, err := c.openJSON(ctx, source.path(), query)
	if err != nil {
		return collectionSnapshot{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	bounded := &io.LimitedReader{R: budget.reader(resp.Body), N: maxPageBytes + 1}
	decoder := json.NewDecoder(bounded)
	version := base.version
	for i := 0; i < maxWatchEvents; i++ {
		var event watchEvent
		if err := decoder.Decode(&event); err != nil {
			if ctx.Err() != nil {
				return collectionSnapshot{}, ctx.Err()
			}
			if err == io.EOF || (i > 0 && errors.Is(err, io.ErrUnexpectedEOF)) {
				break
			}
			return collectionSnapshot{}, invalid
		}
		if bounded.N == 0 {
			return collectionSnapshot{}, invalid
		}
		checkpoint, err := event.apply(source, items)
		if err != nil {
			return collectionSnapshot{}, err
		}
		version = checkpoint
	}
	updated := collectionSnapshot{version: version, items: make([]provider.ClusterWorkload, 0, len(items))}
	for _, item := range items {
		updated.items = append(updated.items, item)
	}
	return updated, nil
}

func (event watchEvent) apply(source workloadSource, items map[string]provider.ClusterWorkload) (string, error) {
	invalid := &provider.Failure{Kind: provider.InvalidResponse}
	if event.Type == "ERROR" {
		return "", watchStatusError(event.Object)
	}
	var object workloadObject
	if err := json.Unmarshal(event.Object, &object); err != nil || !validResourceVersion(object.Metadata.ResourceVersion) ||
		(object.Kind != "" && object.Kind != source.kind) || (object.Version != "" && object.Version != source.version) {
		return "", invalid
	}
	version := object.Metadata.ResourceVersion
	if event.Type == "BOOKMARK" {
		if object.Metadata.Namespace != "" && object.Metadata.Namespace != source.namespace {
			return "", invalid
		}
		return version, nil
	}
	item, valid := source.project(object)
	if !valid {
		return "", invalid
	}
	previous, exists := items[item.Name]
	switch event.Type {
	case "ADDED", "MODIFIED":
		if exists && previous.UID != item.UID || !exists && len(items) >= provider.MaxClusterWorkloads {
			return "", invalid
		}
		items[item.Name] = item
	case "DELETED":
		if exists && previous.UID == item.UID {
			delete(items, item.Name)
		}
	default:
		return "", invalid
	}
	return version, nil
}

func watchStatusError(raw json.RawMessage) error {
	var status struct {
		Kind    string `json:"kind"`
		Code    int    `json:"code"`
		Reason  string `json:"reason"`
		Details struct {
			RetryAfterSeconds int64 `json:"retryAfterSeconds"`
		} `json:"details"`
	}
	if json.Unmarshal(raw, &status) != nil || status.Kind != "Status" {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	switch status.Code {
	case http.StatusGone:
		return &provider.Failure{Kind: provider.ExpiredCursor}
	case http.StatusTooManyRequests:
		delay := time.Duration(max(0, min(status.Details.RetryAfterSeconds, 86400))) * time.Second
		return &provider.Failure{Kind: provider.Throttled, RetryAfter: delay}
	}
	if status.Code == 0 && (status.Reason == "Expired" || status.Reason == "Gone") {
		return &provider.Failure{Kind: provider.ExpiredCursor}
	}
	return responseFailure(&http.Response{StatusCode: status.Code})
}
