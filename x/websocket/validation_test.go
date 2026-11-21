package websocket

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateTextMessage(t *testing.T) {
	cfg := DefaultMessageValidationConfig()

	tests := []struct {
		name    string
		data    []byte
		cfg     MessageValidationConfig
		wantErr bool
	}{
		{
			name:    "Valid message",
			data:    []byte("Hello, World!"),
			cfg:     cfg,
			wantErr: false,
		},
		{
			name:    "Newlines rejected by default",
			data:    []byte("Hello\nWorld\n"),
			cfg:     cfg,
			wantErr: true,
		},
		{
			name:    "Newlines allowed when configured",
			data:    []byte("Hello\nWorld\n"),
			cfg:     MessageValidationConfig{RejectControlCharacters: true, RequireValidUTF8: true, AllowedNewlines: true},
			wantErr: false,
		},
		{
			name:    "Tabs rejected by default",
			data:    []byte("Hello\tWorld"),
			cfg:     cfg,
			wantErr: true,
		},
		{
			name:    "Tabs allowed when configured",
			data:    []byte("Hello\tWorld"),
			cfg:     MessageValidationConfig{RejectControlCharacters: true, RequireValidUTF8: true, AllowedTabs: true},
			wantErr: false,
		},
		{
			name:    "Null byte rejected",
			data:    []byte("Hello\x00World"),
			cfg:     cfg,
			wantErr: true,
		},
		{
			name:    "Bell character rejected",
			data:    []byte("Hello\x07World"),
			cfg:     cfg,
			wantErr: true,
		},
		{
			name:    "Escape sequence rejected",
			data:    []byte("Hello\x1b[31mWorld"),
			cfg:     cfg,
			wantErr: true,
		},
		{
			name:    "DEL character rejected",
			data:    []byte("Hello\x7FWorld"),
			cfg:     cfg,
			wantErr: true,
		},
		{
			name:    "Carriage return rejected when configured",
			data:    []byte("Hello\rWorld"),
			cfg:     MessageValidationConfig{RejectControlCharacters: true, AllowedNewlines: false},
			wantErr: true,
		},
		{
			name:    "Empty message rejected when not allowed",
			data:    []byte(""),
			cfg:     MessageValidationConfig{AllowEmpty: false},
			wantErr: true,
		},
		{
			name:    "Empty message allowed when configured",
			data:    []byte(""),
			cfg:     MessageValidationConfig{AllowEmpty: true},
			wantErr: false,
		},
		{
			name:    "Message too long",
			data:    []byte(strings.Repeat("a", 1001)),
			cfg:     MessageValidationConfig{MaxLength: 1000},
			wantErr: true,
		},
		{
			name:    "Invalid UTF-8 rejected",
			data:    []byte{0xFF, 0xFE, 0xFD},
			cfg:     MessageValidationConfig{RequireValidUTF8: true},
			wantErr: true,
		},
		{
			name:    "Valid UTF-8 accepted",
			data:    []byte("Hello 世界 🌍"),
			cfg:     cfg,
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateTextMessage(tc.data, tc.cfg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateTextMessage() error = %v, wantErr %v", err, tc.wantErr)
			}

			if tc.wantErr && err != nil {
				// Verify we get specific error types
				if !errors.Is(err, ErrInvalidUTF8) &&
					!errors.Is(err, ErrControlCharacters) &&
					!errors.Is(err, ErrMessageTooLong) &&
					!errors.Is(err, ErrEmptyMessage) {
					t.Fatalf("expected validation error, got %v", err)
				}
			}
		})
	}
}

func TestSanitizeForLogging(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		maxLen  int
		want    string
		wantLen int
	}{
		{
			name:    "Normal text",
			data:    []byte("Hello, World!"),
			maxLen:  100,
			want:    "Hello, World!",
			wantLen: 13,
		},
		{
			name:    "With control characters",
			data:    []byte("Hello\x00\x07World"),
			maxLen:  100,
			want:    "Hello  World",
			wantLen: 12,
		},
		{
			name:    "Truncated message",
			data:    []byte(strings.Repeat("a", 100)),
			maxLen:  10,
			want:    strings.Repeat("a", 10) + "...",
			wantLen: 13,
		},
		{
			name:    "With ANSI escape",
			data:    []byte("Hello\x1b[31mWorld"),
			maxLen:  100,
			want:    "Hello [31mWorld",
			wantLen: 15,
		},
		{
			name:    "Invalid UTF-8 replaced",
			data:    []byte{0x48, 0x65, 0xFF, 0xFE, 0x6C, 0x6C, 0x6F}, // "He" + invalid + "llo"
			maxLen:  100,
			want:    "He�llo",
			wantLen: 8,
		},
		{
			name:    "Replaces newlines",
			data:    []byte("Hello\nWorld"),
			maxLen:  100,
			want:    "Hello World",
			wantLen: 11,
		},
		{
			name:    "Replaces tabs",
			data:    []byte("Hello\tWorld"),
			maxLen:  100,
			want:    "Hello World",
			wantLen: 11,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeForLogging(tc.data, tc.maxLen)
			if got != tc.want {
				t.Fatalf("SanitizeForLogging() = %q, want %q", got, tc.want)
			}
			if len(got) != tc.wantLen {
				t.Fatalf("SanitizeForLogging() length = %d, want %d", len(got), tc.wantLen)
			}
		})
	}
}

func TestDefaultMessageValidationConfig(t *testing.T) {
	cfg := DefaultMessageValidationConfig()

	if cfg.MaxLength <= 0 {
		t.Fatal("default should have max length")
	}
	if cfg.AllowEmpty {
		t.Fatal("default should not allow empty")
	}
	if !cfg.RejectControlCharacters {
		t.Fatal("default should reject control characters")
	}
	if !cfg.RequireValidUTF8 {
		t.Fatal("default should require valid UTF-8")
	}
	if cfg.AllowedNewlines {
		t.Fatal("default should reject newlines")
	}
	if cfg.AllowedTabs {
		t.Fatal("default should reject tabs")
	}
}

func TestValidateRoomName(t *testing.T) {
	tests := []struct {
		name string
		room string
		ok   bool
	}{
		{name: "default", room: "default", ok: true},
		{name: "tenant scoped", room: "tenant:chat.main_1", ok: true},
		{name: "empty", room: "", ok: false},
		{name: "slash", room: "bad/room", ok: false},
		{name: "space", room: "bad room", ok: false},
		{name: "newline", room: "bad\nroom", ok: false},
		{name: "too long", room: strings.Repeat("a", MaxRoomNameLength+1), ok: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRoomName(tc.room)
			if tc.ok && err != nil {
				t.Fatalf("ValidateRoomName() error = %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrInvalidRoomName) {
				t.Fatalf("ValidateRoomName() error = %v, want ErrInvalidRoomName", err)
			}
		})
	}
}

func TestValidateTextMessage_AllControlCharacters(t *testing.T) {
	cfg := MessageValidationConfig{
		RejectControlCharacters: true,
		AllowedNewlines:         false,
		AllowedTabs:             false,
	}

	// Test all ASCII control characters (0x00-0x1F and 0x7F)
	for i := 0; i < 32; i++ {
		data := []byte{byte(i)}
		err := ValidateTextMessage(data, cfg)
		if err == nil {
			t.Fatalf("expected error for control character 0x%02X", i)
		}
		if !errors.Is(err, ErrControlCharacters) {
			t.Fatalf("expected ErrControlCharacters for 0x%02X, got %v", i, err)
		}
	}

	// Test DEL (0x7F)
	data := []byte{0x7F}
	err := ValidateTextMessage(data, cfg)
	if err == nil {
		t.Fatal("expected error for DEL character")
	}
	if !errors.Is(err, ErrControlCharacters) {
		t.Fatalf("expected ErrControlCharacters for DEL, got %v", err)
	}
}

func TestNewValidationErrorReportsFieldAndReason(t *testing.T) {
	err := NewValidationError("room", "name is missing")
	if got, want := err.Error(), "websocket: validation error on room: name is missing"; got != want {
		t.Errorf("error=%q, want %q", got, want)
	}
}
