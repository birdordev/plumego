package rw

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
	"time"
)

// Test stubs for database connections
type stubConnector struct {
	conn *stubConn
}

func (c *stubConnector) Connect(ctx context.Context) (driver.Conn, error) {
	return c.conn, nil
}

func (c *stubConnector) Driver() driver.Driver {
	return stubDriver{}
}

type stubDriver struct{}

func (d stubDriver) Open(name string) (driver.Conn, error) {
	return nil, errors.New("not supported")
}

type stubConn struct {
	pingErr error
}

func (c *stubConn) Prepare(query string) (driver.Stmt, error) {
	return stubStmt{}, nil
}

func (c *stubConn) Close() error {
	return nil
}

func (c *stubConn) Begin() (driver.Tx, error) {
	return stubTx{}, nil
}

func (c *stubConn) Ping(ctx context.Context) error {
	return c.pingErr
}

type stubStmt struct{}

func (s stubStmt) Close() error {
	return nil
}

func (s stubStmt) NumInput() int {
	return -1
}

func (s stubStmt) Exec(args []driver.Value) (driver.Result, error) {
	return stubResult{}, nil
}

func (s stubStmt) Query(args []driver.Value) (driver.Rows, error) {
	return stubRows{}, nil
}

type stubTx struct{}

func (t stubTx) Commit() error {
	return nil
}

func (t stubTx) Rollback() error {
	return nil
}

type stubResult struct{}

func (r stubResult) LastInsertId() (int64, error) {
	return 0, nil
}

func (r stubResult) RowsAffected() (int64, error) {
	return 0, nil
}

type stubRows struct{}

func (r stubRows) Columns() []string {
	return nil
}

func (r stubRows) Close() error {
	return nil
}

func (r stubRows) Next(dest []driver.Value) error {
	return io.EOF
}

func newStubDB() *sql.DB {
	return sql.OpenDB(&stubConnector{conn: &stubConn{}})
}

func TestNewCluster(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	replica1 := newStubDB()
	defer replica1.Close()

	replica2 := newStubDB()
	defer replica2.Close()

	config := Config{
		Primary:  primary,
		Replicas: []*sql.DB{replica1, replica2},
		HealthCheck: HealthCheckConfig{
			Enabled: false, // Disable for unit test
		},
	}

	cluster, err := New(config)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cluster.Close()

	if cluster.Primary() != primary {
		t.Error("primary mismatch")
	}

	if len(cluster.Replicas()) != 2 {
		t.Errorf("got %d replicas, want 2", len(cluster.Replicas()))
	}
}

func TestClusterCopiesReplicaSlices(t *testing.T) {
	primary := newStubDB()
	replica1 := newStubDB()
	replica2 := newStubDB()
	replicas := []*sql.DB{replica1, replica2}

	cluster, err := New(Config{
		Primary:  primary,
		Replicas: replicas,
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer cluster.Close()

	replicas[0] = nil
	if got := cluster.Replicas()[0]; got != replica1 {
		t.Fatalf("mutating input replicas changed cluster replica: got %v, want %v", got, replica1)
	}

	returned := cluster.Replicas()
	returned[1] = nil
	if got := cluster.Replicas()[1]; got != replica2 {
		t.Fatalf("mutating returned replicas changed cluster replica: got %v, want %v", got, replica2)
	}
}

func TestNewClusterNoPrimary(t *testing.T) {
	_, err := New(Config{
		Primary:  nil,
		Replicas: []*sql.DB{newStubDB()},
	})

	if err != ErrNoPrimary {
		t.Errorf("got error %v, want %v", err, ErrNoPrimary)
	}
}

func TestNewClusterInvalidReplicaWeights(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()
	replica := newStubDB()
	defer replica.Close()

	_, err := New(Config{
		Primary:        primary,
		Replicas:       []*sql.DB{replica},
		ReplicaWeights: []int{0},
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	})
	if !errors.Is(err, ErrInvalidReplicaWeight) {
		t.Fatalf("New() error = %v, want ErrInvalidReplicaWeight", err)
	}
}

func TestClusterCloseIdempotent(t *testing.T) {
	primary := newStubDB()
	replica := newStubDB()

	cluster, err := New(Config{
		Primary:  primary,
		Replicas: []*sql.DB{replica},
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := cluster.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := cluster.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestClusterHealthCheckUsesConfiguredContext(t *testing.T) {
	primary := newStubDB()
	replica := newStubDB()

	healthCtx, cancel := context.WithCancel(context.Background())
	cluster, err := New(Config{
		Primary:            primary,
		Replicas:           []*sql.DB{replica},
		HealthCheckContext: healthCtx,
		HealthCheck: HealthCheckConfig{
			Enabled:           true,
			Interval:          2 * time.Millisecond,
			Timeout:           time.Millisecond,
			FailureThreshold:  1,
			RecoveryThreshold: 1,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer cluster.Close()

	deadline := time.Now().Add(100 * time.Millisecond)
	for cluster.Metrics().HealthCheckCount.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if cluster.Metrics().HealthCheckCount.Load() == 0 {
		t.Fatal("expected health checker to run before context cancellation")
	}

	cancel()
	before := cluster.Metrics().HealthCheckCount.Load()
	time.Sleep(20 * time.Millisecond)
	after := cluster.Metrics().HealthCheckCount.Load()
	if after > before+1 {
		t.Fatalf("health checks continued after context cancellation: before=%d after=%d", before, after)
	}
}

func TestClusterExecContext(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	config := Config{
		Primary: primary,
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	}

	cluster, err := New(config)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cluster.Close()

	ctx := t.Context()
	_, err = cluster.ExecContext(ctx, "INSERT INTO test VALUES (1)")
	if err != nil {
		t.Errorf("ExecContext failed: %v", err)
	}

	metrics := cluster.Metrics()
	if metrics.TotalQueries.Load() != 1 {
		t.Errorf("got %d total queries, want 1", metrics.TotalQueries.Load())
	}
	if metrics.PrimaryQueries.Load() != 1 {
		t.Errorf("got %d primary queries, want 1", metrics.PrimaryQueries.Load())
	}
}

func TestClusterQueryContextPrimary(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	replica := newStubDB()
	defer replica.Close()

	config := Config{
		Primary:  primary,
		Replicas: []*sql.DB{replica},
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	}

	cluster, err := New(config)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cluster.Close()

	// Write query should use primary
	ctx := t.Context()
	rows, err := cluster.QueryContext(ctx, "SELECT * FROM test FOR UPDATE")
	if err != nil {
		t.Errorf("QueryContext failed: %v", err)
	}
	if rows != nil {
		rows.Close()
	}

	metrics := cluster.Metrics()
	if metrics.PrimaryQueries.Load() != 1 {
		t.Errorf("got %d primary queries, want 1", metrics.PrimaryQueries.Load())
	}
	if metrics.ReplicaQueries.Load() != 0 {
		t.Errorf("got %d replica queries, want 0", metrics.ReplicaQueries.Load())
	}
}

func TestClusterQueryContextReplica(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	replica := newStubDB()
	defer replica.Close()

	config := Config{
		Primary:  primary,
		Replicas: []*sql.DB{replica},
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	}

	cluster, err := New(config)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cluster.Close()

	// Read query should use replica
	ctx := t.Context()
	rows, err := cluster.QueryContext(ctx, "SELECT * FROM test")
	if err != nil {
		t.Errorf("QueryContext failed: %v", err)
	}
	if rows != nil {
		rows.Close()
	}

	metrics := cluster.Metrics()
	if metrics.ReplicaQueries.Load() != 1 {
		t.Errorf("got %d replica queries, want 1", metrics.ReplicaQueries.Load())
	}
	if metrics.PrimaryQueries.Load() != 0 {
		t.Errorf("got %d primary queries, want 0", metrics.PrimaryQueries.Load())
	}
}

func TestClusterWithForcePrimary(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	replica := newStubDB()
	defer replica.Close()

	config := Config{
		Primary:  primary,
		Replicas: []*sql.DB{replica},
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	}

	cluster, err := New(config)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cluster.Close()

	// Force primary even for read
	ctx := WithForcePrimary(t.Context())
	rows, err := cluster.QueryContext(ctx, "SELECT * FROM test")
	if err != nil {
		t.Errorf("QueryContext failed: %v", err)
	}
	if rows != nil {
		rows.Close()
	}

	metrics := cluster.Metrics()
	if metrics.PrimaryQueries.Load() != 1 {
		t.Errorf("got %d primary queries, want 1", metrics.PrimaryQueries.Load())
	}
	if metrics.ReplicaQueries.Load() != 0 {
		t.Errorf("got %d replica queries, want 0", metrics.ReplicaQueries.Load())
	}
}

func TestClusterFallbackToPrimary(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	// Create replica that will fail health checks
	failingReplica := sql.OpenDB(&stubConnector{
		conn: &stubConn{pingErr: errors.New("connection failed")},
	})
	defer failingReplica.Close()

	config := Config{
		Primary:           primary,
		Replicas:          []*sql.DB{failingReplica},
		FallbackToPrimary: true,
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	}

	cluster, err := New(config)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cluster.Close()

	// Mark replica as unhealthy
	cluster.markReplicaHealth(0, false)

	// Read query should fallback to primary
	ctx := t.Context()
	rows, err := cluster.QueryContext(ctx, "SELECT * FROM test")
	if err != nil {
		t.Errorf("QueryContext failed: %v", err)
	}
	if rows != nil {
		rows.Close()
	}

	metrics := cluster.Metrics()
	if metrics.FallbackCount.Load() != 1 {
		t.Errorf("got %d fallback count, want 1", metrics.FallbackCount.Load())
	}
}

func TestClusterBeginTx(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	config := Config{
		Primary: primary,
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	}

	cluster, err := New(config)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cluster.Close()

	ctx := t.Context()
	tx, err := cluster.BeginTx(ctx, nil)
	if err != nil {
		t.Errorf("BeginTx failed: %v", err)
	}
	if tx != nil {
		_ = tx.Rollback()
	}

	metrics := cluster.Metrics()
	if metrics.PrimaryQueries.Load() != 1 {
		t.Errorf("got %d primary queries, want 1", metrics.PrimaryQueries.Load())
	}
}

func TestClusterPingContext(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	replica := newStubDB()
	defer replica.Close()

	config := Config{
		Primary:  primary,
		Replicas: []*sql.DB{replica},
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	}

	cluster, err := New(config)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cluster.Close()

	ctx := t.Context()
	err = cluster.PingContext(ctx)
	if err != nil {
		t.Errorf("PingContext failed: %v", err)
	}
}

func TestClusterStats(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	replica := newStubDB()
	defer replica.Close()

	config := Config{
		Primary:  primary,
		Replicas: []*sql.DB{replica},
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	}

	cluster, err := New(config)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cluster.Close()

	stats := cluster.Stats()
	if stats.Replicas == nil || len(stats.Replicas) != 1 {
		t.Errorf("expected 1 replica stats, got %d", len(stats.Replicas))
	}
}

func TestClusterReplicaHealth(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	replica1 := newStubDB()
	defer replica1.Close()

	replica2 := newStubDB()
	defer replica2.Close()

	config := Config{
		Primary:  primary,
		Replicas: []*sql.DB{replica1, replica2},
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	}

	cluster, err := New(config)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cluster.Close()

	// Initially all healthy
	health := cluster.ReplicaHealth()
	if len(health) != 2 {
		t.Fatalf("expected 2 health statuses, got %d", len(health))
	}
	if !health[0] || !health[1] {
		t.Error("expected all replicas to be healthy initially")
	}

	// Mark one as unhealthy
	cluster.markReplicaHealth(0, false)

	health = cluster.ReplicaHealth()
	if health[0] {
		t.Error("expected replica 0 to be unhealthy")
	}
	if !health[1] {
		t.Error("expected replica 1 to be healthy")
	}
}

func TestDefaultHealthCheckConfig(t *testing.T) {
	config := DefaultHealthCheckConfig()

	if !config.Enabled {
		t.Error("expected health check to be enabled by default")
	}
	if config.Interval != 30*time.Second {
		t.Errorf("got interval %v, want 30s", config.Interval)
	}
	if config.Timeout != 5*time.Second {
		t.Errorf("got timeout %v, want 5s", config.Timeout)
	}
	if config.FailureThreshold != 3 {
		t.Errorf("got failure threshold %d, want 3", config.FailureThreshold)
	}
	if config.RecoveryThreshold != 2 {
		t.Errorf("got recovery threshold %d, want 2", config.RecoveryThreshold)
	}
}
func TestClusterQueryRowContext(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	replica := newStubDB()
	defer replica.Close()

	config := Config{
		Primary:  primary,
		Replicas: []*sql.DB{replica},
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	}

	cluster, err := New(config)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cluster.Close()

	// Read query should use replica
	ctx := t.Context()
	row := cluster.QueryRowContext(ctx, "SELECT * FROM test WHERE id = ?", 1)
	if row == nil {
		t.Error("expected row, got nil")
	}

	metrics := cluster.Metrics()
	if metrics.ReplicaQueries.Load() != 1 {
		t.Errorf("got %d replica queries, want 1", metrics.ReplicaQueries.Load())
	}
}

func TestClusterQueryRowContextPrimary(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	replica := newStubDB()
	defer replica.Close()

	config := Config{
		Primary:  primary,
		Replicas: []*sql.DB{replica},
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	}

	cluster, err := New(config)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cluster.Close()

	// Write query should use primary
	ctx := WithForcePrimary(t.Context())
	row := cluster.QueryRowContext(ctx, "SELECT * FROM test WHERE id = ?", 1)
	if row == nil {
		t.Error("expected row, got nil")
	}

	metrics := cluster.Metrics()
	if metrics.PrimaryQueries.Load() != 1 {
		t.Errorf("got %d primary queries, want 1", metrics.PrimaryQueries.Load())
	}
}

func TestClusterQueryRowContextRoutingError(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	replica := newStubDB()
	defer replica.Close()

	cluster, err := New(Config{
		Primary:           primary,
		Replicas:          []*sql.DB{replica},
		FallbackToPrimary: false,
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	})
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cluster.Close()

	cluster.markReplicaHealth(0, false)

	row := cluster.QueryRowContext(t.Context(), "SELECT * FROM test WHERE id = ?", 1)
	var got int
	err = row.Scan(&got)
	if !errors.Is(err, ErrNoHealthyReplicas) {
		t.Fatalf("Scan() error = %v, want %v", err, ErrNoHealthyReplicas)
	}

	metrics := cluster.Metrics()
	if metrics.RoutingErrors.Load() != 1 {
		t.Errorf("got %d routing errors, want 1", metrics.RoutingErrors.Load())
	}
}

func TestClusterNoReplicas(t *testing.T) {
	primary := newStubDB()
	defer primary.Close()

	config := Config{
		Primary:  primary,
		Replicas: []*sql.DB{}, // No replicas
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	}

	cluster, err := New(config)
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer cluster.Close()

	// Read query should fallback to primary
	ctx := t.Context()
	rows, err := cluster.QueryContext(ctx, "SELECT * FROM test")
	if err != nil {
		t.Errorf("QueryContext failed: %v", err)
	}
	if rows != nil {
		rows.Close()
	}

	metrics := cluster.Metrics()
	if metrics.PrimaryQueries.Load() != 1 {
		t.Errorf("got %d primary queries, want 1", metrics.PrimaryQueries.Load())
	}
	if metrics.FallbackCount.Load() != 1 {
		t.Errorf("got %d fallback count, want 1", metrics.FallbackCount.Load())
	}
}

func TestLoadBalancerNames(t *testing.T) {
	weightedLB, err := NewWeightedBalancer([]int{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		lb   LoadBalancer
		want string
	}{
		{"RoundRobin", NewRoundRobinBalancer(), "round_robin"},
		{"Random", NewRandomBalancer(), "random"},
		{"LeastConn", NewLeastConnBalancer(), "least_connections"},
		{"Weighted", weightedLB, "weighted_round_robin"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.lb.Name(); got != tt.want {
				t.Errorf("Name() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTransactionAwarePolicyNilBase(t *testing.T) {
	// Should use default SQLTypePolicy when base is nil
	policy := NewTransactionAwarePolicy(nil)

	ctx := t.Context()

	// Should route write to primary
	if !policy.ShouldUsePrimary(ctx, "INSERT INTO test VALUES (1)") {
		t.Error("expected INSERT to use primary")
	}

	// Should route read to replica
	if policy.ShouldUsePrimary(ctx, "SELECT * FROM test") {
		t.Error("expected SELECT to use replica")
	}
}

func TestCompositePolicyEmpty(t *testing.T) {
	policy := NewCompositePolicy()

	ctx := t.Context()

	// No policies, should default to false (replica)
	if policy.ShouldUsePrimary(ctx, "INSERT INTO test VALUES (1)") {
		t.Error("expected empty composite policy to return false")
	}
}

// Benchmark tests
func BenchmarkClusterQueryContext(b *testing.B) {
	primary := newStubDB()
	defer primary.Close()

	replica := newStubDB()
	defer replica.Close()

	config := Config{
		Primary:  primary,
		Replicas: []*sql.DB{replica},
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	}

	cluster, _ := New(config)
	defer cluster.Close()

	ctx := b.Context()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, _ := cluster.QueryContext(ctx, "SELECT * FROM test")
		if rows != nil {
			rows.Close()
		}
	}
}

func BenchmarkClusterQueryContextPrimary(b *testing.B) {
	primary := newStubDB()
	defer primary.Close()

	config := Config{
		Primary: primary,
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	}

	cluster, _ := New(config)
	defer cluster.Close()

	ctx := b.Context()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, _ := cluster.QueryContext(ctx, "SELECT * FROM test FOR UPDATE")
		if rows != nil {
			rows.Close()
		}
	}
}

func BenchmarkClusterExecContext(b *testing.B) {
	primary := newStubDB()
	defer primary.Close()

	config := Config{
		Primary: primary,
		HealthCheck: HealthCheckConfig{
			Enabled: false,
		},
	}

	cluster, _ := New(config)
	defer cluster.Close()

	ctx := b.Context()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cluster.ExecContext(ctx, "INSERT INTO test VALUES (?)", i)
	}
}

func BenchmarkRoutingDecision(b *testing.B) {
	policy := NewTransactionAwarePolicy(NewSQLTypePolicy())
	ctx := b.Context()

	queries := []string{
		"SELECT * FROM users",
		"INSERT INTO users VALUES (1)",
		"UPDATE users SET name = 'test'",
		"SELECT * FROM users FOR UPDATE",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		query := queries[i%len(queries)]
		policy.ShouldUsePrimary(ctx, query)
	}
}

func BenchmarkLoadBalancerNext(b *testing.B) {
	lb := NewRoundRobinBalancer()
	replicas := []Replica{
		{Index: 0, IsHealthy: true},
		{Index: 1, IsHealthy: true},
		{Index: 2, IsHealthy: true},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = lb.Next(replicas)
	}
}
