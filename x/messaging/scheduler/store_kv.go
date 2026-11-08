package scheduler

import (
	"context"
	"encoding/json"
	"strings"

	kvstore "github.com/birdordev/plumego/store/kv"
)

// KVStore persists jobs in the internal KV store.
type KVStore struct {
	kv     *kvstore.KVStore
	prefix string
}

// NewKVStore constructs a KV-backed scheduler store.
func NewKVStore(kv *kvstore.KVStore, prefix string) *KVStore {
	if prefix == "" {
		prefix = "scheduler:job:"
	}
	if !strings.HasSuffix(prefix, ":") {
		prefix += ":"
	}
	return &KVStore{kv: kv, prefix: prefix}
}

func (k *KVStore) Save(job StoredJob) error {
	if k == nil || k.kv == nil {
		return nil
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return err
	}
	return k.kv.Set(k.prefix+string(job.ID), payload, 0)
}

func (k *KVStore) Delete(id JobID) error {
	if k == nil || k.kv == nil {
		return nil
	}
	return k.kv.Delete(k.prefix + string(id))
}

func (k *KVStore) List() ([]StoredJob, error) {
	if k == nil || k.kv == nil {
		return nil, nil
	}
	keys, err := k.kv.KeysContext(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]StoredJob, 0)
	for _, key := range keys {
		if !strings.HasPrefix(key, k.prefix) {
			continue
		}
		raw, err := k.kv.Get(key)
		if err != nil {
			continue
		}
		var job StoredJob
		if err := json.Unmarshal(raw, &job); err != nil {
			continue
		}
		out = append(out, job)
	}
	return out, nil
}
