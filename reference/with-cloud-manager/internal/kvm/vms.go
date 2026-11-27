package kvm

import (
	"context"
	"database/sql"
	"errors"

	"with-cloud-manager/internal/identity"
)

type VMInput struct {
	Placement string `json:"placement_id"`
	Volume    string `json:"boot_volume,omitempty"`
	ImageID   string `json:"image_id,omitempty"`
}

type VM struct {
	IPv4Assignment
	ImageID           string   `json:"image_id,omitempty"`
	ImageSHA256       string   `json:"image_sha256,omitempty"`
	ImageSizeBytes    int64    `json:"image_size_bytes,omitempty"`
	Subnet            string   `json:"subnet,omitempty"`
	Gateway           string   `json:"gateway,omitempty"`
	ID                string   `json:"id"`
	Placement         string   `json:"placement_id"`
	Host              string   `json:"host"`
	Generation        int64    `json:"generation"`
	Network           string   `json:"network"`
	StoragePool       string   `json:"storage_pool"`
	Volume            string   `json:"boot_volume"`
	Capacity          Capacity `json:"capacity"`
	State             string   `json:"state"`
	Revision          int64    `json:"revision"`
	Observation       string   `json:"observation,omitempty"`
	SharedStoragePool string   `json:"shared_storage_pool_id,omitempty"`
}

type vmRecord struct {
	VM
	requester, reviewer                                 string
	requesterRevision, reviewerRevision                 int64
	shutdownRequester, shutdownReviewer                 string
	shutdownRequesterRevision, shutdownReviewerRevision int64
	retireRequester, retireReviewer                     string
	retireRequesterRevision, retireReviewerRevision     int64
}

const vmColumns = `v.vm_id::text,p.placement_id,p.host_id,p.generation,p.network_id,p.storage_pool,p.boot_volume,
	p.vcpu,p.memory_mib,p.disk_mib,p.gpu,v.state,v.revision,v.observation,p.requester_id,v.reviewer_id,
	v.requester_revision,v.reviewer_revision,v.shutdown_requester_id,v.shutdown_reviewer_id,
	v.shutdown_requester_revision,v.shutdown_reviewer_revision,
	v.retire_requester_id,v.retire_reviewer_id,v.retire_requester_revision,v.retire_reviewer_revision,
	COALESCE(host(p.ipv4),''),COALESCE(n.subnet::text,''),COALESCE(host(n.gateway),''),
	COALESCE(i.image_id,''),COALESCE(i.sha256,''),COALESCE(i.size_bytes,0),COALESCE(p.shared_storage_pool_id,'')`
const vmFrom = ` FROM cmp_kvm_vms AS v JOIN cmp_kvm_placements AS p USING (placement_id)
	LEFT JOIN cmp_kvm_ipv4_pools AS n ON (n.tenant_id,n.project_id,n.site_id,n.network_id)=(p.tenant_id,p.project_id,p.site_id,p.ipam_network_id)
	LEFT JOIN cmp_kvm_boot_images AS i ON (i.tenant_id,i.project_id,i.site_id,i.image_id)=(p.tenant_id,p.project_id,p.site_id,p.image_id)`

func scanVM(row interface{ Scan(...any) error }) (vmRecord, error) {
	var v vmRecord
	err := row.Scan(&v.ID, &v.Placement, &v.Host, &v.Generation, &v.Network, &v.StoragePool, &v.Volume,
		&v.Capacity.VCPU, &v.Capacity.MemoryMiB, &v.Capacity.DiskMiB, &v.Capacity.GPU,
		&v.State, &v.Revision, &v.Observation, &v.requester, &v.reviewer, &v.requesterRevision, &v.reviewerRevision,
		&v.shutdownRequester, &v.shutdownReviewer, &v.shutdownRequesterRevision, &v.shutdownReviewerRevision,
		&v.retireRequester, &v.retireReviewer, &v.retireRequesterRevision, &v.retireReviewerRevision, &v.Address, &v.Subnet, &v.Gateway,
		&v.ImageID, &v.ImageSHA256, &v.ImageSizeBytes, &v.SharedStoragePool)
	v.MAC = IPv4MAC(v.Address)
	return v, err
}

func readVM(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, id string, lock bool) (vmRecord, error) {
	query := "SELECT " + vmColumns + vmFrom + " WHERE (p.tenant_id,p.project_id,p.site_id,v.vm_id)=($1,$2,$3,$4::uuid)"
	if lock {
		query += " FOR UPDATE OF v"
	}
	v, err := scanVM(tx.QueryRowContext(ctx, query, actor.Tenant, actor.Project, site, id))
	return v, hiddenMissing(err)
}

func (s Store) GetVM(ctx context.Context, token string, scope identity.Scope, site, id string) (VM, error) {
	if !hostUUID.MatchString(id) {
		return VM{}, ErrInvalid
	}
	var result vmRecord
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		var err error
		result, err = readVM(ctx, tx, actor, site, id, false)
		return err
	})
	return result.VM, err
}

func (s Store) SubmitVM(ctx context.Context, token string, scope identity.Scope, site string, input VMInput) (VM, error) {
	if !validVMInput(input) {
		return VM{}, ErrInvalid
	}
	var result vmRecord
	err := s.inProject(ctx, token, scope, site, identity.Execute, false, func(tx *sql.Tx, actor identity.Scope, member identity.Membership) error {
		var hostID, network string
		err := tx.QueryRowContext(ctx, `SELECT host_id,network_id FROM cmp_kvm_placements
			WHERE (tenant_id,project_id,site_id,placement_id,requester_id)=($1,$2,$3,$4,$5)`,
			actor.Tenant, actor.Project, site, input.Placement, actor.Principal).Scan(&hostID, &network)
		if err != nil {
			return hiddenMissing(err)
		}
		if err := lockIPv4Network(ctx, tx, actor, site, network, false); err != nil {
			return err
		}
		host, err := readHost(ctx, tx, actor, site, hostID, true)
		if err != nil {
			return err
		}
		// 所有绑定依次锁宿主机与预留，防止到期回收和提交同时成功。
		var image, volume string
		p, err := scanPlacement(tx.QueryRowContext(ctx, "SELECT "+placementColumns+`,COALESCE(image_id,''),boot_volume
			FROM cmp_kvm_placements WHERE placement_id=$1 FOR UPDATE`, input.Placement), &image, &volume)
		if err != nil {
			return err
		}
		prior, err := scanVM(tx.QueryRowContext(ctx, "SELECT "+vmColumns+vmFrom+`
			WHERE v.vm_id=COALESCE((SELECT vm_id FROM cmp_kvm_vms WHERE placement_id=$1),
				(SELECT vm_id FROM cmp_kvm_cold_migrations WHERE source_placement_id=$1 AND status='approved'))`, p.ID))
		if err == nil {
			if image != input.ImageID || (image == "" && volume != input.Volume) {
				return ErrConflict
			}
			result = prior
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if p.State != "held" || p.Generation != host.Generation || host.Draining || p.Capacity.GPU != 0 || p.EvacuationVM != "" {
			return ErrConflict
		}
		if p.SharedStoragePool != "" {
			if p.SharedStoragePool != host.SharedStoragePool {
				return ErrConflict
			}
			ok, err := fitsCurrentCapacity(ctx, tx, actor, host, Capacity{DiskMiB: p.Capacity.DiskMiB})
			if err != nil {
				return err
			}
			if !ok {
				return ErrNoCapacity
			}
		}
		volume, err = reserveBootVolume(ctx, tx, actor, site, p, input)
		if err != nil {
			return err
		}
		if err := consumeVMPlacement(ctx, tx, p.ID, volume, input.ImageID); err != nil {
			return err
		}
		var id string
		if err := tx.QueryRowContext(ctx, `INSERT INTO cmp_kvm_vms(placement_id,requester_revision) VALUES ($1,$2)
			RETURNING vm_id::text`, p.ID, member.Revision).Scan(&id); err != nil {
			return err
		}
		result, err = readVM(ctx, tx, actor, site, id, false)
		if err != nil {
			return err
		}
		return recordEvent(ctx, tx, actor, site, p.Host, "vm_submit", p.ID)
	})
	return result.VM, err
}

func consumeVMPlacement(ctx context.Context, tx *sql.Tx, placement, volume, image string) error {
	var changed string
	err := tx.QueryRowContext(ctx, `UPDATE cmp_kvm_placements SET consumed_at=clock_timestamp(),boot_volume=$2,image_id=NULLIF($3,'')
		WHERE placement_id=$1 AND expires_at>clock_timestamp() AND consumed_at IS NULL
		  AND released_at IS NULL AND expired_at IS NULL RETURNING placement_id`, placement, volume, image).Scan(&changed)
	if errors.Is(err, sql.ErrNoRows) || uniqueViolation(err) {
		return ErrConflict
	}
	return err
}
