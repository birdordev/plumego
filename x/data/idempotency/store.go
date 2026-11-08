package idempotency

import (
	"errors"

	stable "github.com/birdordev/plumego/store/idempotency"
)

var (
	ErrNotFound      = stable.ErrNotFound
	ErrInvalidKey    = stable.ErrInvalidKey
	ErrInvalidRecord = stable.ErrInvalidRecord
	ErrExpired       = stable.ErrExpired
	ErrInvalidConfig = errors.New("idempotency: invalid config")
)

type Status = stable.Status

const (
	StatusInProgress = stable.StatusInProgress
	StatusCompleted  = stable.StatusCompleted
)

type Record = stable.Record

type Store = stable.Store

func ValidateKey(key string) error {
	return stable.ValidateKey(key)
}

func ValidateRecord(record Record) error {
	return stable.ValidateRecord(record)
}
