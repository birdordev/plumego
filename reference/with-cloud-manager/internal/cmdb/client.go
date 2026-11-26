package cmdb

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
)

type HTTPStatusError struct{ Status int }

func (e HTTPStatusError) Error() string {
	return fmt.Sprintf("CMDB snapshot API returned HTTP %d", e.Status)
}

var ErrSourceUnavailable = errors.New("CMDB snapshot API request failed")

func (s Store) Sync(ctx context.Context, base *url.URL, project Project, token string, transport http.RoundTripper) (Receipt, error) {
	key := MirrorKey{Source: base.String(), Scope: project}
	generation, err := s.reserve(ctx, key)
	if err != nil {
		return Receipt{}, err
	}
	target := base.JoinPath("v1", "tenants", url.PathEscape(project.Tenant), "projects", url.PathEscape(project.Project), "governance", "cmdb-snapshot")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return Receipt{}, errors.New("invalid CMDB snapshot request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", MediaType)
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Receipt{}, ctx.Err()
		}
		return Receipt{}, ErrSourceUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Receipt{}, HTTPStatusError{Status: response.StatusCode}
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != MediaType {
		return Receipt{}, ErrInvalidSnapshot
	}
	if response.ContentLength > maxSnapshotBytes {
		return Receipt{}, ErrSnapshotLimit
	}
	result, err := s.apply(ctx, key, generation, response.Body)
	if err != nil && ctx.Err() != nil {
		return Receipt{}, ctx.Err()
	}
	return result, err
}
