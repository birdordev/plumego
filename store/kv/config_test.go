package kvstore

import "testing"

func TestDefaultConfig(t *testing.T) {
	dataDir := "/tmp/test-kv"
	cfg := DefaultConfig(dataDir)

	if cfg.DataDir != dataDir {
		t.Errorf("DefaultConfig() DataDir = %q, want %q", cfg.DataDir, dataDir)
	}
	if cfg.MaxEntries != 100000 {
		t.Errorf("DefaultConfig() MaxEntries = %d, want 100000", cfg.MaxEntries)
	}
	if cfg.MaxMemoryMB != 200 {
		t.Errorf("DefaultConfig() MaxMemoryMB = %d, want 200", cfg.MaxMemoryMB)
	}
}

func TestDefaultConfigTrimsWhitespace(t *testing.T) {
	cfg := DefaultConfig("  /tmp/test  ")
	if cfg.DataDir != "/tmp/test" {
		t.Errorf("DefaultConfig() should trim whitespace, got DataDir = %q", cfg.DataDir)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name:    "valid config",
			cfg:     Config{DataDir: "/tmp/test", MaxEntries: 100, MaxMemoryMB: 10},
			wantErr: false,
		},
		{
			name:    "empty DataDir",
			cfg:     Config{DataDir: "", MaxEntries: 100, MaxMemoryMB: 10},
			wantErr: true,
		},
		{
			name:    "whitespace-only DataDir",
			cfg:     Config{DataDir: "   ", MaxEntries: 100, MaxMemoryMB: 10},
			wantErr: true,
		},
		{
			name:    "zero MaxEntries",
			cfg:     Config{DataDir: "/tmp/test", MaxEntries: 0, MaxMemoryMB: 10},
			wantErr: true,
		},
		{
			name:    "negative MaxEntries",
			cfg:     Config{DataDir: "/tmp/test", MaxEntries: -1, MaxMemoryMB: 10},
			wantErr: true,
		},
		{
			name:    "zero MaxMemoryMB",
			cfg:     Config{DataDir: "/tmp/test", MaxEntries: 100, MaxMemoryMB: 0},
			wantErr: true,
		},
		{
			name:    "negative MaxMemoryMB",
			cfg:     Config{DataDir: "/tmp/test", MaxEntries: 100, MaxMemoryMB: -1},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSetConfigDefaults(t *testing.T) {
	cfg := Config{
		DataDir: "  /tmp/test  ",
	}
	setConfigDefaults(&cfg)

	if cfg.DataDir != "/tmp/test" {
		t.Errorf("setConfigDefaults() should trim DataDir, got %q", cfg.DataDir)
	}
	if cfg.MaxEntries != 100000 {
		t.Errorf("setConfigDefaults() should set default MaxEntries, got %d", cfg.MaxEntries)
	}
	if cfg.MaxMemoryMB != 200 {
		t.Errorf("setConfigDefaults() should set default MaxMemoryMB, got %d", cfg.MaxMemoryMB)
	}

	// Test that non-zero values are preserved
	cfg2 := Config{
		DataDir:     "/tmp/test",
		MaxEntries:  50,
		MaxMemoryMB: 10,
	}
	setConfigDefaults(&cfg2)

	if cfg2.MaxEntries != 50 {
		t.Errorf("setConfigDefaults() should preserve non-zero MaxEntries, got %d", cfg2.MaxEntries)
	}
	if cfg2.MaxMemoryMB != 10 {
		t.Errorf("setConfigDefaults() should preserve non-zero MaxMemoryMB, got %d", cfg2.MaxMemoryMB)
	}
}

func TestOptionsBackwardCompatibility(t *testing.T) {
	// Options is now an alias for Config
	opts := Options{
		DataDir:     "/tmp/test",
		MaxEntries:  100,
		MaxMemoryMB: 10,
	}

	// The explicit Config declaration is the point of this test: a value of
	// the deprecated Options alias must assign into a Config-typed variable.
	//nolint:staticcheck // S1021: the standalone typed declaration is the assertion under test
	var cfg Config
	cfg = opts

	if cfg.DataDir != opts.DataDir {
		t.Error("Options and Config should be interchangeable")
	}
}

func TestDefaultOptionsBackwardCompatibility(t *testing.T) {
	dataDir := "/tmp/test"
	opts := DefaultOptions(dataDir)
	cfg := DefaultConfig(dataDir)

	if opts.DataDir != cfg.DataDir {
		t.Error("DefaultOptions should return same result as DefaultConfig")
	}
	if opts.MaxEntries != cfg.MaxEntries {
		t.Error("DefaultOptions should return same result as DefaultConfig")
	}
	if opts.MaxMemoryMB != cfg.MaxMemoryMB {
		t.Error("DefaultOptions should return same result as DefaultConfig")
	}
}
