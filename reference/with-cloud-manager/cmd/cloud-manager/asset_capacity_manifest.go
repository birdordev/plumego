package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"with-cloud-manager/internal/provider"
)

type assetCapacityManifest struct {
	Tenant          string                          `json:"tenant"`
	Project         string                          `json:"project"`
	Rate            int                             `json:"requests_per_second"`
	DurationSeconds int                             `json:"duration_seconds"`
	Workers         int                             `json:"workers"`
	MaxP95MS        int                             `json:"maximum_p95_ms"`
	Cases           []assetCapacityQueryCase        `json:"cases"`
	Inventory       *assetCapacityInventoryManifest `json:"inventory,omitempty"`
}

type assetCapacityQueryCase struct {
	Name            string                 `json:"name"`
	Kind            provider.Kind          `json:"kind,omitempty"`
	Limit           int                    `json:"limit"`
	Account         string                 `json:"account,omitempty"`
	Region          string                 `json:"region,omitempty"`
	Search          string                 `json:"q,omitempty"`
	ExpectedItems   *int                   `json:"expected_items"`
	AllowedProjects []string               `json:"allowed_projects,omitempty"`
	RequiredAsset   *assetCapacityIdentity `json:"required_asset,omitempty"`
}

type assetCapacityIdentity struct {
	Project string `json:"project"`
	Account string `json:"account"`
	Region  string `json:"region"`
	ID      string `json:"id"`
}

type assetCapacityPage struct {
	Items []struct {
		assetCapacityIdentity
		Kind provider.Kind `json:"kind"`
	} `json:"items"`
}

func readAssetCapacityManifest(input io.Reader, tenant, project string) (assetCapacityManifest, string, error) {
	var m assetCapacityManifest
	data, err := readOperationInput(input, inventoryScopeManifestLimit+(128<<10))
	if err != nil {
		return m, "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil || m.Tenant != tenant || m.Project != project ||
		m.Rate < 1 || m.Rate > 1000 || m.DurationSeconds < 1 || m.DurationSeconds > 600 ||
		m.Rate*m.DurationSeconds > 120000 || m.Workers < 1 || m.Workers > 128 || m.MaxP95MS < 1 || m.MaxP95MS > 5000 ||
		len(m.Cases) < 1 || len(m.Cases) > min(32, m.Rate*m.DurationSeconds) {
		return assetCapacityManifest{}, "", errors.New("invalid asset capacity workload or project")
	}
	names := make(map[string]bool, len(m.Cases))
	for i := range m.Cases {
		c := &m.Cases[i]
		if len(c.AllowedProjects) == 0 {
			c.AllowedProjects = []string{project}
		}
		if names[c.Name] || !c.valid() {
			return assetCapacityManifest{}, "", errors.New("invalid or duplicate asset capacity query case")
		}
		names[c.Name] = true
	}
	digest := sha256.Sum256(data)
	return m, hex.EncodeToString(digest[:]), nil
}

func (c assetCapacityQueryCase) valid() bool {
	if !operationClientSegment(c.Name) || (c.Kind != "" && !c.Kind.Valid()) || c.Limit < 1 || c.Limit > 200 ||
		c.ExpectedItems == nil || *c.ExpectedItems < 0 || *c.ExpectedItems > c.Limit ||
		!validCapacityText(c.Account, 256) || !validCapacityText(c.Region, 256) || !validCapacityText(c.Search, 256) ||
		len(c.AllowedProjects) > 32 {
		return false
	}
	for i, project := range c.AllowedProjects {
		if !operationClientSegment(project) || slices.Contains(c.AllowedProjects[:i], project) {
			return false
		}
	}
	id := c.RequiredAsset
	return id == nil || *c.ExpectedItems > 0 && slices.Contains(c.AllowedProjects, id.Project) &&
		id.Account != "" && validCapacityText(id.Account, 256) && (c.Account == "" || c.Account == id.Account) &&
		id.Region != "" && validCapacityText(id.Region, 256) && (c.Region == "" || c.Region == id.Region) &&
		id.ID != "" && validCapacityText(id.ID, provider.MaxObjectIDSize)
}

func validCapacityText(value string, limit int) bool {
	return len(value) <= limit && !strings.ContainsFunc(value, unicode.IsControl)
}

func (c assetCapacityQueryCase) target(base *url.URL) *url.URL {
	target := *base
	values := url.Values{"limit": {strconv.Itoa(c.Limit)}}
	for name, value := range map[string]string{"kind": string(c.Kind), "account": c.Account, "region": c.Region, "q": c.Search} {
		if value != "" {
			values.Set(name, value)
		}
	}
	target.RawQuery = values.Encode()
	return &target
}

func (c assetCapacityQueryCase) check(page assetCapacityPage) error {
	if page.Items == nil || len(page.Items) != *c.ExpectedItems {
		return errors.New("asset API returned an unexpected page size")
	}
	found := c.RequiredAsset == nil
	for _, item := range page.Items {
		if item.ID == "" || item.Account == "" || item.Region == "" || !item.Kind.Valid() || (c.Kind != "" && item.Kind != c.Kind) ||
			!slices.Contains(c.AllowedProjects, item.Project) ||
			(c.Account != "" && item.Account != c.Account) || (c.Region != "" && item.Region != c.Region) {
			return errors.New("asset API returned an item outside the expected scope")
		}
		if required := c.RequiredAsset; required != nil && item.assetCapacityIdentity == *required {
			found = true
		}
	}
	if !found {
		return errors.New("asset API did not return the required item")
	}
	return nil
}
