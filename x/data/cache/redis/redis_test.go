package redis

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/birdordev/plumego/store/cache"
)

var errMiss = errors.New("cache miss")

type stubClient struct {
	data map[string][]byte
}

func (s *stubClient) Get(ctx context.Context, key string) ([]byte, error) {
	value, ok := s.data[key]
	if !ok {
		return nil, errMiss
	}
	return value, nil
}

func (s *stubClient) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if s.data == nil {
		s.data = make(map[string][]byte)
	}
	s.data[key] = value
	return nil
}

func (s *stubClient) Del(ctx context.Context, keys ...string) (int64, error) {
	var removed int64
	for _, key := range keys {
		if _, ok := s.data[key]; ok {
			delete(s.data, key)
			removed++
		}
	}
	return removed, nil
}

func (s *stubClient) Exists(ctx context.Context, keys ...string) (int64, error) {
	var count int64
	for _, key := range keys {
		if _, ok := s.data[key]; ok {
			count++
		}
	}
	return count, nil
}

func (s *stubClient) IncrBy(ctx context.Context, key string, delta int64) (int64, error) {
	if s.data == nil {
		s.data = make(map[string][]byte)
	}
	current := int64(0)
	if value, ok := s.data[key]; ok && len(value) > 0 {
		parsed, err := strconv.ParseInt(string(value), 10, 64)
		if err != nil {
			return 0, cache.ErrNotInteger
		}
		current = parsed
	}
	next := current + delta
	s.data[key] = []byte(strconv.FormatInt(next, 10))
	return next, nil
}

func (s *stubClient) Append(ctx context.Context, key string, data []byte) (int64, error) {
	if s.data == nil {
		s.data = make(map[string][]byte)
	}
	value := append([]byte(nil), s.data[key]...)
	value = append(value, data...)
	s.data[key] = value
	return int64(len(value)), nil
}

type noAtomicClient struct {
	data map[string][]byte
}

func (s *noAtomicClient) Get(ctx context.Context, key string) ([]byte, error) {
	value, ok := s.data[key]
	if !ok {
		return nil, errMiss
	}
	return value, nil
}

func (s *noAtomicClient) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if s.data == nil {
		s.data = make(map[string][]byte)
	}
	s.data[key] = value
	return nil
}

func (s *noAtomicClient) Del(ctx context.Context, keys ...string) (int64, error) {
	var removed int64
	for _, key := range keys {
		if _, ok := s.data[key]; ok {
			delete(s.data, key)
			removed++
		}
	}
	return removed, nil
}

func (s *noAtomicClient) Exists(ctx context.Context, keys ...string) (int64, error) {
	var count int64
	for _, key := range keys {
		if _, ok := s.data[key]; ok {
			count++
		}
	}
	return count, nil
}

type aliasAppendClient struct {
	noAtomicClient
}

func (s *aliasAppendClient) Append(ctx context.Context, key string, data []byte) (int64, error) {
	if s.data == nil {
		s.data = make(map[string][]byte)
	}
	s.data[key] = data
	return int64(len(data)), nil
}

type stubFlusher struct {
	stubClient
	flushed bool
}

func (s *stubFlusher) FlushDB(ctx context.Context) error {
	s.data = make(map[string][]byte)
	s.flushed = true
	return nil
}

type noAtomicFlusher struct {
	noAtomicClient
	flushed bool
}

func (s *noAtomicFlusher) FlushDB(ctx context.Context) error {
	s.data = make(map[string][]byte)
	s.flushed = true
	return nil
}

type stubPrefixFlusher struct {
	stubFlusher
	prefixes []string
}

func (s *stubPrefixFlusher) FlushPrefix(ctx context.Context, prefix string) error {
	s.prefixes = append(s.prefixes, prefix)
	for key := range s.data {
		if strings.HasPrefix(key, prefix) {
			delete(s.data, key)
		}
	}
	return nil
}

func TestAdapterGetNotFound(t *testing.T) {
	adapter := NewAdapter(&stubClient{}, func(err error) bool {
		return errors.Is(err, errMiss)
	})

	_, err := adapter.Get(t.Context(), "missing")
	if !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestAdapterGetMissRequiresConfiguredMapper(t *testing.T) {
	adapter, err := NewValidatedAdapterWithOptions(&stubClient{})
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	_, err = adapter.Get(t.Context(), "missing")
	if !errors.Is(err, errMiss) {
		t.Fatalf("expected raw client miss error, got %v", err)
	}
	if errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("unexpected stable cache miss without mapper: %v", err)
	}

	mapped, err := NewValidatedAdapterWithOptions(&stubClient{}, WithNotFound(func(err error) bool {
		return errors.Is(err, errMiss)
	}))
	if err != nil {
		t.Fatalf("unexpected mapped adapter validation error: %v", err)
	}
	_, err = mapped.Get(t.Context(), "missing")
	if !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("expected mapped ErrNotFound, got %v", err)
	}
}

func TestAdapterSetAndExists(t *testing.T) {
	client := &stubClient{data: make(map[string][]byte)}
	adapter := NewAdapter(client, nil)

	if err := adapter.Set(t.Context(), "key", []byte("value"), time.Minute); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	exists, err := adapter.Exists(t.Context(), "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Fatalf("expected key to exist")
	}
}

func TestNewAdapterWithOptions(t *testing.T) {
	adapter := NewAdapterWithOptions(&stubClient{},
		WithNotFound(func(err error) bool {
			return errors.Is(err, errMiss)
		}),
		WithMaxKeyLength(5),
		WithAllowFlushDB(true),
		WithClearPrefix("app:"),
	)

	if adapter.MaxKeyLength != 5 {
		t.Fatalf("MaxKeyLength = %d, want 5", adapter.MaxKeyLength)
	}
	if !adapter.AllowFlushDB {
		t.Fatal("expected AllowFlushDB")
	}
	if adapter.ClearPrefix != "app:" {
		t.Fatalf("ClearPrefix = %q, want app:", adapter.ClearPrefix)
	}
	if adapter.IsNotFound == nil || !adapter.IsNotFound(errMiss) {
		t.Fatal("expected IsNotFound option to be installed")
	}
}

func TestNewAdapterWithOptionsFreezesConfiguredBehavior(t *testing.T) {
	client := &stubPrefixFlusher{
		stubFlusher: stubFlusher{
			stubClient: stubClient{data: map[string][]byte{
				"app:key":   []byte("value"),
				"other:key": []byte("value"),
			}},
		},
	}
	adapter := NewAdapterWithOptions(client,
		WithNotFound(func(err error) bool {
			return errors.Is(err, errMiss)
		}),
		WithMaxKeyLength(5),
		WithClearPrefix("app:"),
	)

	adapter.IsNotFound = nil
	adapter.MaxKeyLength = 100
	adapter.ClearPrefix = "other:"

	if _, err := adapter.Get(t.Context(), "miss"); !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("expected frozen not-found mapper, got %v", err)
	}
	if err := adapter.Set(t.Context(), "toolong", []byte("value"), 0); !errors.Is(err, cache.ErrKeyTooLong) {
		t.Fatalf("expected frozen key length, got %v", err)
	}
	if err := adapter.Clear(t.Context()); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}
	if _, ok := client.data["app:key"]; ok {
		t.Fatal("expected frozen app: prefix to be cleared")
	}
	if _, ok := client.data["other:key"]; !ok {
		t.Fatal("expected mutated other: prefix to be ignored")
	}
}

func TestNewAdapterWithOptionsFreezesFlushDBPolicy(t *testing.T) {
	client := &stubFlusher{stubClient: stubClient{data: map[string][]byte{"key": []byte("value")}}}
	adapter := NewAdapterWithOptions(client, WithAllowFlushDB(false))
	adapter.AllowFlushDB = true

	if err := adapter.Clear(t.Context()); !errors.Is(err, ErrFlushDBDisabled) {
		t.Fatalf("expected ErrFlushDBDisabled, got %v", err)
	}
	if client.flushed {
		t.Fatal("expected frozen flush policy to reject FlushDB")
	}
}

func TestNewValidatedAdapterWithOptionsRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		new  func() (*Adapter, error)
		want error
	}{
		{
			name: "nil client",
			new: func() (*Adapter, error) {
				return NewValidatedAdapterWithOptions(nil)
			},
			want: ErrNilClient,
		},
		{
			name: "negative max key length",
			new: func() (*Adapter, error) {
				return NewValidatedAdapterWithOptions(&stubClient{}, WithMaxKeyLength(-1))
			},
			want: cache.ErrInvalidConfig,
		},
		{
			name: "empty clear prefix",
			new: func() (*Adapter, error) {
				return NewValidatedAdapterWithOptions(&stubClient{}, WithClearPrefix(""))
			},
			want: cache.ErrInvalidKey,
		},
		{
			name: "invalid clear prefix",
			new: func() (*Adapter, error) {
				return NewValidatedAdapterWithOptions(&stubClient{}, WithClearPrefix("bad\nprefix"))
			},
			want: cache.ErrInvalidKey,
		},
		{
			name: "custom option negative max key length",
			new: func() (*Adapter, error) {
				return NewValidatedAdapterWithOptions(&stubClient{}, func(a *Adapter) {
					a.MaxKeyLength = -1
				})
			},
			want: cache.ErrInvalidConfig,
		},
		{
			name: "custom option invalid clear prefix",
			new: func() (*Adapter, error) {
				return NewValidatedAdapterWithOptions(&stubClient{}, func(a *Adapter) {
					a.ClearPrefix = "bad\nprefix"
				})
			},
			want: cache.ErrInvalidKey,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adapter, err := tc.new()
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if adapter != nil {
				t.Fatal("expected nil adapter on validation error")
			}
		})
	}
}

func TestNewValidatedAdapterWithOptionsAcceptsValidConfig(t *testing.T) {
	adapter, err := NewValidatedAdapterWithOptions(
		&stubPrefixFlusher{},
		WithMaxKeyLength(0),
		WithClearPrefix("app:"),
	)
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if adapter == nil {
		t.Fatal("expected adapter")
	}
}

func TestNewValidatedAdapterWithOptionsCustomOptionsAreCompatibilityHooks(t *testing.T) {
	client := &stubPrefixFlusher{
		stubFlusher: stubFlusher{
			stubClient: stubClient{data: map[string][]byte{
				"app:key":   []byte("value"),
				"other:key": []byte("value"),
			}},
		},
	}
	adapter, err := NewValidatedAdapterWithOptions(client, func(a *Adapter) {
		a.ClearPrefix = "app:"
	})
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	adapter.ClearPrefix = "other:"
	if err := adapter.Clear(t.Context()); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}
	if _, ok := client.data["app:key"]; !ok {
		t.Fatal("expected custom option prefix to remain mutable compatibility state")
	}
	if _, ok := client.data["other:key"]; ok {
		t.Fatal("expected mutated compatibility prefix to be cleared")
	}
}

func TestNewValidatedAdapterWithOptionsFreezesConfiguredBehavior(t *testing.T) {
	client := &stubPrefixFlusher{
		stubFlusher: stubFlusher{
			stubClient: stubClient{data: map[string][]byte{
				"app:key":   []byte("value"),
				"other:key": []byte("value"),
			}},
		},
	}
	adapter, err := NewValidatedAdapterWithOptions(client,
		WithNotFound(func(err error) bool {
			return errors.Is(err, errMiss)
		}),
		WithMaxKeyLength(5),
		WithClearPrefix("app:"),
	)
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	adapter.IsNotFound = nil
	adapter.MaxKeyLength = 100
	adapter.ClearPrefix = "other:"

	if _, err := adapter.Get(t.Context(), "miss"); !errors.Is(err, cache.ErrNotFound) {
		t.Fatalf("expected frozen not-found mapper, got %v", err)
	}
	if err := adapter.Set(t.Context(), "toolong", []byte("value"), 0); !errors.Is(err, cache.ErrKeyTooLong) {
		t.Fatalf("expected frozen key length, got %v", err)
	}
	if err := adapter.Clear(t.Context()); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}
	if _, ok := client.data["app:key"]; ok {
		t.Fatal("expected frozen app: prefix to be cleared")
	}
	if _, ok := client.data["other:key"]; !ok {
		t.Fatal("expected mutated other: prefix to be ignored")
	}
}

func TestNewValidatedAdapterWithOptionsFreezesClearPolicy(t *testing.T) {
	client := &stubPrefixFlusher{
		stubFlusher: stubFlusher{
			stubClient: stubClient{data: map[string][]byte{
				"app:key":   []byte("value"),
				"other:key": []byte("value"),
			}},
		},
	}
	adapter, err := NewValidatedAdapterWithOptions(client,
		WithAllowFlushDB(false),
		WithClearPrefix("app:"),
	)
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	adapter.AllowFlushDB = true
	adapter.ClearPrefix = "other:"

	if err := adapter.Clear(t.Context()); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}
	if client.flushed {
		t.Fatal("expected frozen prefix clear to avoid mutated FlushDB policy")
	}
	if _, ok := client.data["app:key"]; ok {
		t.Fatal("expected frozen app: prefix to be cleared")
	}
	if _, ok := client.data["other:key"]; !ok {
		t.Fatal("expected mutated other: prefix to be ignored")
	}
}

func TestValidatedCapabilityConstructors(t *testing.T) {
	t.Run("counter", func(t *testing.T) {
		adapter, err := NewValidatedCounterAdapterWithOptions(&stubClient{}, WithMaxKeyLength(5))
		if err != nil {
			t.Fatalf("NewValidatedCounterAdapterWithOptions: %v", err)
		}
		if _, err := adapter.Incr(t.Context(), "count", 1); err != nil {
			t.Fatalf("Incr failed: %v", err)
		}
		adapter.MaxKeyLength = 100
		if _, err := adapter.Incr(t.Context(), "toolong", 1); !errors.Is(err, cache.ErrKeyTooLong) {
			t.Fatalf("expected frozen key length, got %v", err)
		}
	})

	t.Run("appender", func(t *testing.T) {
		adapter, err := NewValidatedAppenderAdapterWithOptions(&stubClient{}, WithMaxKeyLength(5))
		if err != nil {
			t.Fatalf("NewValidatedAppenderAdapterWithOptions: %v", err)
		}
		if err := adapter.Append(t.Context(), "key", []byte("value")); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
		adapter.MaxKeyLength = 100
		if err := adapter.Append(t.Context(), "toolong", []byte("value")); !errors.Is(err, cache.ErrKeyTooLong) {
			t.Fatalf("expected frozen key length, got %v", err)
		}
	})

	t.Run("atomic", func(t *testing.T) {
		adapter, err := NewValidatedAtomicAdapterWithOptions(&stubClient{}, WithMaxKeyLength(5))
		if err != nil {
			t.Fatalf("NewValidatedAtomicAdapterWithOptions: %v", err)
		}
		if _, err := adapter.Incr(t.Context(), "count", 1); err != nil {
			t.Fatalf("Incr failed: %v", err)
		}
		if err := adapter.Append(t.Context(), "key", []byte("value")); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
		adapter.MaxKeyLength = 100
		if _, err := adapter.Incr(t.Context(), "toolong", 1); !errors.Is(err, cache.ErrKeyTooLong) {
			t.Fatalf("expected frozen key length, got %v", err)
		}
	})
}

func TestValidatedCapabilityConstructorsRejectInvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		new  func() error
		want error
	}{
		{
			name: "counter nil client",
			new: func() error {
				_, err := NewValidatedCounterAdapterWithOptions(nil)
				return err
			},
			want: ErrNilClient,
		},
		{
			name: "counter invalid option",
			new: func() error {
				_, err := NewValidatedCounterAdapterWithOptions(&stubClient{}, WithMaxKeyLength(-1))
				return err
			},
			want: cache.ErrInvalidConfig,
		},
		{
			name: "counter unsupported",
			new: func() error {
				_, err := NewValidatedCounterAdapterWithOptions(&noAtomicClient{})
				return err
			},
			want: cache.ErrCapabilityUnsupported,
		},
		{
			name: "appender nil client",
			new: func() error {
				_, err := NewValidatedAppenderAdapterWithOptions(nil)
				return err
			},
			want: ErrNilClient,
		},
		{
			name: "appender invalid option",
			new: func() error {
				_, err := NewValidatedAppenderAdapterWithOptions(&stubClient{}, WithClearPrefix(""))
				return err
			},
			want: cache.ErrInvalidKey,
		},
		{
			name: "appender unsupported",
			new: func() error {
				_, err := NewValidatedAppenderAdapterWithOptions(&noAtomicClient{})
				return err
			},
			want: cache.ErrCapabilityUnsupported,
		},
		{
			name: "atomic nil client",
			new: func() error {
				_, err := NewValidatedAtomicAdapterWithOptions(nil)
				return err
			},
			want: ErrNilClient,
		},
		{
			name: "atomic invalid option",
			new: func() error {
				_, err := NewValidatedAtomicAdapterWithOptions(&stubClient{}, WithClearPrefix("bad\nprefix"))
				return err
			},
			want: cache.ErrInvalidKey,
		},
		{
			name: "atomic unsupported",
			new: func() error {
				_, err := NewValidatedAtomicAdapterWithOptions(&noAtomicClient{})
				return err
			},
			want: cache.ErrCapabilityUnsupported,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.new()
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAdapterCopiesValuesOnSetAndGet(t *testing.T) {
	client := &stubClient{data: make(map[string][]byte)}
	adapter := NewAdapter(client, nil)

	original := []byte("value")
	if err := adapter.Set(t.Context(), "key", original, time.Minute); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	original[0] = 'X'
	if string(client.data["key"]) != "value" {
		t.Fatalf("stored value = %q, want value", client.data["key"])
	}

	got, err := adapter.Get(t.Context(), "key")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	got[0] = 'Y'
	if string(client.data["key"]) != "value" {
		t.Fatalf("client value after Get mutation = %q, want value", client.data["key"])
	}
}

func TestAdapterCopiesValuesOnAppend(t *testing.T) {
	client := &aliasAppendClient{noAtomicClient: noAtomicClient{data: make(map[string][]byte)}}
	adapter, err := NewAppenderAdapter(client, nil)
	if err != nil {
		t.Fatalf("NewAppenderAdapter: %v", err)
	}

	data := []byte("value")
	if err := adapter.Append(t.Context(), "key", data); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	data[0] = 'X'
	if string(client.data["key"]) != "value" {
		t.Fatalf("appended value = %q, want value", client.data["key"])
	}
}

func TestAdapterCapabilities(t *testing.T) {
	prefixAdapter := NewAdapterWithOptions(&stubPrefixFlusher{}, WithClearPrefix("app:"))
	prefixCaps := prefixAdapter.Capabilities()
	if !prefixCaps.Atomic || !prefixCaps.Append || !prefixCaps.Clear || !prefixCaps.PrefixClear {
		t.Fatalf("prefix capabilities = %#v, want atomic append clear prefix clear", prefixCaps)
	}
	if prefixCaps.FlushDB {
		t.Fatalf("prefix capabilities = %#v, want FlushDB false while prefix clear is selected", prefixCaps)
	}

	flushAdapter := NewAdapterWithOptions(&noAtomicFlusher{}, WithAllowFlushDB(true))
	flushCaps := flushAdapter.Capabilities()
	if !flushCaps.Clear || !flushCaps.FlushDB {
		t.Fatalf("flush capabilities = %#v, want clear and FlushDB", flushCaps)
	}
	if flushCaps.Atomic || flushCaps.Append || flushCaps.PrefixClear {
		t.Fatalf("flush capabilities = %#v, want no atomic append prefix clear", flushCaps)
	}

	if caps := (*Adapter)(nil).Capabilities(); caps != (AdapterCapabilities{}) {
		t.Fatalf("nil capabilities = %#v, want zero", caps)
	}
}

func TestAdapterClear(t *testing.T) {
	client := &stubFlusher{stubClient: stubClient{data: map[string][]byte{"k": []byte("v")}}}
	adapter := NewAdapter(client, nil)
	adapter.AllowFlushDB = true

	if err := adapter.Clear(t.Context()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !client.flushed {
		t.Fatalf("expected flush to be called")
	}
}

func TestAdapterClearUsesPrefixWhenConfigured(t *testing.T) {
	client := &stubPrefixFlusher{
		stubFlusher: stubFlusher{
			stubClient: stubClient{data: map[string][]byte{
				"app:key":   []byte("value"),
				"other:key": []byte("value"),
			}},
		},
	}
	adapter := NewAdapterWithOptions(client, WithAllowFlushDB(true), WithClearPrefix("app:"))

	if err := adapter.Clear(t.Context()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.flushed {
		t.Fatal("expected prefix clear to avoid FlushDB")
	}
	if len(client.prefixes) != 1 || client.prefixes[0] != "app:" {
		t.Fatalf("prefixes = %#v, want app:", client.prefixes)
	}
	if _, ok := client.data["app:key"]; ok {
		t.Fatal("expected app:key to be removed")
	}
	if _, ok := client.data["other:key"]; !ok {
		t.Fatal("expected other:key to remain")
	}
}

func TestAdapterClearPrefixUnsupportedDoesNotFlushDB(t *testing.T) {
	client := &stubFlusher{stubClient: stubClient{data: map[string][]byte{"app:key": []byte("value")}}}
	adapter := NewAdapterWithOptions(client, WithAllowFlushDB(true), WithClearPrefix("app:"))

	if err := adapter.Clear(t.Context()); !errors.Is(err, ErrClearUnsupported) {
		t.Fatalf("expected ErrClearUnsupported, got %v", err)
	}
	if client.flushed {
		t.Fatal("expected FlushDB not to be called when prefix clear is unsupported")
	}
}

func TestAdapterClearDisabledByDefault(t *testing.T) {
	adapter := NewAdapter(&stubFlusher{}, nil)

	if err := adapter.Clear(t.Context()); !errors.Is(err, ErrFlushDBDisabled) {
		t.Fatalf("expected ErrFlushDBDisabled, got %v", err)
	}
}

func TestAdapterClearUnsupported(t *testing.T) {
	adapter := NewAdapter(&stubClient{}, nil)
	adapter.AllowFlushDB = true

	if err := adapter.Clear(t.Context()); !errors.Is(err, ErrClearUnsupported) {
		t.Fatalf("expected ErrClearUnsupported, got %v", err)
	}
}

func TestAdapterKeyValidation(t *testing.T) {
	adapter := NewAdapter(&stubClient{data: make(map[string][]byte)}, nil)

	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{
			name:    "empty key",
			key:     "",
			wantErr: true,
		},
		{
			name:    "valid key",
			key:     "valid:key",
			wantErr: false,
		},
		{
			name:    "key with newline",
			key:     "key\nwith\nnewline",
			wantErr: true,
		},
		{
			name:    "key with tab",
			key:     "key\twith\ttab",
			wantErr: true,
		},
		{
			name:    "key with null byte",
			key:     "key\x00null",
			wantErr: true,
		},
		{
			name:    "key with DEL character",
			key:     "key\x7Fdel",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := adapter.Set(t.Context(), tc.key, []byte("value"), 0)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Set() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestAdapterKeyValidationWrapsStableErrors(t *testing.T) {
	adapter := NewAdapter(&stubClient{data: make(map[string][]byte)}, nil)

	err := adapter.Set(t.Context(), "", []byte("value"), 0)
	if !errors.Is(err, cache.ErrInvalidConfig) || !errors.Is(err, cache.ErrInvalidKey) {
		t.Fatalf("empty key error = %v, want ErrInvalidConfig and ErrInvalidKey", err)
	}

	err = adapter.Set(t.Context(), "bad\nkey", []byte("value"), 0)
	if !errors.Is(err, cache.ErrInvalidConfig) || !errors.Is(err, cache.ErrInvalidKey) {
		t.Fatalf("control key error = %v, want ErrInvalidConfig and ErrInvalidKey", err)
	}
}

func TestAdapterKeyTooLong(t *testing.T) {
	adapter := NewAdapter(&stubClient{data: make(map[string][]byte)}, nil)
	adapter.MaxKeyLength = 10

	longKey := "this_is_a_very_long_key"
	err := adapter.Set(t.Context(), longKey, []byte("value"), 0)
	if err == nil {
		t.Fatal("expected error for key too long")
	}
	if !errors.Is(err, cache.ErrKeyTooLong) {
		t.Fatalf("expected ErrKeyTooLong, got %v", err)
	}
}

func TestCounterAdapterIncr(t *testing.T) {
	client := &stubClient{data: make(map[string][]byte)}
	adapter, err := NewCounterAdapter(client, func(err error) bool {
		return errors.Is(err, errMiss)
	})
	if err != nil {
		t.Fatalf("NewCounterAdapter: %v", err)
	}

	// Test increment on non-existent key
	val1, err := adapter.Incr(t.Context(), "counter", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val1 != 5 {
		t.Fatalf("expected 5, got %d", val1)
	}

	// Test increment on existing key
	val2, err := adapter.Incr(t.Context(), "counter", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val2 != 8 {
		t.Fatalf("expected 8, got %d", val2)
	}

	// Test increment by negative number
	val3, err := adapter.Incr(t.Context(), "counter", -2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val3 != 6 {
		t.Fatalf("expected 6, got %d", val3)
	}
}

func TestCounterAdapterDecr(t *testing.T) {
	client := &stubClient{data: make(map[string][]byte)}
	adapter, err := NewCounterAdapter(client, func(err error) bool {
		return errors.Is(err, errMiss)
	})
	if err != nil {
		t.Fatalf("NewCounterAdapter: %v", err)
	}

	// Test decrement on non-existent key
	val1, err := adapter.Decr(t.Context(), "counter", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val1 != -5 {
		t.Fatalf("expected -5, got %d", val1)
	}

	// Test decrement on existing key
	val2, err := adapter.Decr(t.Context(), "counter", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val2 != -8 {
		t.Fatalf("expected -8, got %d", val2)
	}
}

func TestCounterAdapterIncrNonInteger(t *testing.T) {
	client := &stubClient{data: make(map[string][]byte)}
	adapter, err := NewCounterAdapter(client, nil)
	if err != nil {
		t.Fatalf("NewCounterAdapter: %v", err)
	}

	// Set a non-integer value
	err = adapter.Set(t.Context(), "key", []byte("not an integer"), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Try to increment
	_, err = adapter.Incr(t.Context(), "key", 1)
	if !errors.Is(err, cache.ErrNotInteger) {
		t.Fatalf("expected ErrNotInteger, got %v", err)
	}
}

func TestAdapterAtomicUnsupported(t *testing.T) {
	adapter := NewAdapter(&noAtomicClient{data: make(map[string][]byte)}, nil)

	if _, ok := any(adapter).(cache.CounterCache); ok {
		t.Fatal("base Adapter should not implement CounterCache")
	}
	if _, ok := any(adapter).(cache.AppenderCache); ok {
		t.Fatal("base Adapter should not implement AppenderCache")
	}
	if _, err := NewCounterAdapter(adapter.Client, nil); !errors.Is(err, ErrAtomicUnsupported) {
		t.Fatalf("expected ErrAtomicUnsupported from NewCounterAdapter, got %v", err)
	}
	if _, err := NewAppenderAdapter(adapter.Client, nil); !errors.Is(err, ErrAtomicUnsupported) {
		t.Fatalf("expected ErrAtomicUnsupported from NewAppenderAdapter, got %v", err)
	}
}

func TestAppenderAdapterAppend(t *testing.T) {
	client := &stubClient{data: make(map[string][]byte)}
	adapter, err := NewAppenderAdapter(client, func(err error) bool {
		return errors.Is(err, errMiss)
	})
	if err != nil {
		t.Fatalf("NewAppenderAdapter: %v", err)
	}

	// Test append on non-existent key
	err = adapter.Append(t.Context(), "key", []byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, err := adapter.Get(t.Context(), "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(val) != "hello" {
		t.Fatalf("expected 'hello', got %q", val)
	}

	// Test append on existing key
	err = adapter.Append(t.Context(), "key", []byte(" world"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	val, err = adapter.Get(t.Context(), "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(val) != "hello world" {
		t.Fatalf("expected 'hello world', got %q", val)
	}
}

func TestAdapterDelete(t *testing.T) {
	client := &stubClient{data: make(map[string][]byte)}
	adapter := NewAdapter(client, nil)

	// Set a value
	err := adapter.Set(t.Context(), "key", []byte("value"), 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Delete it
	err = adapter.Delete(t.Context(), "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify it's gone
	exists, err := adapter.Exists(t.Context(), "key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists {
		t.Fatal("expected key to be deleted")
	}
}
