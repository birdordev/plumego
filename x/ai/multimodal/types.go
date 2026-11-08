// Package multimodal provides multimodal content support for AI providers.
package multimodal

import (
	"encoding/base64"
	"fmt"
	"time"

	"github.com/birdordev/plumego/x/ai/provider"
)

// ContentType represents the type of content.
type ContentType string

const (
	ContentTypeText       ContentType = "text"
	ContentTypeImage      ContentType = "image"
	ContentTypeAudio      ContentType = "audio"
	ContentTypeVideo      ContentType = "video"
	ContentTypeDocument   ContentType = "document"
	ContentTypeToolUse    ContentType = "tool_use"
	ContentTypeToolResult ContentType = "tool_result"
)

// ContentBlock represents a block of content in a message.
type ContentBlock struct {
	Type       ContentType      `json:"type"`
	Text       string           `json:"text,omitempty"`
	Image      *ImageContent    `json:"image,omitempty"`
	Audio      *AudioContent    `json:"audio,omitempty"`
	Video      *VideoContent    `json:"video,omitempty"`
	Document   *DocumentContent `json:"document,omitempty"`
	ToolUse    *ToolUse         `json:"tool_use,omitempty"`
	ToolResult *ToolResult      `json:"tool_result,omitempty"`
}

// ImageFormat represents image format.
type ImageFormat string

const (
	ImageFormatJPEG ImageFormat = "jpeg"
	ImageFormatPNG  ImageFormat = "png"
	ImageFormatWebP ImageFormat = "webp"
	ImageFormatGIF  ImageFormat = "gif"
)

// ImageSource indicates how the image is provided.
type ImageSource string

const (
	ImageSourceBase64 ImageSource = "base64"
	ImageSourceURL    ImageSource = "url"
)

// ImageContent represents image content.
type ImageContent struct {
	Format    ImageFormat `json:"format"`
	Source    ImageSource `json:"source"`
	Data      []byte      `json:"data,omitempty"` // Base64 encoded in JSON
	URL       string      `json:"url,omitempty"`
	Width     int         `json:"width,omitempty"`
	Height    int         `json:"height,omitempty"`
	MimeType  string      `json:"mime_type,omitempty"`
	SizeBytes int64       `json:"size_bytes,omitempty"`
}

// Base64String returns the base64-encoded string representation.
func (i *ImageContent) Base64String() string {
	if i.Source == ImageSourceBase64 && len(i.Data) > 0 {
		return base64.StdEncoding.EncodeToString(i.Data)
	}
	return ""
}

// AudioFormat represents audio format.
type AudioFormat string

const (
	AudioFormatMP3  AudioFormat = "mp3"
	AudioFormatWAV  AudioFormat = "wav"
	AudioFormatOGG  AudioFormat = "ogg"
	AudioFormatFLAC AudioFormat = "flac"
	AudioFormatM4A  AudioFormat = "m4a"
)

// AudioContent represents audio content.
type AudioContent struct {
	Format     AudioFormat `json:"format"`
	Data       []byte      `json:"data,omitempty"`
	URL        string      `json:"url,omitempty"`
	Duration   float64     `json:"duration,omitempty"` // seconds
	Transcript string      `json:"transcript,omitempty"`
	MimeType   string      `json:"mime_type,omitempty"`
	SizeBytes  int64       `json:"size_bytes,omitempty"`
	SampleRate int         `json:"sample_rate,omitempty"` // Hz
	Channels   int         `json:"channels,omitempty"`    // 1=mono, 2=stereo
}

// Base64String returns the base64-encoded string representation.
func (a *AudioContent) Base64String() string {
	if len(a.Data) > 0 {
		return base64.StdEncoding.EncodeToString(a.Data)
	}
	return ""
}

// VideoFormat represents video format.
type VideoFormat string

const (
	VideoFormatMP4  VideoFormat = "mp4"
	VideoFormatWebM VideoFormat = "webm"
	VideoFormatAVI  VideoFormat = "avi"
	VideoFormatMOV  VideoFormat = "mov"
)

// VideoContent represents video content.
type VideoContent struct {
	Format    VideoFormat   `json:"format"`
	Data      []byte        `json:"data,omitempty"`
	URL       string        `json:"url,omitempty"`
	Duration  float64       `json:"duration,omitempty"` // seconds
	Width     int           `json:"width,omitempty"`
	Height    int           `json:"height,omitempty"`
	FrameRate float64       `json:"frame_rate,omitempty"` // fps
	MimeType  string        `json:"mime_type,omitempty"`
	SizeBytes int64         `json:"size_bytes,omitempty"`
	Thumbnail *ImageContent `json:"thumbnail,omitempty"`
}

// Base64String returns the base64-encoded string representation.
func (v *VideoContent) Base64String() string {
	if len(v.Data) > 0 {
		return base64.StdEncoding.EncodeToString(v.Data)
	}
	return ""
}

// DocumentFormat represents document format.
type DocumentFormat string

const (
	DocumentFormatPDF  DocumentFormat = "pdf"
	DocumentFormatDOCX DocumentFormat = "docx"
	DocumentFormatTXT  DocumentFormat = "txt"
	DocumentFormatHTML DocumentFormat = "html"
	DocumentFormatMD   DocumentFormat = "markdown"
	DocumentFormatCSV  DocumentFormat = "csv"
	DocumentFormatJSON DocumentFormat = "json"
	DocumentFormatXML  DocumentFormat = "xml"
)

// DocumentContent represents document content.
type DocumentContent struct {
	Format    DocumentFormat `json:"format"`
	Data      []byte         `json:"data,omitempty"`
	URL       string         `json:"url,omitempty"`
	PageCount int            `json:"page_count,omitempty"`
	Text      string         `json:"text,omitempty"` // Extracted text
	Title     string         `json:"title,omitempty"`
	Author    string         `json:"author,omitempty"`
	MimeType  string         `json:"mime_type,omitempty"`
	SizeBytes int64          `json:"size_bytes,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// Base64String returns the base64-encoded string representation.
func (d *DocumentContent) Base64String() string {
	if len(d.Data) > 0 {
		return base64.StdEncoding.EncodeToString(d.Data)
	}
	return ""
}

// ToolUse represents a tool invocation request.
type ToolUse struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ToolResult represents the result of a tool invocation.
type ToolResult struct {
	ToolUseID string         `json:"tool_use_id"`
	Content   []ContentBlock `json:"content"`
	IsError   bool           `json:"is_error,omitempty"`
}

// Message represents a multimodal message.
type Message struct {
	Role       MessageRole    `json:"role"`
	Content    []ContentBlock `json:"content"`
	Name       string         `json:"name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Timestamp  time.Time      `json:"timestamp,omitempty"`
}

// MessageRole is an alias for provider.Role.
// Use provider.Role directly when possible.
type MessageRole = provider.Role

const (
	RoleSystem    = provider.RoleSystem
	RoleUser      = provider.RoleUser
	RoleAssistant = provider.RoleAssistant
	RoleTool      = provider.RoleTool
)

// NewTextMessage creates a new text-only message.
func NewTextMessage(role MessageRole, text string) *Message {
	return &Message{
		Role: role,
		Content: []ContentBlock{
			{
				Type: ContentTypeText,
				Text: text,
			},
		},
		Timestamp: time.Now(),
	}
}

// NewImageMessage creates a new image message.
func NewImageMessage(role MessageRole, image *ImageContent, caption string) *Message {
	content := []ContentBlock{
		{
			Type:  ContentTypeImage,
			Image: image,
		},
	}

	if caption != "" {
		content = append(content, ContentBlock{
			Type: ContentTypeText,
			Text: caption,
		})
	}

	return &Message{
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
	}
}

// AddText adds a text content block to the message.
func (m *Message) AddText(text string) {
	m.Content = append(m.Content, ContentBlock{
		Type: ContentTypeText,
		Text: text,
	})
}

// AddImage adds an image content block to the message.
func (m *Message) AddImage(image *ImageContent) {
	m.Content = append(m.Content, ContentBlock{
		Type:  ContentTypeImage,
		Image: image,
	})
}

// AddAudio adds an audio content block to the message.
func (m *Message) AddAudio(audio *AudioContent) {
	m.Content = append(m.Content, ContentBlock{
		Type:  ContentTypeAudio,
		Audio: audio,
	})
}

// AddVideo adds a video content block to the message.
func (m *Message) AddVideo(video *VideoContent) {
	m.Content = append(m.Content, ContentBlock{
		Type:  ContentTypeVideo,
		Video: video,
	})
}

// AddDocument adds a document content block to the message.
func (m *Message) AddDocument(document *DocumentContent) {
	m.Content = append(m.Content, ContentBlock{
		Type:     ContentTypeDocument,
		Document: document,
	})
}

// GetText extracts all text content from the message.
func (m *Message) GetText() string {
	var text string
	for _, block := range m.Content {
		if block.Type == ContentTypeText {
			text += block.Text
		}
	}
	return text
}

// HasImages returns true if the message contains any images.
func (m *Message) HasImages() bool {
	for _, block := range m.Content {
		if block.Type == ContentTypeImage {
			return true
		}
	}
	return false
}

// HasAudio returns true if the message contains any audio.
func (m *Message) HasAudio() bool {
	for _, block := range m.Content {
		if block.Type == ContentTypeAudio {
			return true
		}
	}
	return false
}

// HasVideo returns true if the message contains any video.
func (m *Message) HasVideo() bool {
	for _, block := range m.Content {
		if block.Type == ContentTypeVideo {
			return true
		}
	}
	return false
}

// HasDocuments returns true if the message contains any documents.
func (m *Message) HasDocuments() bool {
	for _, block := range m.Content {
		if block.Type == ContentTypeDocument {
			return true
		}
	}
	return false
}

// ContentTypes returns all content types present in the message.
func (m *Message) ContentTypes() []ContentType {
	types := make(map[ContentType]bool)
	for _, block := range m.Content {
		types[block.Type] = true
	}

	result := make([]ContentType, 0, len(types))
	for t := range types {
		result = append(result, t)
	}
	return result
}

// Validate validates the message structure.
func (m *Message) Validate() error {
	if m.Role == "" {
		return fmt.Errorf("message role is required")
	}

	if len(m.Content) == 0 {
		return fmt.Errorf("message must have at least one content block")
	}

	for i, block := range m.Content {
		if err := block.Validate(); err != nil {
			return fmt.Errorf("content block %d: %w", i, err)
		}
	}

	return nil
}

// Validate validates the content block.
func (c *ContentBlock) Validate() error {
	switch c.Type {
	case ContentTypeText:
		if c.Text == "" {
			return fmt.Errorf("text content is empty")
		}
	case ContentTypeImage:
		if c.Image == nil {
			return fmt.Errorf("image content is nil")
		}
		return c.Image.Validate()
	case ContentTypeAudio:
		if c.Audio == nil {
			return fmt.Errorf("audio content is nil")
		}
		return c.Audio.Validate()
	case ContentTypeVideo:
		if c.Video == nil {
			return fmt.Errorf("video content is nil")
		}
		return c.Video.Validate()
	case ContentTypeDocument:
		if c.Document == nil {
			return fmt.Errorf("document content is nil")
		}
		return c.Document.Validate()
	case ContentTypeToolUse:
		if c.ToolUse == nil {
			return fmt.Errorf("tool use is nil")
		}
	case ContentTypeToolResult:
		if c.ToolResult == nil {
			return fmt.Errorf("tool result is nil")
		}
	default:
		return fmt.Errorf("unknown content type: %s", c.Type)
	}

	return nil
}

// Validate validates the image content.
func (i *ImageContent) Validate() error {
	if i.Format == "" {
		return fmt.Errorf("image format is required")
	}

	switch i.Source {
	case ImageSourceBase64:
		if len(i.Data) == 0 {
			return fmt.Errorf("base64 image data is empty")
		}
	case ImageSourceURL:
		if i.URL == "" {
			return fmt.Errorf("image URL is empty")
		}
	default:
		return fmt.Errorf("invalid image source: %s", i.Source)
	}

	return nil
}

// Validate validates the audio content.
func (a *AudioContent) Validate() error {
	if a.Format == "" {
		return fmt.Errorf("audio format is required")
	}

	if len(a.Data) == 0 && a.URL == "" {
		return fmt.Errorf("audio must have either data or URL")
	}

	return nil
}

// Validate validates the video content.
func (v *VideoContent) Validate() error {
	if v.Format == "" {
		return fmt.Errorf("video format is required")
	}

	if len(v.Data) == 0 && v.URL == "" {
		return fmt.Errorf("video must have either data or URL")
	}

	return nil
}

// Validate validates the document content.
func (d *DocumentContent) Validate() error {
	if d.Format == "" {
		return fmt.Errorf("document format is required")
	}

	if len(d.Data) == 0 && d.URL == "" && d.Text == "" {
		return fmt.Errorf("document must have data, URL, or text")
	}

	return nil
}
