package messaging

import (
	"time"

	"github.com/birdordev/plumego/x/messaging/mq"
	"github.com/birdordev/plumego/x/messaging/mq/store"
)

const (
	defaultWorkerConcurrency = 4
	defaultWorkerPoll        = 200 * time.Millisecond
	defaultWorkerLease       = 30 * time.Second
	defaultWorkerLeaseExtend = 15 * time.Second
	defaultWorkerShutdown    = 30 * time.Second
)

type serviceRuntime struct {
	store        mq.TaskStore
	queue        *mq.TaskQueue
	worker       *mq.Worker
	workerConfig mq.WorkerConfig
}

func newServiceRuntime(cfg Config) serviceRuntime {
	taskStore := cfg.TaskStore
	if taskStore == nil {
		taskStore = store.NewMemory(store.DefaultMemConfig())
	}

	workerCfg := newWorkerConfig(cfg)
	queue := mq.NewTaskQueue(taskStore)

	return serviceRuntime{
		store:        taskStore,
		queue:        queue,
		worker:       mq.NewWorker(queue, workerCfg),
		workerConfig: workerCfg,
	}
}

func newWorkerConfig(cfg Config) mq.WorkerConfig {
	concurrency := cfg.WorkerConcurrency
	if concurrency <= 0 {
		concurrency = defaultWorkerConcurrency
	}
	maxInflight := cfg.WorkerMaxInflight
	if maxInflight <= 0 {
		maxInflight = concurrency * 2
	}

	return mq.WorkerConfig{
		ConsumerID:          cfg.ConsumerID,
		Concurrency:         concurrency,
		MaxInflight:         maxInflight,
		LeaseDuration:       defaultWorkerLease,
		LeaseExtendInterval: defaultWorkerLeaseExtend,
		PollInterval:        defaultWorkerPoll,
		ShutdownTimeout:     defaultWorkerShutdown,
		RetryPolicy: mq.ExponentialBackoff{
			Base:   2 * time.Second,
			Max:    5 * time.Minute,
			Factor: 2,
			Jitter: 0.2,
		},
	}
}
