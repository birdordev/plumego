package cmdb

import (
	"errors"
	"time"

	"with-cloud-manager/internal/inventory"
)

const MediaType = "application/x-ndjson"
const SchemaVersion = 1
const MaxAssets = 250_000

const (
	maxLineBytes     = 256 << 10
	maxSnapshotBytes = 256 << 20
)

var (
	ErrInvalidSnapshot = errors.New("invalid or incomplete CMDB snapshot")
	ErrSnapshotLimit   = errors.New("CMDB snapshot exceeds its limits")
)

type Project struct {
	Tenant  string `json:"tenant"`
	Project string `json:"project"`
}

type Manifest struct {
	Project
	SchemaVersion int       `json:"schema_version"`
	CapturedAt    time.Time `json:"captured_at"`
}

type Summary struct {
	Count  int    `json:"count"`
	SHA256 string `json:"sha256"`
}

type record struct {
	Type     string               `json:"type"`
	Manifest *Manifest            `json:"manifest,omitempty"`
	Asset    *inventory.CMDBAsset `json:"asset,omitempty"`
	Summary  *Summary             `json:"summary,omitempty"`
}
