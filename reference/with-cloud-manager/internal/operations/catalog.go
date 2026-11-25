package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	dbutil "github.com/birdordev/plumego/store/db"

	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

//go:embed 0026_instance_catalog.sql
var catalogSchema string

var ErrCatalogStale = errors.New("catalog revision or version is no longer current")
var catalogID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

type CatalogSpec struct {
	Title         string `json:"title"`
	Account       string `json:"account"`
	Region        string `json:"region"`
	ImageID       string `json:"image_id"`
	Size          string `json:"size"`
	NetworkID     string `json:"network_id"`
	SecurityGroup string `json:"security_group_id"`
}

type CatalogCreate struct {
	ID string `json:"id"`
	CatalogSpec
}

type CatalogVersion struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
	CatalogSpec
	PublishedBy string    `json:"published_by"`
	PublishedAt time.Time `json:"published_at"`
}

type CatalogItem struct {
	CatalogVersion
	Revision int64 `json:"revision"`
	Enabled  bool  `json:"enabled"`
}

type CatalogPage struct {
	Items []CatalogItem `json:"items"`
	Next  string        `json:"next,omitempty"`
}

type CatalogLaunch struct {
	Version int64  `json:"version"`
	Name    string `json:"name"`
}

type catalogReference struct {
	id       string
	version  int64
	eligible bool
}

func (spec CatalogSpec) launchInput(name string) CreateInput {
	return CreateInput{Account: spec.Account, Region: spec.Region, InstanceSpec: provider.InstanceSpec{
		Name: name, ImageID: spec.ImageID, Size: spec.Size, NetworkID: spec.NetworkID, SecurityGroup: spec.SecurityGroup,
	}}
}

func validCatalogSpec(spec CatalogSpec) bool {
	return spec.Title != "" && len(spec.Title) <= 120 && utf8.ValidString(spec.Title) &&
		strings.IndexFunc(spec.Title, unicode.IsControl) < 0 && validLaunchInput(spec.launchInput("template"))
}

const catalogVersionFields = "v.template_id,v.version,v.title,v.account_id,v.region,v.image_id,v.size,v.network_id,v.security_group_id,v.published_by,v.published_at"

func scanCatalogVersion(row interface{ Scan(...any) error }, version *CatalogVersion, extra ...any) error {
	fields := []any{&version.ID, &version.Version, &version.Title, &version.Account, &version.Region, &version.ImageID,
		&version.Size, &version.NetworkID, &version.SecurityGroup, &version.PublishedBy, &version.PublishedAt}
	return row.Scan(append(fields, extra...)...)
}

func (s Store) ListCatalog(ctx context.Context, token string, scope identity.Scope, includeDisabled bool, after string, limit int) (CatalogPage, error) {
	page := CatalogPage{Items: make([]CatalogItem, 0)}
	if limit < 1 || limit > 100 || (after != "" && !catalogID.MatchString(after)) {
		return page, ErrInvalid
	}
	permission := identity.View
	if includeDisabled {
		permission = identity.Manage
	}
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) (resultErr error) {
		actor, err := operationActor(ctx, tx, token, scope, permission)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT "+catalogVersionFields+`,i.revision,i.enabled
			FROM cmp_instance_catalog_items AS i JOIN cmp_instance_catalog_versions AS v
			USING (tenant_id,project_id,template_id,version)
			WHERE (i.tenant_id,i.project_id)=($1,$2) AND i.template_id>$3 AND (i.enabled OR $4)
			ORDER BY i.template_id LIMIT $5`, actor.Tenant, actor.Project, after, includeDisabled, limit+1)
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
		for rows.Next() {
			var item CatalogItem
			if err := scanCatalogVersion(rows, &item.CatalogVersion, &item.Revision, &item.Enabled); err != nil {
				return err
			}
			if len(page.Items) == limit {
				page.Next = page.Items[limit-1].ID
				break
			}
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	if err != nil {
		return CatalogPage{}, err
	}
	return page, nil
}

func (s Store) GetCatalog(ctx context.Context, token string, scope identity.Scope, id string) (CatalogItem, error) {
	if !catalogID.MatchString(id) {
		return CatalogItem{}, ErrInvalid
	}
	var item CatalogItem
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.View)
		if err != nil {
			return err
		}
		err = scanCatalogVersion(tx.QueryRowContext(ctx, "SELECT "+catalogVersionFields+`,i.revision,i.enabled
			FROM cmp_instance_catalog_items AS i JOIN cmp_instance_catalog_versions AS v
			USING (tenant_id,project_id,template_id,version)
			WHERE (i.tenant_id,i.project_id,i.template_id)=($1,$2,$3)`, actor.Tenant, actor.Project, id),
			&item.CatalogVersion, &item.Revision, &item.Enabled)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrForbidden
		}
		if err == nil && !item.Enabled {
			_, err = identity.NewStore(tx).AuthorizeProject(ctx, actor, identity.Manage)
		}
		return err
	})
	if err != nil {
		return CatalogItem{}, err
	}
	return item, nil
}

func readCatalogVersion(ctx context.Context, tx *sql.Tx, scope identity.Scope, id string, version int64) (CatalogVersion, error) {
	var item CatalogVersion
	err := scanCatalogVersion(tx.QueryRowContext(ctx, "SELECT "+catalogVersionFields+`
		FROM cmp_instance_catalog_versions AS v WHERE (v.tenant_id,v.project_id,v.template_id,v.version)=($1,$2,$3,$4)`,
		scope.Tenant, scope.Project, id, version), &item)
	return item, err
}

func (s Store) GetCatalogVersion(ctx context.Context, token string, scope identity.Scope, id string, version int64) (CatalogVersion, error) {
	if !catalogID.MatchString(id) || version < 1 {
		return CatalogVersion{}, ErrInvalid
	}
	var item CatalogVersion
	err := dbutil.WithTransaction(ctx, s.db, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead}, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Manage)
		if err != nil {
			return err
		}
		item, err = readCatalogVersion(ctx, tx, actor, id, version)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrForbidden
		}
		return err
	})
	if err != nil {
		return CatalogVersion{}, err
	}
	return item, nil
}

func (s Store) CreateCatalog(ctx context.Context, token string, scope identity.Scope, input CatalogCreate) (CatalogItem, error) {
	return s.publishCatalog(ctx, token, scope, input.ID, 0, input.CatalogSpec)
}

func (s Store) PublishCatalog(ctx context.Context, token string, scope identity.Scope, id string, revision int64, spec CatalogSpec) (CatalogItem, error) {
	if revision < 1 {
		return CatalogItem{}, ErrInvalid
	}
	return s.publishCatalog(ctx, token, scope, id, revision, spec)
}

func (s Store) publishCatalog(ctx context.Context, token string, scope identity.Scope, id string, revision int64, spec CatalogSpec) (CatalogItem, error) {
	spec.Title = strings.TrimSpace(spec.Title)
	if !catalogID.MatchString(id) || !validCatalogSpec(spec) {
		return CatalogItem{}, ErrInvalid
	}
	item := CatalogItem{CatalogVersion: CatalogVersion{ID: id, CatalogSpec: spec}, Enabled: true}
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Manage)
		if err != nil {
			return err
		}
		if err := publishCatalogRevision(ctx, tx, actor, id, revision, &item); err != nil {
			return err
		}
		access, err := authorizeCreation(ctx, tx, actor, spec.Account)
		if err != nil {
			return err
		}
		if _, err := validateLaunchSpec(ctx, tx, actor, spec.Account, spec.Region, access.Provider, spec.launchInput("template").InstanceSpec); err != nil {
			return err
		}
		item.PublishedBy = actor.Principal
		err = tx.QueryRowContext(ctx, `INSERT INTO cmp_instance_catalog_versions
			(tenant_id,project_id,template_id,version,title,account_id,region,image_id,size,network_id,security_group_id,published_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING published_at`,
			actor.Tenant, actor.Project, id, item.Version, spec.Title, spec.Account, spec.Region, spec.ImageID, spec.Size,
			spec.NetworkID, spec.SecurityGroup, actor.Principal).Scan(&item.PublishedAt)
		if err != nil {
			return err
		}
		return recordCatalogChange(ctx, tx, actor, item, "publish")
	})
	if err != nil {
		return CatalogItem{}, err
	}
	return item, nil
}

func publishCatalogRevision(ctx context.Context, tx *sql.Tx, actor identity.Scope, id string, revision int64, item *CatalogItem) error {
	if revision == 0 {
		err := tx.QueryRowContext(ctx, `INSERT INTO cmp_instance_catalog_items
			(tenant_id,project_id,template_id,version,revision,enabled) VALUES ($1,$2,$3,1,1,true)
			ON CONFLICT DO NOTHING RETURNING version,revision`, actor.Tenant, actor.Project, id).
			Scan(&item.Version, &item.Revision)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		return err
	}
	return changeCatalogRevision(ctx, tx, actor, id, revision, true, item)
}

func changeCatalogRevision(ctx context.Context, tx *sql.Tx, actor identity.Scope, id string, revision int64, publish bool, item *CatalogItem) error {
	err := tx.QueryRowContext(ctx, `UPDATE cmp_instance_catalog_items
		SET revision=revision+1,version=version+$5::boolean::int,enabled=$5
		WHERE (tenant_id,project_id,template_id,revision)=($1,$2,$3,$4) AND (enabled OR $5)
		RETURNING version,revision,enabled`, actor.Tenant, actor.Project, id, revision, publish).
		Scan(&item.Version, &item.Revision, &item.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCatalogStale
	}
	return err
}

func recordCatalogChange(ctx context.Context, tx *sql.Tx, actor identity.Scope, item CatalogItem, action string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO cmp_instance_catalog_events
		(tenant_id,project_id,template_id,version,revision,actor_id,action) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		actor.Tenant, actor.Project, item.ID, item.Version, item.Revision, actor.Principal, action)
	return err
}

func (s Store) RetireCatalog(ctx context.Context, token string, scope identity.Scope, id string, revision int64) (CatalogItem, error) {
	if !catalogID.MatchString(id) || revision < 1 {
		return CatalogItem{}, ErrInvalid
	}
	var item CatalogItem
	err := dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Manage)
		if err != nil {
			return err
		}
		if err := changeCatalogRevision(ctx, tx, actor, id, revision, false, &item); err != nil {
			return err
		}
		item.CatalogVersion, err = readCatalogVersion(ctx, tx, actor, id, item.Version)
		if err != nil {
			return err
		}
		return recordCatalogChange(ctx, tx, actor, item, "retire")
	})
	if err != nil {
		return CatalogItem{}, err
	}
	return item, nil
}

func (s Store) SubmitCatalogCreation(ctx context.Context, token string, scope identity.Scope, key, id string, input CatalogLaunch) (Operation, error) {
	input.Name = strings.TrimSpace(input.Name)
	if !catalogID.MatchString(id) || !operationKey.MatchString(key) || input.Version < 1 || !instanceName.MatchString(input.Name) {
		return Operation{}, ErrInvalid
	}
	payload, err := json.Marshal(struct {
		ID string
		CatalogLaunch
	}{id, input})
	if err != nil {
		return Operation{}, err
	}
	digest := sha256.Sum256(append([]byte("catalog-launch\x00"), payload...))
	var op Operation
	err = dbutil.WithTransaction(ctx, s.db, nil, func(tx *sql.Tx) error {
		actor, err := operationActor(ctx, tx, token, scope, identity.Execute)
		if err != nil {
			return err
		}
		ref := catalogReference{id: id, version: input.Version}
		err = tx.QueryRowContext(ctx, `SELECT enabled AND version=$4 FROM cmp_instance_catalog_items
			WHERE (tenant_id,project_id,template_id)=($1,$2,$3) FOR SHARE`,
			actor.Tenant, actor.Project, id, input.Version).Scan(&ref.eligible)
		if errors.Is(err, sql.ErrNoRows) {
			return identity.ErrForbidden
		}
		if err != nil {
			return err
		}
		version, err := readCatalogVersion(ctx, tx, actor, id, input.Version)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrCatalogStale
		}
		if err != nil {
			return err
		}
		return s.submitCreation(ctx, tx, actor, key, digest, version.launchInput(input.Name), ref, &op)
	})
	if err != nil {
		return Operation{}, err
	}
	return op, nil
}
