package kvm

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"regexp"
	"strings"
	"time"

	"with-cloud-manager/internal/identity"
)

const maxBootImageBytes = 32 << 30
const bootImageVolumePrefix = "cmp-image-"

var bootImageDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

type BootImageSpec struct {
	ID        string `json:"image_id,omitempty"`
	SHA256    string `json:"image_sha256,omitempty"`
	SizeBytes int64  `json:"image_size_bytes,omitempty"`
}

func (image BootImageSpec) Valid() bool {
	return identifier.MatchString(image.ID) && bootImageDigest.MatchString(image.SHA256) &&
		image.SizeBytes > 0 && image.SizeBytes <= maxBootImageBytes
}

type BootImage struct {
	BootImageSpec
	Site      string    `json:"site"`
	CreatedAt time.Time `json:"created_at"`
}

func ImageVolumeName(placement string) string {
	digest := sha256.Sum256([]byte(placement))
	return bootImageVolumePrefix + hex.EncodeToString(digest[:16]) + ".raw"
}

func validVMInput(input VMInput) bool {
	if !placementKey.MatchString(input.Placement) {
		return false
	}
	if input.ImageID != "" {
		return input.Volume == "" && identifier.MatchString(input.ImageID)
	}
	return identifier.MatchString(input.Volume)
}

func (s Store) RegisterBootImage(ctx context.Context, token string, scope identity.Scope, site string, image BootImageSpec) (BootImage, error) {
	if !image.Valid() {
		return BootImage{}, ErrInvalid
	}
	var result BootImage
	err := s.inProject(ctx, token, scope, site, identity.Manage, false, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		err := tx.QueryRowContext(ctx, `INSERT INTO cmp_kvm_boot_images
			(tenant_id,project_id,site_id,image_id,sha256,size_bytes,created_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`,
			actor.Tenant, actor.Project, site, image.ID, image.SHA256, image.SizeBytes, actor.Principal).Scan(&result.CreatedAt)
		if uniqueViolation(err) {
			return ErrConflict
		}
		result.BootImageSpec, result.Site = image, site
		return err
	})
	return result, err
}

func (s Store) GetBootImage(ctx context.Context, token string, scope identity.Scope, site, id string) (BootImage, error) {
	if !identifier.MatchString(id) {
		return BootImage{}, ErrInvalid
	}
	var result BootImage
	err := s.inProject(ctx, token, scope, site, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		var err error
		result, err = readBootImage(ctx, tx, actor, site, id)
		return err
	})
	return result, err
}

func readBootImage(ctx context.Context, tx *sql.Tx, actor identity.Scope, site, id string) (BootImage, error) {
	result := BootImage{BootImageSpec: BootImageSpec{ID: id}, Site: site}
	err := tx.QueryRowContext(ctx, `SELECT sha256,size_bytes,created_at FROM cmp_kvm_boot_images
		WHERE (tenant_id,project_id,site_id,image_id)=($1,$2,$3,$4)`,
		actor.Tenant, actor.Project, site, id).Scan(&result.SHA256, &result.SizeBytes, &result.CreatedAt)
	return result, hiddenMissing(err)
}

func reserveBootVolume(ctx context.Context, tx *sql.Tx, actor identity.Scope, site string, p Placement, input VMInput) (string, error) {
	volume := input.Volume
	if input.ImageID != "" {
		image, err := readBootImage(ctx, tx, actor, site, input.ImageID)
		if err != nil {
			return "", err
		}
		if image.SizeBytes > p.Capacity.DiskMiB*(1<<20) {
			return "", ErrConflict
		}
		volume = ImageVolumeName(p.ID)
	} else if strings.HasPrefix(volume, bootImageVolumePrefix) {
		return "", ErrInvalid
	}
	return volume, nil
}
