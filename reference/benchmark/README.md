# Plumego Benchmarks

This directory is a separate Go module for local benchmark evidence.
It keeps comparison-only dependencies (chi, gin, echo) out of the main module.

## Prerequisites

- Go 1.24 or newer.
- A source checkout of `github.com/birdordev/plumego`.

## Run

From the repository root:

```bash
cd reference/benchmark
go test -bench=. -benchmem -count=3 ./...
```

Or from this directory:

```bash
go test -bench=. -benchmem -count=3 ./...
```

## Benchmark dimensions

### Router — route shape

Compares request dispatch overhead across Plumego, Chi, Gin, and Echo for three
route shapes: static, single path param, and multi path param.

```
BenchmarkRouterStatic/{framework}
BenchmarkRouterSingleParam/{framework}
BenchmarkRouterMultiParam/{framework}
```

### Router — route table scale

Registers 100 or 500 routes and dispatches to the last one. Verifies that
lookup time does not degrade significantly with larger route tables.

```
BenchmarkRouterScale100/{framework}
BenchmarkRouterScale500/{framework}
```

### Router — parallel throughput

Single path-param route under concurrent load (`b.RunParallel`). Measures
throughput at GOMAXPROCS goroutines.

```
BenchmarkRouterParallel{Framework}
```

### Router — 404 overhead

Measures the cost of a request that matches no route.

```
BenchmarkRouterNotFound{Framework}
```

### Middleware chain — stdlib-compatible

Tests `func(http.Handler) http.Handler` chains at 1, 3, and 5 layers with a
no-op handler (HTTP 204) and a JSON handler (HTTP 200). These chains are
framework-neutral; any stdlib-compatible router (Plumego, Chi) can use them.

```
BenchmarkChain{1,3,5}{NoOp,JSON}
```

### Middleware chain — Gin

Tests Gin's native `gin.HandlerFunc` chain at 1, 3, and 5 layers.

```
BenchmarkGinChain{1,3,5}{NoOp,JSON}
```

### Middleware chain — Echo

Tests Echo's native `echo.MiddlewareFunc` chain at 1, 3, and 5 layers.

```
BenchmarkEchoChain{1,3,5}{NoOp,JSON}
```

## Update Results

When capturing release evidence, run the full suite and append the verbatim
output to `docs/evidence/benchmarks/results-vX.Y.Z.md` with the machine, Go version,
command, and date.
