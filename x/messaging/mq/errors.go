package mq

import "errors"

var (
	// ErrRecoveredPanic is returned when a broker operation recovers from panic.
	ErrRecoveredPanic = errors.New("mq: panic recovered")

	// ErrNotInitialized is returned when the broker is not properly initialized.
	ErrNotInitialized = errors.New("mq: broker not initialized")

	// ErrInvalidTopic is returned when a topic is invalid.
	ErrInvalidTopic = errors.New("mq: invalid topic")

	// ErrNilMessage is returned when attempting to publish a nil message.
	ErrNilMessage = errors.New("mq: message cannot be nil")

	// ErrInvalidConfig is returned when broker configuration is invalid.
	ErrInvalidConfig = errors.New("mq: invalid configuration")

	// ErrBrokerClosed is returned when attempting to use a closed broker.
	ErrBrokerClosed = errors.New("mq: broker is closed")

	// ErrMessageAcknowledged is returned when attempting to acknowledge an already acknowledged message.
	ErrMessageAcknowledged = errors.New("mq: message already acknowledged")

	// ErrMessageNotAcked is returned when a message requires acknowledgment but none was received.
	ErrMessageNotAcked = errors.New("mq: message requires acknowledgment")

	// ErrClusterDisabled is returned when cluster mode is disabled.
	ErrClusterDisabled = errors.New("mq: cluster mode is disabled")

	// ErrNodeNotFound is returned when a cluster node is not found.
	ErrNodeNotFound = errors.New("mq: cluster node not found")

	// ErrTransactionNotSupported is returned when transaction is not supported.
	ErrTransactionNotSupported = errors.New("mq: transaction not supported")

	// ErrDeadLetterNotSupported is returned when dead letter queue is not supported.
	ErrDeadLetterNotSupported = errors.New("mq: dead letter queue not supported")

	// ErrMemoryLimitExceeded is returned when memory usage exceeds the configured limit.
	ErrMemoryLimitExceeded = errors.New("mq: memory limit exceeded")

	// ErrMessageExpired is returned when a message has expired based on its TTL.
	ErrMessageExpired = errors.New("mq: message has expired")

	// ErrTransactionNotFound is returned when a transaction ID is not found.
	ErrTransactionNotFound = errors.New("mq: transaction not found")

	// ErrTransactionTimeout is returned when a transaction exceeds its timeout.
	ErrTransactionTimeout = errors.New("mq: transaction timeout")

	// ErrTransactionCommitted is returned when attempting to use a committed transaction.
	ErrTransactionCommitted = errors.New("mq: transaction already committed")

	// ErrTransactionRolledBack is returned when attempting to use a rolled back transaction.
	ErrTransactionRolledBack = errors.New("mq: transaction already rolled back")

	// ErrDuplicateTask is returned when a task with the same ID already exists.
	ErrDuplicateTask = errors.New("mq: duplicate task")

	// ErrTaskNotFound is returned when a task is not found.
	ErrTaskNotFound = errors.New("mq: task not found")

	// ErrLeaseLost is returned when a task lease is lost.
	ErrLeaseLost = errors.New("mq: lease lost")
	// ErrClaimPaused 表示登记执行时领取被暂停，应保留任务并归还当前代次。
	ErrClaimPaused = errors.New("mq: claim paused")
	// ErrExecutionHandled 表示登记回调已提交当前领取的状态转换。
	ErrExecutionHandled = errors.New("mq: execution handled")

	// ErrTaskExpired is returned when a task has expired.
	ErrTaskExpired = errors.New("mq: task expired")
)
