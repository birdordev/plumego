package kvm

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"with-cloud-manager/internal/identity"
)

const HostImageTTL = 30 * time.Minute

var ErrNoVerifiedTarget = errors.New("no eligible KVM target with a fresh verified backup")

type HostImageReport struct {
	AgentIdentity
	Revision  int64  `json:"revision"`
	State     string `json:"state"`
	SHA256    string `json:"image_sha256"`
	SizeBytes int64  `json:"image_size_bytes"`
}

type HostImageReceipt struct {
	BootImageSpec
	Host       string     `json:"host"`
	Generation int64      `json:"generation"`
	Revision   int64      `json:"revision"`
	State      string     `json:"state"`
	Available  bool       `json:"available"`
	ReportedAt *time.Time `json:"reported_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

const hostImageKey = `(tenant_id,project_id,site_id,host_id,image_id)=($1,$2,$3,$4,$5)`

const hostImageAvailable = `cache.state='verified'
	AND cache.reported_at<=statement_timestamp() AND cache.expires_at>statement_timestamp()`

func (s Store) GetHostImage(ctx context.Context, token string, scope identity.Scope, site, host, image string) (HostImageReceipt, error) {
	if !identifier.MatchString(host) || !identifier.MatchString(image) {
		return HostImageReceipt{}, ErrInvalid
	}
	var receipt HostImageReceipt
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		target, err := readHost(ctx, tx, actor, site, host, false)
		if err != nil {
			return err
		}
		receipt, err = readHostImageReceipt(ctx, tx, actor, target, image)
		return err
	})
	return receipt, err
}

func (s Store) CheckHostImage(ctx context.Context, token string, scope identity.Scope, site, host, image string, agent AgentIdentity) (HostImageReceipt, error) {
	if !identifier.MatchString(image) {
		return HostImageReceipt{}, ErrInvalid
	}
	var receipt HostImageReceipt
	err := s.withVMHost(ctx, token, scope, site, host, agent, func(tx *sql.Tx, actor identity.Scope, target Host) error {
		var err error
		receipt, err = readHostImageReceipt(ctx, tx, actor, target, image)
		return err
	})
	return receipt, err
}

func (s Store) ReportHostImage(ctx context.Context, token string, scope identity.Scope, site, host, image string, report HostImageReport) (HostImageReceipt, error) {
	spec := BootImageSpec{ID: image, SHA256: report.SHA256, SizeBytes: report.SizeBytes}
	if !spec.Valid() || report.Revision < 0 || (report.State != "verified" && report.State != "missing") {
		return HostImageReceipt{}, ErrInvalid
	}
	var receipt HostImageReceipt
	err := s.withVMHost(ctx, token, scope, site, host, report.AgentIdentity, func(tx *sql.Tx, actor identity.Scope, target Host) error {
		current, err := readHostImageReceipt(ctx, tx, actor, target, image)
		if err != nil {
			return err
		}
		if current.BootImageSpec != spec {
			return ErrConflict
		}
		if current.Revision > 0 && current.Revision-1 == report.Revision && current.State == report.State {
			receipt = current
			return nil
		}
		if current.Revision != report.Revision {
			return ErrStale
		}
		receipt, err = writeHostImageReceipt(ctx, tx, actor, target, current, report.State)
		return err
	})
	if err != nil {
		return HostImageReceipt{}, err
	}
	return receipt, nil
}

func readHostImageReceipt(ctx context.Context, tx *sql.Tx, actor identity.Scope, target Host, image string) (HostImageReceipt, error) {
	spec, err := readBootImage(ctx, tx, actor, target.Site, image)
	if err != nil {
		return HostImageReceipt{}, err
	}
	result := HostImageReceipt{BootImageSpec: spec.BootImageSpec, Host: target.ID, Generation: target.Generation, State: "unknown"}
	err = tx.QueryRowContext(ctx, `SELECT revision,state,reported_at,expires_at,COALESCE(`+hostImageAvailable+`,false) AND $8=''
		FROM cmp_kvm_host_images AS cache WHERE `+hostImageKey+` AND host_generation=$6 AND agent_id=$7`,
		actor.Tenant, actor.Project, target.Site, target.ID, image, target.Generation, target.Agent, target.FenceState).
		Scan(&result.Revision, &result.State, &result.ReportedAt, &result.ExpiresAt, &result.Available)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	return result, err
}

func writeHostImageReceipt(ctx context.Context, tx *sql.Tx, actor identity.Scope, target Host, receipt HostImageReceipt, state string) (HostImageReceipt, error) {
	err := tx.QueryRowContext(ctx, `INSERT INTO cmp_kvm_host_images
		(tenant_id,project_id,site_id,host_id,image_id,host_generation,agent_id,revision,state,reported_at,expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,statement_timestamp(),
			CASE WHEN $9='verified' THEN statement_timestamp()+$10*interval '1 second' END)
		ON CONFLICT (tenant_id,project_id,site_id,host_id,image_id) DO UPDATE SET
			host_generation=EXCLUDED.host_generation,agent_id=EXCLUDED.agent_id,revision=EXCLUDED.revision,
			state=EXCLUDED.state,reported_at=EXCLUDED.reported_at,expires_at=EXCLUDED.expires_at
		RETURNING revision,state,reported_at,expires_at`,
		actor.Tenant, actor.Project, target.Site, target.ID, receipt.ID, target.Generation, actor.Principal,
		receipt.Revision+1, state, int64(HostImageTTL/time.Second)).
		Scan(&receipt.Revision, &receipt.State, &receipt.ReportedAt, &receipt.ExpiresAt)
	receipt.Available = state == "verified"
	return receipt, err
}

func requireVerifiedHostImage(ctx context.Context, tx *sql.Tx, actor identity.Scope, target Host, image string) error {
	var available bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cmp_kvm_host_images AS cache
		WHERE `+hostImageKey+` AND host_generation=$6 AND agent_id=$7 AND `+hostImageAvailable+`)`,
		actor.Tenant, actor.Project, target.Site, target.ID, image, target.Generation, target.Agent).Scan(&available)
	if err == nil && !available {
		return ErrNoVerifiedTarget
	}
	return err
}
