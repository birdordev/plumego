package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
)

type workerPoolConfig struct {
	name                          string
	concurrency, maxOpen, maxIdle int
	contracts                     []taskContract
}

func loadWorkerPool(name, versions string) (workerPoolConfig, error) {
	var p workerPoolConfig
	if name == "" {
		name = "short"
	}
	p.name = name
	switch name {
	case "short":
		p.concurrency, p.maxOpen, p.maxIdle = 4, 16, 8
		contracts, err := workerTaskContracts(versions)
		if err != nil {
			return workerPoolConfig{}, errors.New("invalid SCHEDULER_WORKER_TASK_VERSIONS")
		}
		p.contracts = contracts
		for _, topic := range []string{reserveTaskType, releaseTaskType} {
			contract, _ := lookupTaskContract(topic, 1)
			p.contracts = append(p.contracts, contract)
		}
	case "cpu", "io", "sandbox":
		if versions != "" && versions != "1" {
			return workerPoolConfig{}, errors.New("selected Worker pool only supports version 1")
		}
		taskType := computeTaskType
		p.concurrency, p.maxOpen, p.maxIdle = min(runtime.GOMAXPROCS(0), 4), 8, 4
		switch name {
		case "io":
			taskType = ioTaskType
			p.concurrency = 2
		case "sandbox":
			taskType = sandboxTaskType
			p.concurrency = 1
		}
		contract, _ := lookupTaskContract(taskType, 1)
		p.contracts = []taskContract{contract}
		if name == "cpu" {
			checkpoint, _ := lookupTaskContract(checkpointTaskType, 1)
			p.contracts = append(p.contracts, checkpoint)
		}
	default:
		return workerPoolConfig{}, errors.New("invalid SCHEDULER_WORKER_POOL")
	}

	for _, setting := range []struct {
		env     string
		value   *int
		minimum int
	}{
		{"SCHEDULER_WORKER_CONCURRENCY", &p.concurrency, 1},
		{"SCHEDULER_WORKER_DB_MAX_OPEN", &p.maxOpen, 1},
		{"SCHEDULER_WORKER_DB_MAX_IDLE", &p.maxIdle, 0},
	} {
		raw := os.Getenv(setting.env)
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < setting.minimum {
			return workerPoolConfig{}, errors.New("invalid " + setting.env)
		}
		*setting.value = n
	}
	if os.Getenv("SCHEDULER_WORKER_DB_MAX_IDLE") == "" {
		p.maxIdle = min(p.maxIdle, p.maxOpen)
	}
	if p.maxIdle > p.maxOpen {
		return workerPoolConfig{}, errors.New("worker idle connections exceed open connection limit")
	}
	return p, nil
}

func ioDelay(payload json.RawMessage) (time.Duration, error) {
	var params struct {
		DelayMS *int `json:"delay_ms"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	raw := bytes.TrimSpace(payload)
	if len(raw) == 0 || raw[0] != '{' || !json.Valid(raw) || decoder.Decode(&params) != nil {
		return 0, errInvalidTask
	}
	if params.DelayMS == nil {
		return 2 * time.Second, nil
	}
	if *params.DelayMS < 1 || *params.DelayMS > 30000 {
		return 0, errInvalidTask
	}
	return time.Duration(*params.DelayMS) * time.Millisecond, nil
}

func (r repository) workerHandler(contract taskContract, objects *resultObjects, sandbox *sandboxExecutor) mq.TaskHandler {
	switch contract.taskType {
	case reserveTaskType:
		return r.reserveForWorkflow
	case releaseTaskType:
		return r.compensateReservation
	case checkpointTaskType:
		return r.checkpointTask
	case sandboxTaskType:
		return r.sandboxHandler(sandbox)
	case exportTaskType:
		return r.exportHandler(objects)
	case ioTaskType:
		return func(ctx context.Context, task mq.Task) error {
			delay, err := ioDelay(task.Payload)
			if err != nil {
				return &mq.TaskFailure{Kind: mq.TaskFailurePermanent}
			}
			if _, err := r.db.ExecContext(ctx, "SELECT pg_sleep($1)", delay.Seconds()); err != nil {
				return err
			}
			return r.recordResult(ctx, task)
		}
	case computeTaskType:
		return func(ctx context.Context, task mq.Task) error {
			digest := sha256.Sum256(task.Payload)
			for i := range 100000 {
				if i%256 == 0 && ctx.Err() != nil {
					return ctx.Err()
				}
				digest = sha256.Sum256(digest[:])
			}
			return r.recordResult(ctx, task)
		}
	default:
		return r.recordResult
	}
}
