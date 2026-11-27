package main

import (
	"context"
	"slices"
	"sync"
	"time"
)

type assetArrivalRate struct {
	requests, rate, workers, waiting int
}

func assetLatencyP95(latencies []time.Duration) time.Duration {
	if len(latencies) == 0 {
		return 0
	}
	slices.Sort(latencies)
	return latencies[len(latencies)-len(latencies)/20-1]
}

type assetArrivalResult struct {
	latency time.Duration
	err     error
	dropped bool
}

func assetArrivalPlan(seconds, rate, workers int, maxP95 time.Duration) assetArrivalRate {
	waiting := int(maxP95*time.Duration(rate)/time.Second) - workers
	return assetArrivalRate{requests: seconds * rate, rate: rate, workers: workers, waiting: max(1, waiting)}
}

func driveAssetArrivals(ctx context.Context, start time.Time, plan assetArrivalRate,
	read func(context.Context, int, time.Time) error,
) ([]assetArrivalResult, error) {
	type arrival struct {
		index int
		at    time.Time
	}
	results := make([]assetArrivalResult, plan.requests)
	queue := make(chan arrival, plan.waiting)
	var workers sync.WaitGroup
	for range plan.workers {
		workers.Go(func() {
			for r := range queue {
				if ctx.Err() != nil {
					return
				}
				results[r.index].err = read(ctx, r.index, r.at)
				results[r.index].latency = time.Since(r.at)
			}
		})
	}
	timer := time.NewTimer(time.Until(start))
	defer timer.Stop()
schedule:
	for i := range plan.requests {
		at := start.Add(time.Duration(i) * time.Second / time.Duration(plan.rate))
		if i != 0 {
			timer.Reset(time.Until(at))
		}
		select {
		case <-ctx.Done():
			break schedule
		case <-timer.C:
		}
		select {
		case queue <- arrival{i, at}:
		default:
			results[i].dropped = true
		}
	}
	close(queue)
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
