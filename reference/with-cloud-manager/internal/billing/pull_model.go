package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"with-cloud-manager/internal/dbschema"
	"with-cloud-manager/internal/provider"
)

var ErrPullProfile = errors.New("billing pull profile generation conflicts with the active configuration")
var ErrPullLease = errors.New("billing pull lease expired or was replaced")

const (
	MinPullInterval = 60
	MaxPullInterval = 86400
	pullTimeout     = 60 * time.Second
	pullLease       = 90 * time.Second
)

type PullJob struct {
	Key
	Generation      int64        `json:"generation"`
	IntervalSeconds int          `json:"interval_seconds"`
	Region          string       `json:"region"`
	Bucket          string       `json:"bucket"`
	Manifest        Manifest     `json:"manifest"`
	Objects         []PullObject `json:"objects"`
}

type PullReader interface {
	Head(context.Context, string, string) (provider.ObjectMeta, error)
	Read(context.Context, string, provider.ObjectMeta, int64) ([]byte, error)
}

type Puller struct {
	store  Store
	job    PullJob
	id     string
	digest string
	reader PullReader
}

type PullStatus struct {
	Outcome        string     `json:"outcome"`
	Generation     int64      `json:"generation"`
	Observation    string     `json:"observation_sha256,omitempty"`
	ReportRevision *int64     `json:"report_revision,omitempty"`
	NextAt         time.Time  `json:"next_at"`
	AttemptedAt    *time.Time `json:"attempted_at,omitempty"`
	Failures       int        `json:"failures"`
}

func MigratePulls(ctx context.Context, db *sql.DB) error {
	return dbschema.Apply(ctx, db, "0096_billing_pull")
}

func pullJobID(key Key) string {
	return digestOf([]byte(strings.Join([]string{key.Tenant, key.Project, key.Account, key.Period, key.Source}, "\x00")))
}

func NewPuller(store Store, job PullJob, reader PullReader) (Puller, error) {
	if !job.Valid() || job.Generation < 1 || job.IntervalSeconds < MinPullInterval || job.IntervalSeconds > MaxPullInterval ||
		!validID(job.Region) || !validID(job.Bucket) || reader == nil || len(job.Objects) < 1 || len(job.Objects) > MaxParts ||
		job.Manifest.validate(job.Source) != nil {
		return Puller{}, ErrInvalid
	}
	objects := slices.Clone(job.Objects)
	slices.SortFunc(objects, func(a, b PullObject) int { return strings.Compare(a.Key, b.Key) })
	var parts int
	for i := range objects {
		object := &objects[i]
		if !object.valid() || i > 0 && objects[i-1].Key == object.Key {
			return Puller{}, ErrInvalid
		}
		object.Entries = slices.Clone(object.Entries)
		slices.Sort(object.Entries)
		parts += max(1, len(object.Entries))
	}
	if parts > MaxParts {
		return Puller{}, ErrInvalid
	}
	job.Objects = objects
	encoded, err := json.Marshal(job)
	if err != nil || len(encoded) > 128<<10 {
		return Puller{}, ErrInvalid
	}
	// 分离配置与签名凭据，调度表只记录非敏感配置的摘要。
	if err := json.Unmarshal(encoded, &job); err != nil {
		return Puller{}, ErrInvalid
	}
	return Puller{store: store, job: job, id: pullJobID(job.Key), digest: digestOf(encoded), reader: reader}, nil
}
