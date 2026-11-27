package kvm

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"time"

	"with-cloud-manager/internal/identity"
)

type InventoryQuery struct {
	Site, After string
	Limit       int
}

func (q InventoryQuery) cursor(idPattern *regexp.Regexp) (site, id string, err error) {
	if (q.Site != "" && !identifier.MatchString(q.Site)) || q.Limit < 1 || q.Limit > 100 {
		return "", "", ErrInvalid
	}
	if q.After == "" {
		return "", "", nil
	}
	site, id, _ = strings.Cut(q.After, "/")
	if !identifier.MatchString(site) || !idPattern.MatchString(id) || (q.Site != "" && site != q.Site) {
		return "", "", ErrInvalid
	}
	return site, id, nil
}

type InventoryPage[T any] struct {
	Items []T    `json:"items"`
	Next  string `json:"next,omitempty"`
}

func parseTimedInventoryCursor(after string) (*time.Time, string, error) {
	if after == "" {
		return nil, "", nil
	}
	stamp, id, _ := strings.Cut(after, "|")
	parsed, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || !identifier.MatchString(id) {
		return nil, "", ErrInvalid
	}
	return &parsed, id, nil
}

type HostSummary struct {
	Site          string     `json:"site"`
	ID            string     `json:"id"`
	FailureDomain string     `json:"failure_domain"`
	Draining      bool       `json:"draining"`
	FenceState    string     `json:"fence_state"`
	ReportedAt    *time.Time `json:"reported_at,omitempty"`
}

type VMSummary struct {
	Site    string `json:"site"`
	ID      string `json:"id"`
	Host    string `json:"host"`
	State   string `json:"state"`
	Address string `json:"ipv4,omitempty"`
}

func (s Store) ListHosts(ctx context.Context, token string, scope identity.Scope, q InventoryQuery) (InventoryPage[HostSummary], error) {
	site, id, err := q.cursor(identifier)
	if err != nil {
		return InventoryPage[HostSummary]{}, err
	}
	page := InventoryPage[HostSummary]{Items: make([]HostSummary, 0, q.Limit+1)}
	err = s.authorizedProject(ctx, token, scope, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		rows, err := tx.QueryContext(ctx, `SELECT site_id,host_id,failure_domain,draining,fence_state,reported_at
			FROM cmp_kvm_hosts WHERE (tenant_id,project_id)=($1,$2) AND ($3='' OR site_id=$3)
			AND (site_id,host_id)>($4,$5) ORDER BY site_id,host_id LIMIT $6`,
			actor.Tenant, actor.Project, q.Site, site, id, q.Limit+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var host HostSummary
			if err := rows.Scan(&host.Site, &host.ID, &host.FailureDomain, &host.Draining, &host.FenceState, &host.ReportedAt); err != nil {
				return err
			}
			page.Items = append(page.Items, host)
		}
		return rows.Err()
	})
	if err != nil {
		return InventoryPage[HostSummary]{}, err
	}
	if len(page.Items) > q.Limit {
		page.Items = page.Items[:q.Limit]
		last := page.Items[q.Limit-1]
		page.Next = last.Site + "/" + last.ID
	}
	return page, nil
}

func (s Store) ListVMs(ctx context.Context, token string, scope identity.Scope, q InventoryQuery) (InventoryPage[VMSummary], error) {
	site, id, err := q.cursor(hostUUID)
	if err != nil {
		return InventoryPage[VMSummary]{}, err
	}
	if id == "" {
		id = "00000000-0000-0000-0000-000000000000"
	}
	page := InventoryPage[VMSummary]{Items: make([]VMSummary, 0, q.Limit+1)}
	err = s.authorizedProject(ctx, token, scope, identity.View, true, func(tx *sql.Tx, actor identity.Scope, _ identity.Membership) error {
		rows, err := tx.QueryContext(ctx, `SELECT p.site_id,v.vm_id::text,p.host_id,v.state,COALESCE(host(p.ipv4),'')
			FROM cmp_kvm_placements AS p JOIN cmp_kvm_vms AS v USING (placement_id)
			WHERE (p.tenant_id,p.project_id)=($1,$2) AND ($3='' OR p.site_id=$3)
			AND p.site_id>=$4 AND (p.site_id,v.vm_id)>($4,$5::uuid)
			ORDER BY p.site_id,v.vm_id LIMIT $6`, actor.Tenant, actor.Project, q.Site, site, id, q.Limit+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var vm VMSummary
			if err := rows.Scan(&vm.Site, &vm.ID, &vm.Host, &vm.State, &vm.Address); err != nil {
				return err
			}
			page.Items = append(page.Items, vm)
		}
		return rows.Err()
	})
	if err != nil {
		return InventoryPage[VMSummary]{}, err
	}
	if len(page.Items) > q.Limit {
		page.Items = page.Items[:q.Limit]
		last := page.Items[q.Limit-1]
		page.Next = last.Site + "/" + last.ID
	}
	return page, nil
}
