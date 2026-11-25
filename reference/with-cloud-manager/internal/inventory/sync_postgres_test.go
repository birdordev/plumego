package inventory

import (
	"context"
	"errors"
	"testing"
	"time"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
	"with-cloud-manager/internal/provider"
)

type fakeDiscovery func(context.Context, provider.Scope, string) (provider.Page, error)

func (f fakeDiscovery) Discover(ctx context.Context, scope provider.Scope, cursor string) (provider.Page, error) {
	return f(ctx, scope, cursor)
}

type fakeObserver func(context.Context, provider.Scope, string) (provider.Object, error)

func (f fakeObserver) Observe(ctx context.Context, scope provider.Scope, id string) (provider.Object, error) {
	return f(ctx, scope, id)
}

func TestAcceptancePostgresInventory(t *testing.T) {
	db := dbtest.Open(t)
	ctx := t.Context()
	for _, migrate := range []func() error{
		func() error { return identity.Migrate(ctx, db) },
		func() error { return Migrate(ctx, db) },
	} {
		if err := migrate(); err != nil {
			t.Fatal(err)
		}
	}
	run := func(query string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	run(`INSERT INTO cmp_tenants VALUES ('local');
		INSERT INTO cmp_projects VALUES ('local','project');
		INSERT INTO cmp_cloud_accounts (tenant_id,account_id,project_id,provider,remote_identity,credential_ref,mode)
			VALUES ('local','account','project','aws-ec2','aws:123','test/account','read_only');`)
	store := NewStore(db)
	key := Key{"local", "account", "test-region", provider.Instance}
	claim := func() Claim {
		t.Helper()
		claim, err := store.Claim(ctx, key, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return claim
	}
	page := func(next string, ids ...string) fakeDiscovery {
		return func(_ context.Context, scope provider.Scope, _ string) (provider.Page, error) {
			if scope.Provider != "aws-ec2" || scope.RemoteIdentity != "aws:123" || scope.Kind != key.Kind || scope.Region != key.Region {
				t.Fatalf("unexpected provider scope: %+v", scope)
			}
			result := provider.Page{Next: next}
			for _, id := range ids {
				result.Objects = append(result.Objects, provider.Object{ID: id, Name: id})
			}
			return result, nil
		}
	}
	step := func(current Claim, adapter fakeDiscovery, wantDone bool) Claim {
		t.Helper()
		next, done, err := store.ScanPage(ctx, current, adapter)
		if err != nil || done != wantDone {
			t.Fatalf("page done=%v: %v", done, err)
		}
		return next
	}
	checkCounts := func(wantAll, wantMissing, wantRemoved int) {
		t.Helper()
		var all, missing, removed int
		err := db.QueryRowContext(ctx, `SELECT count(*),
			count(*) FILTER (WHERE missing_generation IS NOT NULL AND removed_at IS NULL),
			count(*) FILTER (WHERE removed_at IS NOT NULL) FROM cmp_inventory_objects`).
			Scan(&all, &missing, &removed)
		if err != nil || all != wantAll || missing != wantMissing || removed != wantRemoved {
			t.Fatalf("assets all/missing/removed %d/%d/%d, want %d/%d/%d, err %v", all, missing, removed, wantAll, wantMissing, wantRemoved, err)
		}
	}
	fail := func(kind provider.FailureKind) fakeDiscovery {
		return func(context.Context, provider.Scope, string) (provider.Page, error) {
			return provider.Page{}, &provider.Failure{Kind: kind}
		}
	}
	step(claim(), fakeDiscovery(func(context.Context, provider.Scope, string) (provider.Page, error) {
		asset := provider.Object{ID: "stable", Name: "stable"}
		asset.Link(provider.Volume, "disk", "uses_volume")
		return provider.Page{Objects: []provider.Object{asset, {ID: "to-retire", Name: "to-retire"}}}, nil
	}), true)
	var link string
	if err := db.QueryRowContext(ctx, "SELECT links->0->>'id' FROM cmp_inventory_objects WHERE external_id='stable'").Scan(&link); err != nil || link != "disk" {
		t.Fatalf("inventory topology not persisted: %q %v", link, err)
	}
	type claimResult struct {
		claim Claim
		err   error
	}
	start := make(chan struct{})
	results := make(chan claimResult, 2)
	for range 2 {
		go func() {
			<-start
			fresh, err := store.Claim(ctx, key, time.Minute)
			results <- claimResult{fresh, err}
		}()
	}
	close(start)
	a, b := <-results, <-results
	if a.err != nil {
		a, b = b, a
	}
	if a.err != nil || !errors.Is(b.err, ErrUnavailable) {
		t.Fatalf("concurrent claim errors: %v, %v", a.err, b.err)
	}
	first := step(a.claim, page("second", "stable", "stable"), false)
	if _, err := store.Claim(ctx, key, time.Minute); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("live lease accepted twice: %v", err)
	}
	for _, kind := range []provider.FailureKind{provider.Unauthorized, provider.Throttled} {
		if _, _, err := store.ScanPage(ctx, first, fail(kind)); !provider.IsFailure(err, kind) {
			t.Fatalf("expected %s, got %v", kind, err)
		}
		checkCounts(2, 0, 0)
	}
	run("UPDATE cmp_inventory_scopes SET lease_until=clock_timestamp()-interval '1 second'")
	resumed := claim()
	if resumed.cursor != "second" || resumed.generation != first.generation || resumed.fence <= first.fence {
		t.Fatalf("unexpected resumed checkpoint: %+v", resumed)
	}
	if _, _, err := store.ScanPage(ctx, first, page("", "stale")); !errors.Is(err, ErrStale) {
		t.Fatalf("old lease: %v", err)
	}
	run(`ALTER TABLE cmp_inventory_scopes ADD CONSTRAINT test_fail_checkpoint CHECK (cursor <> 'blocked')`)
	if _, _, err := store.ScanPage(ctx, resumed, page("blocked", "rollback-only")); err == nil {
		t.Fatal("checkpoint failure was not injected")
	}
	var phantom int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM cmp_inventory_history WHERE external_id='rollback-only'").Scan(&phantom); err != nil || phantom != 0 {
		t.Fatalf("rolled-back history count=%d error=%v", phantom, err)
	}
	checkCounts(2, 0, 0)
	resumed = step(resumed, page("third", "fresh"), false)
	if _, _, err := store.ScanPage(ctx, resumed, page("second", "cycle")); !provider.IsFailure(err, provider.InvalidResponse) {
		t.Fatalf("expected cycle rejection, got %v", err)
	}
	checkCounts(3, 0, 0)
	step(resumed, page(""), true)
	checkCounts(3, 1, 0)
	observer := func(kind provider.FailureKind) fakeObserver {
		return func(context.Context, provider.Scope, string) (provider.Object, error) {
			return provider.Object{}, &provider.Failure{Kind: kind}
		}
	}
	if removed, err := store.ConfirmMissing(ctx, key, "to-retire", observer(provider.Unauthorized)); removed || !provider.IsFailure(err, provider.Unauthorized) {
		t.Fatalf("authorization loss removed=%v err=%v", removed, err)
	}
	checkCounts(3, 1, 0)
	if removed, err := store.ConfirmMissing(ctx, key, "to-retire", observer(provider.Absent)); !removed || err != nil {
		t.Fatalf("authoritative absence removed=%v err=%v", removed, err)
	}
	checkCounts(3, 0, 1)
	rows, err := db.QueryContext(ctx, `SELECT action FROM cmp_inventory_history
		WHERE external_id='to-retire' ORDER BY occurred_at,event_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for _, expected := range []string{"discovered", "missing_candidate", "removed"} {
		var got string
		if !rows.Next() {
			t.Fatalf("missing history %s: %v", expected, rows.Err())
		}
		if err := rows.Scan(&got); err != nil || got != expected {
			t.Fatalf("history action=%q, want=%q, error=%v", got, expected, err)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("unexpected extra history: %v", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	running := step(claim(), page("expiring", "fresh"), false)
	if _, _, err := store.ScanPage(ctx, running, fail(provider.ExpiredCursor)); !provider.IsFailure(err, provider.ExpiredCursor) {
		t.Fatalf("expired cursor: %v", err)
	}
	checkCounts(3, 0, 1)
	restarted := claim()
	if restarted.cursor != "" || restarted.generation <= running.generation {
		t.Fatalf("fresh start after expiration: %+v", restarted)
	}
	running = step(restarted, page("pending", "fresh"), false)
	_, _, err = store.ScanPage(ctx, running, fakeDiscovery(func(context.Context, provider.Scope, string) (provider.Page, error) {
		run("UPDATE cmp_cloud_accounts SET credential_ref='test/rotated'")
		return provider.Page{Objects: []provider.Object{{ID: "late"}}}, nil
	}))
	if !errors.Is(err, ErrStale) {
		t.Fatalf("late response from replaced credential: %v", err)
	}
	checkCounts(3, 0, 1)
	if _, err := store.ClaimWithCredentialRevision(ctx, key, time.Minute, 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("pre-rotation profile claimed the updated credential: %v", err)
	}
	rotated := claim()
	if rotated.cursor != "" || rotated.generation <= running.generation {
		t.Fatalf("fresh start after rotation: %+v", rotated)
	}
	run(`UPDATE cmp_cloud_accounts SET mode='suspended';
		UPDATE cmp_cloud_accounts SET mode='read_only';`)
	if _, _, err := store.ScanPage(ctx, rotated, page("", "old")); !errors.Is(err, ErrStale) {
		t.Fatalf("pre-suspension claim reused after reactivation: %v", err)
	}
	checkCounts(3, 0, 1)
}
