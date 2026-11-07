package multimodal

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectImageFormat(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    ImageFormat
		wantErr bool
	}{
		{
			name:    "detect PNG",
			data:    []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D},
			want:    ImageFormatPNG,
			wantErr: false,
		},
		{
			name:    "detect JPEG",
			data:    []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01},
			want:    ImageFormatJPEG,
			wantErr: false,
		},
		{
			name:    "detect WebP",
			data:    []byte("RIFF\x00\x00\x00\x00WEBP"),
			want:    ImageFormatWebP,
			wantErr: false,
		},
		{
			name:    "detect GIF",
			data:    []byte("GIF89a\x00\x00\x00\x00\x00\x00"),
			want:    ImageFormatGIF,
			wantErr: false,
		},
		{
			name:    "data too short",
			data:    []byte{0x89, 0x50},
			want:    "",
			wantErr: true,
		},
		{
			name:    "unknown format",
			data:    []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DetectImageFormat(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("DetectImageFormat() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("DetectImageFormat() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetectAudioFormat(t *testing.T) {
	tests := []struct {
		name string
		path string
		want AudioFormat
	}{
		{name: "mp3 file", path: "audio.mp3", want: AudioFormatMP3},
		{name: "wav file", path: "audio.wav", want: AudioFormatWAV},
		{name: "ogg file", path: "audio.ogg", want: AudioFormatOGG},
		{name: "flac file", path: "audio.flac", want: AudioFormatFLAC},
		{name: "m4a file", path: "audio.m4a", want: AudioFormatM4A},
		{name: "uppercase MP3", path: "audio.MP3", want: AudioFormatMP3},
		{name: "unknown format", path: "audio.xyz", want: AudioFormatMP3}, // defaults to MP3
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectAudioFormat(tt.path)
			if got != tt.want {
				t.Errorf("DetectAudioFormat() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetectDocumentFormat(t *testing.T) {
	tests := []struct {
		name string
		path string
		want DocumentFormat
	}{
		{name: "pdf file", path: "doc.pdf", want: DocumentFormatPDF},
		{name: "docx file", path: "doc.docx", want: DocumentFormatDOCX},
		{name: "txt file", path: "doc.txt", want: DocumentFormatTXT},
		{name: "html file", path: "doc.html", want: DocumentFormatHTML},
		{name: "htm file", path: "doc.htm", want: DocumentFormatHTML},
		{name: "md file", path: "doc.md", want: DocumentFormatMD},
		{name: "markdown file", path: "doc.markdown", want: DocumentFormatMD},
		{name: "csv file", path: "doc.csv", want: DocumentFormatCSV},
		{name: "json file", path: "doc.json", want: DocumentFormatJSON},
		{name: "xml file", path: "doc.xml", want: DocumentFormatXML},
		{name: "uppercase PDF", path: "doc.PDF", want: DocumentFormatPDF},
		{name: "unknown format", path: "doc.xyz", want: DocumentFormatTXT}, // defaults to TXT
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectDocumentFormat(tt.path)
			if got != tt.want {
				t.Errorf("DetectDocumentFormat() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetMimeType(t *testing.T) {
	tests := []struct {
		name   string
		format string
		want   string
	}{
		{name: "jpeg", format: "jpeg", want: "image/jpeg"},
		{name: "jpg", format: "jpg", want: "image/jpeg"},
		{name: "png", format: "png", want: "image/png"},
		{name: "webp", format: "webp", want: "image/webp"},
		{name: "gif", format: "gif", want: "image/gif"},
		{name: "mp3", format: "mp3", want: "audio/mpeg"},
		{name: "wav", format: "wav", want: "audio/vnd.wave"}, // Standard library returns official IANA MIME type
		{name: "ogg", format: "ogg", want: "audio/ogg"},
		{name: "mp4", format: "mp4", want: "video/mp4"},
		{name: "webm", format: "webm", want: "video/webm"},
		{name: "pdf", format: "pdf", want: "application/pdf"},
		{name: "docx", format: "docx", want: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{name: "unknown", format: "xyz", want: "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetMimeType(tt.format)
			if got != tt.want {
				t.Errorf("GetMimeType() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateSize(t *testing.T) {
	tests := []struct {
		name    string
		size    int64
		maxSize int64
		wantErr bool
	}{
		{name: "within limit", size: 100, maxSize: 1000, wantErr: false},
		{name: "at limit", size: 1000, maxSize: 1000, wantErr: false},
		{name: "exceeds limit", size: 1001, maxSize: 1000, wantErr: true},
		{name: "no limit", size: 1000000, maxSize: 0, wantErr: false},
		{name: "negative max (treated as no limit)", size: 100, maxSize: -1, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSize(tt.size, tt.maxSize)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSize() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEncodeDecodeBase64(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "empty data", data: []byte{}},
		{name: "simple text", data: []byte("hello world")},
		{name: "binary data", data: []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0xFD}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := EncodeBase64(tt.data)
			decoded, err := DecodeBase64(encoded)
			if err != nil {
				t.Fatalf("DecodeBase64() error = %v", err)
			}
			if string(decoded) != string(tt.data) {
				t.Errorf("DecodeBase64() = %v, want %v", decoded, tt.data)
			}
		})
	}
}

func TestDecodeBase64_Invalid(t *testing.T) {
	_, err := DecodeBase64("not-valid-base64!")
	if err == nil {
		t.Error("DecodeBase64() expected error for invalid input")
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name    string
		seconds float64
		want    string
	}{
		{name: "zero", seconds: 0, want: "0:00"},
		{name: "seconds only", seconds: 45, want: "0:45"},
		{name: "minutes and seconds", seconds: 125, want: "2:05"},
		{name: "hours", seconds: 3665, want: "1:01:05"},
		{name: "multiple hours", seconds: 7384, want: "2:03:04"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDuration(tt.seconds)
			if got != tt.want {
				t.Errorf("FormatDuration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatFileSize(t *testing.T) {
	tests := []struct {
		name  string
		bytes int64
		want  string
	}{
		{name: "bytes", bytes: 512, want: "512 B"},
		{name: "kilobytes", bytes: 1536, want: "1.5 KB"},
		{name: "megabytes", bytes: 1048576, want: "1.0 MB"},
		{name: "gigabytes", bytes: 1073741824, want: "1.0 GB"},
		{name: "terabytes", bytes: 1099511627776, want: "1.0 TB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatFileSize(tt.bytes)
			if got != tt.want {
				t.Errorf("FormatFileSize() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContentBlocksToText(t *testing.T) {
	blocks := []ContentBlock{
		{Type: ContentTypeText, Text: "Hello"},
		{Type: ContentTypeImage, Image: &ImageContent{Format: ImageFormatPNG}},
		{Type: ContentTypeText, Text: "world"},
		{Type: ContentTypeAudio, Audio: &AudioContent{Format: AudioFormatMP3}},
		{Type: ContentTypeText, Text: "!"},
	}

	got := ContentBlocksToText(blocks)
	want := "Hello world !"
	if got != want {
		t.Errorf("ContentBlocksToText() = %q, want %q", got, want)
	}
}

func TestCountTokensApprox(t *testing.T) {
	blocks := []ContentBlock{
		{Type: ContentTypeText, Text: "This is a test message"},                              // ~6 tokens (24 chars / 4)
		{Type: ContentTypeImage, Image: &ImageContent{Format: ImageFormatPNG}},               // ~170 tokens
		{Type: ContentTypeAudio, Audio: &AudioContent{Format: AudioFormatMP3, Duration: 30}}, // ~30 tokens
	}

	tokens := CountTokensApprox(blocks)
	// Expected: 6 + 170 + 30 = 206
	if tokens < 200 || tokens > 210 {
		t.Errorf("CountTokensApprox() = %d, want ~206", tokens)
	}
}

func TestIsValidURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "http URL", url: "http://example.com", want: true},
		{name: "https URL", url: "https://example.com", want: true},
		{name: "no protocol", url: "example.com", want: false},
		{name: "ftp URL", url: "ftp://example.com", want: false},
		{name: "empty", url: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidURL(tt.url)
			if got != tt.want {
				t.Errorf("IsValidURL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "clean filename", input: "document.pdf", want: "document.pdf"},
		{name: "with spaces", input: "my document.pdf", want: "my document.pdf"},
		{name: "with slashes", input: "path/to/file.pdf", want: "path_to_file.pdf"},
		{name: "with backslashes", input: "path\\to\\file.pdf", want: "path_to_file.pdf"},
		{name: "with special chars", input: "file:name*?.pdf", want: "file_name__.pdf"},
		{name: "with quotes", input: "\"file\".pdf", want: "_file_.pdf"},
		{name: "with angle brackets", input: "<file>.pdf", want: "_file_.pdf"},
		{name: "with pipe", input: "file|name.pdf", want: "file_name.pdf"},
		{name: "leading spaces", input: "  file.pdf", want: "file.pdf"},
		{name: "trailing spaces", input: "file.pdf  ", want: "file.pdf"},
		{name: "leading dots", input: "..file.pdf", want: "file.pdf"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeFilename(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeFilename() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSanitizeFilename_LongName(t *testing.T) {
	// Create a name longer than 255 characters
	longName := ""
	for i := 0; i < 300; i++ {
		longName += "a"
	}

	result := SanitizeFilename(longName)
	if len(result) > 255 {
		t.Errorf("SanitizeFilename() length = %d, want <= 255", len(result))
	}
}

func TestLoadImageFromFile(t *testing.T) {
	// Create a temporary PNG file
	tmpDir := t.TempDir()
	pngPath := filepath.Join(tmpDir, "test.png")

	// PNG signature
	pngData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}
	if err := os.WriteFile(pngPath, pngData, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	img, err := LoadImageFromFile(pngPath)
	if err != nil {
		t.Fatalf("LoadImageFromFile() error = %v", err)
	}

	if img.Format != ImageFormatPNG {
		t.Errorf("LoadImageFromFile() format = %v, want %v", img.Format, ImageFormatPNG)
	}

	if img.Source != ImageSourceBase64 {
		t.Errorf("LoadImageFromFile() source = %v, want %v", img.Source, ImageSourceBase64)
	}

	if img.SizeBytes != int64(len(pngData)) {
		t.Errorf("LoadImageFromFile() size = %d, want %d", img.SizeBytes, len(pngData))
	}
}

func TestLoadImageFromFile_NotFound(t *testing.T) {
	_, err := LoadImageFromFile("/nonexistent/file.png")
	if err == nil {
		t.Error("LoadImageFromFile() expected error for nonexistent file")
	}
}

func TestLoadImageFromURL(t *testing.T) {
	pngData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngData)
	}))
	defer srv.Close()

	img, err := LoadImageFromURL(srv.URL)
	if err != nil {
		t.Fatalf("LoadImageFromURL() error = %v", err)
	}
	if img.Format != ImageFormatPNG {
		t.Errorf("LoadImageFromURL() format = %v, want %v", img.Format, ImageFormatPNG)
	}
	if img.SizeBytes != int64(len(pngData)) {
		t.Errorf("LoadImageFromURL() size = %d, want %d", img.SizeBytes, len(pngData))
	}
}

func TestLoadImageFromURL_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := LoadImageFromURL(srv.URL)
	if err == nil {
		t.Fatal("LoadImageFromURL() expected error for non-200 status")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("LoadImageFromURL() error = %v, want it to mention status 404", err)
	}
}

func TestLoadImageFromURL_TooLarge(t *testing.T) {
	// Serve one byte past the cap; the loader must fail with ErrImageTooLarge
	// instead of silently truncating.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		chunk := make([]byte, 1<<20)
		for i := int64(0); i <= MaxRemoteImageBytes/int64(len(chunk)); i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	_, err := LoadImageFromURL(srv.URL)
	if !errors.Is(err, ErrImageTooLarge) {
		t.Fatalf("LoadImageFromURL() error = %v, want ErrImageTooLarge", err)
	}
}

func TestReadBoundedBody(t *testing.T) {
	atLimit, err := readBoundedBody(strings.NewReader(strings.Repeat("a", 8)), 8)
	if err != nil {
		t.Fatalf("readBoundedBody() at limit error = %v", err)
	}
	if len(atLimit) != 8 {
		t.Errorf("readBoundedBody() at limit len = %d, want 8", len(atLimit))
	}

	_, err = readBoundedBody(strings.NewReader(strings.Repeat("a", 9)), 8)
	if !errors.Is(err, ErrImageTooLarge) {
		t.Errorf("readBoundedBody() over limit error = %v, want ErrImageTooLarge", err)
	}

	_, err = readBoundedBody(failingReader{}, 8)
	if err == nil || errors.Is(err, ErrImageTooLarge) {
		t.Errorf("readBoundedBody() read failure error = %v, want wrapped read error", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, fmt.Errorf("simulated read failure") }

func TestLoadAudioFromFile(t *testing.T) {
	// Create a temporary MP3 file
	tmpDir := t.TempDir()
	mp3Path := filepath.Join(tmpDir, "test.mp3")

	mp3Data := []byte("fake mp3 data")
	if err := os.WriteFile(mp3Path, mp3Data, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	audio, err := LoadAudioFromFile(mp3Path)
	if err != nil {
		t.Fatalf("LoadAudioFromFile() error = %v", err)
	}

	if audio.Format != AudioFormatMP3 {
		t.Errorf("LoadAudioFromFile() format = %v, want %v", audio.Format, AudioFormatMP3)
	}

	if audio.SizeBytes != int64(len(mp3Data)) {
		t.Errorf("LoadAudioFromFile() size = %d, want %d", audio.SizeBytes, len(mp3Data))
	}
}

func TestLoadDocumentFromFile(t *testing.T) {
	// Create a temporary PDF file
	tmpDir := t.TempDir()
	pdfPath := filepath.Join(tmpDir, "test.pdf")

	pdfData := []byte("fake pdf data")
	if err := os.WriteFile(pdfPath, pdfData, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	doc, err := LoadDocumentFromFile(pdfPath)
	if err != nil {
		t.Fatalf("LoadDocumentFromFile() error = %v", err)
	}

	if doc.Format != DocumentFormatPDF {
		t.Errorf("LoadDocumentFromFile() format = %v, want %v", doc.Format, DocumentFormatPDF)
	}

	if doc.SizeBytes != int64(len(pdfData)) {
		t.Errorf("LoadDocumentFromFile() size = %d, want %d", doc.SizeBytes, len(pdfData))
	}
}
