package multimodal

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrImageTooLarge is returned by LoadImageFromURL when the remote image body
// exceeds MaxRemoteImageBytes, so oversized downloads fail loudly instead of
// being silently truncated.
var ErrImageTooLarge = errors.New("multimodal: remote image exceeds size limit")

// MaxRemoteImageBytes bounds the body LoadImageFromURL reads into memory.
const MaxRemoteImageBytes int64 = 20 << 20 // 20 MiB

// remoteImageHTTPClient is used by LoadImageFromURL. It carries an explicit
// total timeout so a stalled peer cannot block the caller indefinitely.
var remoteImageHTTPClient = &http.Client{Timeout: 30 * time.Second}

// LoadImageFromFile loads an image from a file.
func LoadImageFromFile(path string) (*ImageContent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read image file: %w", err)
	}

	format, err := DetectImageFormat(data)
	if err != nil {
		return nil, fmt.Errorf("failed to detect image format: %w", err)
	}

	return &ImageContent{
		Format:    format,
		Source:    ImageSourceBase64,
		Data:      data,
		SizeBytes: int64(len(data)),
	}, nil
}

// LoadImageFromURL loads an image from a URL. The fetch uses a client with a
// 30s total timeout, and the body is bounded by MaxRemoteImageBytes; larger
// images fail with ErrImageTooLarge. Callers that need custom timeouts,
// redirects, or authentication should fetch the bytes themselves and use
// DetectImageFormat.
func LoadImageFromURL(url string) (*ImageContent, error) {
	resp, err := remoteImageHTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	data, err := readBoundedBody(resp.Body, MaxRemoteImageBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to read image data: %w", err)
	}

	format, err := DetectImageFormat(data)
	if err != nil {
		return nil, fmt.Errorf("failed to detect image format: %w", err)
	}

	return &ImageContent{
		Format:    format,
		Source:    ImageSourceBase64,
		Data:      data,
		SizeBytes: int64(len(data)),
	}, nil
}

// readBoundedBody reads at most max bytes from r and fails with
// ErrImageTooLarge when the source is larger, so truncation is never silent.
// It reads one byte past the limit so overflow is detectable.
func readBoundedBody(r io.Reader, max int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, ErrImageTooLarge
	}
	return body, nil
}

// DetectImageFormat detects the image format from file data.
func DetectImageFormat(data []byte) (ImageFormat, error) {
	if len(data) < 12 {
		return "", fmt.Errorf("data too short to detect format")
	}

	// Check PNG
	if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return ImageFormatPNG, nil
	}

	// Check JPEG
	if data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return ImageFormatJPEG, nil
	}

	// Check WebP
	if string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return ImageFormatWebP, nil
	}

	// Check GIF
	if string(data[0:3]) == "GIF" {
		return ImageFormatGIF, nil
	}

	return "", fmt.Errorf("unknown image format")
}

// LoadAudioFromFile loads audio from a file.
func LoadAudioFromFile(path string) (*AudioContent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read audio file: %w", err)
	}

	format := DetectAudioFormat(path)

	return &AudioContent{
		Format:    format,
		Data:      data,
		SizeBytes: int64(len(data)),
	}, nil
}

// DetectAudioFormat detects audio format from file extension.
func DetectAudioFormat(path string) AudioFormat {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".mp3":
		return AudioFormatMP3
	case ".wav":
		return AudioFormatWAV
	case ".ogg":
		return AudioFormatOGG
	case ".flac":
		return AudioFormatFLAC
	case ".m4a":
		return AudioFormatM4A
	default:
		return AudioFormatMP3 // Default
	}
}

// LoadDocumentFromFile loads a document from a file.
func LoadDocumentFromFile(path string) (*DocumentContent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read document file: %w", err)
	}

	format := DetectDocumentFormat(path)

	return &DocumentContent{
		Format:    format,
		Data:      data,
		SizeBytes: int64(len(data)),
	}, nil
}

// DetectDocumentFormat detects document format from file extension.
func DetectDocumentFormat(path string) DocumentFormat {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pdf":
		return DocumentFormatPDF
	case ".docx":
		return DocumentFormatDOCX
	case ".txt":
		return DocumentFormatTXT
	case ".html", ".htm":
		return DocumentFormatHTML
	case ".md", ".markdown":
		return DocumentFormatMD
	case ".csv":
		return DocumentFormatCSV
	case ".json":
		return DocumentFormatJSON
	case ".xml":
		return DocumentFormatXML
	default:
		return DocumentFormatTXT // Default
	}
}

// GetMimeType returns the MIME type for a format.
func GetMimeType(format string) string {
	switch strings.ToLower(strings.TrimPrefix(format, ".")) {
	case "jpeg", "jpg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	case "mp3":
		return "audio/mpeg"
	case "wav":
		return "audio/vnd.wave"
	case "ogg":
		return "audio/ogg"
	case "mp4":
		return "video/mp4"
	case "webm":
		return "video/webm"
	case "pdf":
		return "application/pdf"
	case "docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	default:
		return "application/octet-stream"
	}
}

// ValidateSize validates content size against limits.
func ValidateSize(size int64, maxSize int64) error {
	if maxSize > 0 && size > maxSize {
		return fmt.Errorf("content size %d exceeds maximum %d", size, maxSize)
	}
	return nil
}

// EncodeBase64 encodes data to base64 string.
func EncodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// DecodeBase64 decodes base64 string to data.
func DecodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

// FormatDuration formats duration in seconds to human-readable string.
func FormatDuration(seconds float64) string {
	hours := int(seconds / 3600)
	minutes := int((seconds - float64(hours*3600)) / 60)
	secs := int(seconds) % 60

	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, secs)
	}
	return fmt.Sprintf("%d:%02d", minutes, secs)
}

// FormatFileSize formats file size in bytes to human-readable string.
func FormatFileSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// ContentBlocksToText extracts all text from content blocks.
func ContentBlocksToText(blocks []ContentBlock) string {
	var text strings.Builder
	for _, block := range blocks {
		if block.Type == ContentTypeText {
			text.WriteString(block.Text)
			text.WriteString(" ")
		}
	}
	return strings.TrimSpace(text.String())
}

// CountTokensApprox provides a rough token count estimate for multimodal content.
func CountTokensApprox(blocks []ContentBlock) int {
	tokens := 0
	for _, block := range blocks {
		switch block.Type {
		case ContentTypeText:
			// ~4 characters per token
			tokens += len(block.Text) / 4
		case ContentTypeImage:
			// Images typically use 85-170 tokens per tile
			tokens += 170
		case ContentTypeAudio:
			// ~1 token per second of audio
			if block.Audio != nil {
				tokens += int(block.Audio.Duration)
			}
		case ContentTypeVideo:
			// ~170 tokens per frame analyzed
			if block.Video != nil {
				tokens += int(block.Video.Duration) * 2 // Rough estimate
			}
		case ContentTypeDocument:
			// Similar to text
			if block.Document != nil && block.Document.Text != "" {
				tokens += len(block.Document.Text) / 4
			}
		}
	}
	return tokens
}

// IsValidURL checks if a string is a valid URL.
func IsValidURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// SanitizeFilename sanitizes a filename to be safe for file systems.
func SanitizeFilename(name string) string {
	// Replace unsafe characters
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, ":", "_")
	name = strings.ReplaceAll(name, "*", "_")
	name = strings.ReplaceAll(name, "?", "_")
	name = strings.ReplaceAll(name, "\"", "_")
	name = strings.ReplaceAll(name, "<", "_")
	name = strings.ReplaceAll(name, ">", "_")
	name = strings.ReplaceAll(name, "|", "_")

	// Trim spaces and dots
	name = strings.TrimSpace(name)
	name = strings.Trim(name, ".")

	// Limit length
	if len(name) > 255 {
		name = name[:255]
	}

	return name
}
