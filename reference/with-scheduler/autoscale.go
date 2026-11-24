package main

import (
	"context"
	"errors"
	"log"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
	"time"
)

type autoscaleConfig struct {
	pool                          workerPoolConfig
	minWorkers, maxWorkers        int
	interval, window, age, settle time.Duration
	drain                         time.Duration
}

func loadAutoscaleConfig() (autoscaleConfig, error) {
	var cfg autoscaleConfig
	pool := os.Getenv("SCHEDULER_WORKER_POOL")
	if pool == "" {
		pool = "io"
	}
	var err error
	cfg.pool, err = loadWorkerPool(pool, os.Getenv("SCHEDULER_WORKER_TASK_VERSIONS"))
	if err != nil {
		return cfg, err
	}
	if pool == "io" && os.Getenv("SCHEDULER_RESULT_DIR") != "" {
		c, _ := lookupTaskContract(exportTaskType, 1)
		cfg.pool.contracts = append(cfg.pool.contracts, c)
	}
	cfg.minWorkers, cfg.maxWorkers = 1, 4
	for _, setting := range []struct {
		name  string
		value *int
	}{
		{"SCHEDULER_AUTOSCALE_MIN", &cfg.minWorkers}, {"SCHEDULER_AUTOSCALE_MAX", &cfg.maxWorkers},
	} {
		if raw := os.Getenv(setting.name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > 32 {
				return cfg, errors.New("invalid " + setting.name)
			}
			*setting.value = value
		}
	}
	if cfg.minWorkers > cfg.maxWorkers {
		return cfg, errors.New("autoscale minimum exceeds maximum")
	}
	for _, setting := range []struct {
		name     string
		value    *time.Duration
		fallback time.Duration
	}{
		{"SCHEDULER_AUTOSCALE_INTERVAL", &cfg.interval, time.Second},
		{"SCHEDULER_AUTOSCALE_WINDOW", &cfg.window, 30 * time.Second},
		{"SCHEDULER_AUTOSCALE_TARGET_AGE", &cfg.age, 2 * time.Second},
		{"SCHEDULER_AUTOSCALE_SCALE_IN_DELAY", &cfg.settle, 30 * time.Second},
		{"SCHEDULER_WORKER_DRAIN_TIMEOUT", &cfg.drain, 10 * time.Second},
	} {
		*setting.value, err = workerDurationEnv(setting.name, setting.fallback)
		if err != nil {
			return cfg, err
		}
	}
	if cfg.interval < 50*time.Millisecond || cfg.window < cfg.interval || cfg.settle < cfg.interval {
		return cfg, errors.New("invalid autoscale observation windows")
	}
	return cfg, nil
}

func scaleTarget(cfg autoscaleConfig, s scaleSnapshot, current int, idle time.Duration) int {
	if current < cfg.minWorkers {
		return cfg.minWorkers
	}
	capacity := float64(current * cfg.pool.concurrency)
	saturation := float64(s.active) / capacity
	if s.ready == 0 && s.arrivals <= s.processed && saturation < 0.5 && idle >= cfg.settle {
		return max(cfg.minWorkers, current-1)
	}
	if s.ready == 0 || (s.oldest < cfg.age.Seconds() && s.arrivals <= s.processed) ||
		(saturation < 0.6 && float64(s.ready) <= capacity) {
		return current
	}
	target := current + 1
	perWorker := s.processed / float64(current)
	if perWorker > 0 {
		demand := (s.arrivals + float64(s.ready)/cfg.age.Seconds()) / perWorker
		target = max(target, int(math.Min(float64(cfg.maxWorkers), math.Ceil(demand))))
	}
	if s.downstreamSlots >= 0 {
		target = min(target, current+s.downstreamSlots/cfg.pool.concurrency)
	}
	if s.downstreamCap > 0 {
		target = min(target, max(current, s.downstreamCap/cfg.pool.concurrency))
	}
	if s.downstreamMinStartMS > 0 {
		headroom := 1000/float64(s.downstreamMinStartMS) - s.downstreamStarts
		if headroom < max(0.1, perWorker) {
			return current
		}
	}
	return min(target, cfg.maxWorkers)
}

type managedWorker struct {
	process  *os.Process
	done     <-chan error
	draining time.Time
}

func startManagedWorker(cmd *exec.Cmd) (*managedWorker, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return &managedWorker{process: cmd.Process, done: done}, nil
}

func (w *managedWorker) drain(now time.Time) {
	if w.draining.IsZero() {
		w.draining = now
		if err := w.process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			log.Print("could not signal autoscaled worker")
		}
	}
}

func runLocalAutoscaler(ctx context.Context, repo repository) error {
	if runtime.GOOS == "windows" {
		return errors.New("local autoscaling requires SIGTERM support")
	}
	cfg, err := loadAutoscaleConfig()
	if err != nil {
		return err
	}
	path, err := os.Executable()
	if err != nil {
		return errors.New("worker executable unavailable")
	}
	return superviseWorkers(ctx, repo, cfg, func() (*managedWorker, error) {
		cmd := exec.Command(path, "-role", "worker")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		cmd.Env = append(os.Environ(), "SCHEDULER_WORKER_POOL="+cfg.pool.name,
			"SCHEDULER_WORKER_PROBE_ADDR=", "SCHEDULER_METRICS_ADDR=")
		return startManagedWorker(cmd)
	})
}

func superviseWorkers(ctx context.Context, repo repository, cfg autoscaleConfig, start func() (*managedWorker, error)) error {
	topics := make([]string, 0, len(cfg.pool.contracts))
	for _, contract := range cfg.pool.contracts {
		topics = append(topics, contract.topic)
	}
	var workers []*managedWorker
	defer func() {
		now := time.Now()
		for _, w := range workers {
			w.drain(now)
		}
		deadline := time.NewTimer(cfg.drain + time.Second)
		defer deadline.Stop()
		for i, w := range workers {
			select {
			case <-w.done:
			case <-deadline.C:
				for _, unfinished := range workers[i:] {
					_ = unfinished.process.Kill()
					<-unfinished.done
				}
				return
			}
		}
	}()
	var idleSince time.Time
	ticker := time.NewTicker(cfg.interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		now := time.Now()
		available := workers[:0]
		current := 0
		for _, worker := range workers {
			select {
			case err := <-worker.done:
				if err != nil {
					log.Print("autoscaled worker exited")
				}
				continue
			default:
			}
			available = append(available, worker)
			if worker.draining.IsZero() {
				current++
			} else if now.Sub(worker.draining) > cfg.drain+time.Second {
				_ = worker.process.Kill()
			}
		}
		workers = available
		sampleCtx, cancel := context.WithTimeout(ctx, time.Second)
		snapshot, err := repo.readScaleSnapshot(sampleCtx, topics, cfg.window)
		cancel()
		if err != nil {
			idleSince = time.Time{}
			if ctx.Err() == nil {
				log.Print("autoscale sample unavailable; keeping worker count")
			}
		} else {
			if snapshot.ready > 0 || snapshot.arrivals > snapshot.processed || snapshot.active*2 >= current*cfg.pool.concurrency {
				idleSince = time.Time{}
			} else if idleSince.IsZero() {
				idleSince = now
			}
			var idle time.Duration
			if !idleSince.IsZero() {
				idle = now.Sub(idleSince)
			}
			target := scaleTarget(cfg, snapshot, current, idle)
			// Draining processes still consume local resources until they exit.
			if target > current {
				for current < target && len(workers) < cfg.maxWorkers && ctx.Err() == nil {
					w, err := start()
					if err != nil {
						log.Print("could not start autoscaled worker")
						break
					}
					workers = append(workers, w)
					current++
					log.Printf("autoscale pool=%s action=start workers=%d ready=%d oldest_seconds=%.3f arrivals_per_second=%.2f processed_per_second=%.2f active=%d downstream_slots=%d",
						cfg.pool.name, current, snapshot.ready, snapshot.oldest, snapshot.arrivals, snapshot.processed, snapshot.active, snapshot.downstreamSlots)
				}
			} else if target < current && len(workers) == current {
				workers[len(workers)-1].drain(now)
				idleSince = now
				log.Printf("autoscale pool=%s action=drain workers=%d active=%d", cfg.pool.name, current-1, snapshot.active)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	return nil
}
