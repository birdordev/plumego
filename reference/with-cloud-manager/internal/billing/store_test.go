package billing

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"

	"with-cloud-manager/internal/dbtest"
	"with-cloud-manager/internal/identity"
)

func billingDB(t *testing.T) *sql.DB {
	t.Helper()
	db := dbtest.Open(t)
	if err := identity.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO cmp_tenants VALUES ('tenant');
		INSERT INTO cmp_projects VALUES ('tenant','finance'),('tenant','other');
		INSERT INTO cmp_cloud_accounts(tenant_id,account_id,project_id,provider,remote_identity,credential_ref,mode)
		VALUES ('tenant','cloud','finance','alibaba-ecs','10002','fixture','read_only')`); err != nil {
		t.Fatal(err)
	}
	return db
}

func testLedger() (Key, Manifest) {
	return Key{Tenant: "tenant", Project: "finance", Account: "cloud", Period: "2026-11", Source: Native},
		Manifest{Provider: "alibaba-ecs", Columns: &Columns{
			Account: "Account", Period: "Month", Currency: "Currency", Billed: "Billed", Effective: "Effective",
		}, ExpectedBilled: map[string]string{"CNY": "0.3", "USD": "0"}}
}

func partCSV(rows ...string) []byte {
	return []byte("Account,Month,Currency,Billed,Effective\n" + strings.Join(rows, "\n") + "\n")
}

func TestBillingLedgerReplayAndAtomicImport(t *testing.T) {
	db := billingDB(t)
	store := NewStore(db)
	key, profile := testLedger()
	first := partCSV("10002,2026-11,CNY,0.100000000001,0.07", "10002,2026-11,USD,-0.01,")
	second := partCSV("10002,2026-11,CNY,0.200000000001,0.1")
	ctx := t.Context()
	report, err := store.Import(ctx, key, profile, 0, [][]byte{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if report.Revision != 1 || report.Rows != 3 || !report.Current || len(report.Parts) != 2 || len(report.Totals) != 2 {
		t.Fatalf("unexpected report %+v", report)
	}
	cny, usd := report.Totals[0], report.Totals[1]
	if cny.Currency != "CNY" || cny.Billed != "0.300000000002" || cny.Effective == nil || *cny.Effective != "0.170000000000" ||
		cny.Variance == nil || *cny.Variance != "0.000000000002" || usd.Effective != nil || usd.Billed != "-0.010000000000" {
		t.Fatalf("totals %+v %+v", cny, usd)
	}
	retry, err := store.Import(ctx, key, profile, 0, [][]byte{second, first})
	if err != nil || retry.Revision != 1 || !retry.Replayed || retry.Digest != report.Digest {
		t.Fatalf("retry %+v error %v", retry, err)
	}
	if _, err := store.Import(ctx, key, profile, 1, [][]byte{first, first}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate parts: %v", err)
	}
	for _, part := range report.Parts {
		data, err := store.Source(ctx, key, 1, part.Number)
		if err != nil || (!bytes.Equal(data, first) && !bytes.Equal(data, second)) || digestOf(data) != part.Digest {
			t.Fatalf("original part %d unavailable: %v", part.Number, err)
		}
	}
	if verified, err := store.Verify(ctx, key, 1); err != nil || verified.Digest != report.Digest {
		t.Fatalf("verification %+v error %v", verified, err)
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE cmp_billing_lines ADD CHECK (billed<>999)"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Import(ctx, key, profile, 1, [][]byte{partCSV("10002,2026-11,CNY,999,")}); err == nil {
		t.Fatal("projection failure published a report")
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM cmp_billing_reports").Scan(&count); err != nil || count != 1 {
		t.Fatalf("rolled-back report count %d: %v", count, err)
	}
	newPart := partCSV("10002,2026-11,CNY,0.300000000000,0.18")
	next, err := store.Import(ctx, key, profile, 1, [][]byte{newPart})
	if err != nil || next.Revision != 2 || next.Totals[0].Variance == nil || *next.Totals[0].Variance != "0.000000000000" {
		t.Fatalf("replacement %+v: %v", next, err)
	}
	retry, err = store.Import(ctx, key, profile, 2, [][]byte{first, second})
	if err != nil || !retry.Replayed || retry.Revision != 1 || retry.Current {
		t.Fatalf("historic retry republished the old report: %+v %v", retry, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE cmp_billing_lines SET billed=2
		WHERE report_id=(SELECT id FROM cmp_billing_reports WHERE revision=1) AND billed>0`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Verify(ctx, key, 1); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("projection corruption accepted: %v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE cmp_billing_totals SET billed=9 WHERE currency='CNY'"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Verify(ctx, key, 2); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("totals corruption accepted: %v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE cmp_billing_parts SET raw=convert_to('bad','UTF8')"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Source(ctx, key, 2, 1); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("source corruption accepted: %v", err)
	}
}

func TestBillingLedgerConcurrentRevision(t *testing.T) {
	store := NewStore(billingDB(t))
	key, profile := testLedger()
	parts := [][]byte{partCSV("10002,2026-11,CNY,1,"), partCSV("10002,2026-11,CNY,2,")}
	start := make(chan struct{})
	results := make(chan error, len(parts))
	var workers sync.WaitGroup
	for _, part := range parts {
		workers.Go(func() {
			<-start
			_, err := store.Import(t.Context(), key, profile, 0, [][]byte{part})
			results <- err
		})
	}
	close(start)
	workers.Wait()
	close(results)
	published, stale := 0, 0
	for err := range results {
		switch {
		case err == nil:
			published++
		case errors.Is(err, ErrConflict):
			stale++
		default:
			t.Fatal(err)
		}
	}
	if published != 1 || stale != 1 {
		t.Fatalf("published %d stale %d", published, stale)
	}
	key.Project = "other"
	if _, err := store.Verify(t.Context(), key, 1); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("non-owner archive access: %v", err)
	}
}
