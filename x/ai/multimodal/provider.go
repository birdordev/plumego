package multimodal

import (
	"context"

	"github.com/birdordev/plumego/x/ai/provider"
)

// Provider extends the base provider with multimodal capabilities.
type Provider interface {
	provider.Provider

	// SupportedModalities returns the modalities supported by this provider.
	SupportedModalities() []ContentType

	// SupportsModality checks if a specific modality is supported.
	SupportsModality(modality ContentType) bool

	// CompleteMultimodal sends a multimodal completion request.
	CompleteMultimodal(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)

	// CompleteMultimodalStream sends a streaming multimodal completion request.
	CompleteMultimodalStream(ctx context.Context, req *CompletionRequest) (*StreamReader, error)
}

// ImageProvider provides image generation capabilities.
type ImageProvider interface {
	// GenerateImage generates an image from a text prompt.
	GenerateImage(ctx context.Context, req *ImageGenerationRequest) (*ImageGenerationResponse, error)

	// EditImage edits an existing image based on a prompt.
	EditImage(ctx context.Context, req *ImageEditRequest) (*ImageGenerationResponse, error)

	// CreateVariation creates a variation of an existing image.
	CreateVariation(ctx context.Context, req *ImageVariationRequest) (*ImageGenerationResponse, error)
}

// AudioProvider provides audio processing capabilities.
type AudioProvider interface {
	// SynthesizeSpeech converts text to speech.
	SynthesizeSpeech(ctx context.Context, req *SpeechSynthesisRequest) (*AudioContent, error)

	// TranscribeAudio transcribes audio to text.
	TranscribeAudio(ctx context.Context, req *TranscriptionRequest) (*TranscriptionResult, error)

	// TranslateAudio translates audio from one language to another.
	TranslateAudio(ctx context.Context, req *TranslationRequest) (*TranscriptionResult, error)
}

// VideoProvider provides video analysis capabilities.
type VideoProvider interface {
	// AnalyzeVideo analyzes video content.
	AnalyzeVideo(ctx context.Context, req *VideoAnalysisRequest) (*VideoAnalysisResult, error)

	// ExtractFrames extracts frames from a video.
	ExtractFrames(ctx context.Context, req *FrameExtractionRequest) ([]*ImageContent, error)
}

// DocumentProvider provides document processing capabilities.
type DocumentProvider interface {
	// ExtractText extracts text from a document.
	ExtractText(ctx context.Context, doc *DocumentContent) (string, error)

	// AnalyzeDocument analyzes document structure and content.
	AnalyzeDocument(ctx context.Context, req *DocumentAnalysisRequest) (*DocumentAnalysisResult, error)
}

// CompletionRequest represents a multimodal completion request.
type CompletionRequest struct {
	Model       string            `json:"model"`
	Messages    []Message         `json:"messages"`
	MaxTokens   int               `json:"max_tokens,omitempty"`
	Temperature float64           `json:"temperature,omitempty"`
	TopP        float64           `json:"top_p,omitempty"`
	Stop        []string          `json:"stop,omitempty"`
	Stream      bool              `json:"stream,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// CompletionResponse is an alias for provider.CompletionResponse.
// Multimodal providers return the same response type as text providers;
// rich media content is represented via provider.ContentBlock fields.
type CompletionResponse = provider.CompletionResponse

// StreamReader reads streaming multimodal responses.
type StreamReader struct {
	stream *provider.StreamReader
}

// Next reads the next chunk from the stream and returns the accumulated response.
func (s *StreamReader) Next() (*CompletionResponse, error) {
	chunk, err := s.stream.Next()
	if err != nil {
		return nil, err
	}

	resp := &CompletionResponse{
		StopReason: chunk.StopReason,
	}
	if chunk.Delta != nil {
		resp.Content = []provider.ContentBlock{
			{
				Type: chunk.Delta.Type,
				Text: chunk.Delta.Text,
			},
		}
	}
	if chunk.Usage != nil {
		resp.Usage = *chunk.Usage
	}
	return resp, nil
}

// Close closes the stream.
func (s *StreamReader) Close() error {
	return s.stream.Close()
}

// ImageGenerationRequest represents an image generation request.
type ImageGenerationRequest struct {
	Prompt         string      `json:"prompt"`
	Model          string      `json:"model,omitempty"`
	Size           string      `json:"size,omitempty"`            // e.g., "1024x1024"
	Quality        string      `json:"quality,omitempty"`         // e.g., "standard", "hd"
	Style          string      `json:"style,omitempty"`           // e.g., "natural", "vivid"
	N              int         `json:"n,omitempty"`               // Number of images
	ResponseFormat ImageFormat `json:"response_format,omitempty"` // png, jpeg, webp
}

// ImageGenerationResponse represents an image generation response.
type ImageGenerationResponse struct {
	Created int64           `json:"created"`
	Images  []*ImageContent `json:"images"`
}

// ImageEditRequest represents an image editing request.
type ImageEditRequest struct {
	Image  *ImageContent `json:"image"`
	Mask   *ImageContent `json:"mask,omitempty"`
	Prompt string        `json:"prompt"`
	Model  string        `json:"model,omitempty"`
	Size   string        `json:"size,omitempty"`
	N      int           `json:"n,omitempty"`
}

// ImageVariationRequest represents an image variation request.
type ImageVariationRequest struct {
	Image *ImageContent `json:"image"`
	Model string        `json:"model,omitempty"`
	Size  string        `json:"size,omitempty"`
	N     int           `json:"n,omitempty"`
}

// SpeechSynthesisRequest represents a text-to-speech request.
type SpeechSynthesisRequest struct {
	Text   string      `json:"text"`
	Model  string      `json:"model,omitempty"`
	Voice  string      `json:"voice,omitempty"`
	Format AudioFormat `json:"format,omitempty"`
	Speed  float64     `json:"speed,omitempty"` // 0.25 to 4.0
}

// TranscriptionRequest represents an audio transcription request.
type TranscriptionRequest struct {
	Audio       *AudioContent `json:"audio"`
	Model       string        `json:"model,omitempty"`
	Language    string        `json:"language,omitempty"`
	Prompt      string        `json:"prompt,omitempty"`
	Format      string        `json:"format,omitempty"` // json, text, srt, vtt
	Temperature float64       `json:"temperature,omitempty"`
}

// TranscriptionResult represents a transcription result.
type TranscriptionResult struct {
	Text     string                 `json:"text"`
	Language string                 `json:"language,omitempty"`
	Duration float64                `json:"duration,omitempty"`
	Segments []TranscriptionSegment `json:"segments,omitempty"`
}

// TranscriptionSegment represents a segment of transcription.
type TranscriptionSegment struct {
	ID    int     `json:"id"`
	Start float64 `json:"start"` // seconds
	End   float64 `json:"end"`   // seconds
	Text  string  `json:"text"`
}

// TranslationRequest represents an audio translation request.
type TranslationRequest struct {
	Audio       *AudioContent `json:"audio"`
	Model       string        `json:"model,omitempty"`
	Prompt      string        `json:"prompt,omitempty"`
	Format      string        `json:"format,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
}

// VideoAnalysisRequest represents a video analysis request.
type VideoAnalysisRequest struct {
	Video     *VideoContent `json:"video"`
	Model     string        `json:"model,omitempty"`
	Tasks     []string      `json:"tasks,omitempty"`      // e.g., ["object_detection", "scene_detection"]
	FrameRate float64       `json:"frame_rate,omitempty"` // frames per second to analyze
	StartTime float64       `json:"start_time,omitempty"` // seconds
	EndTime   float64       `json:"end_time,omitempty"`   // seconds
}

// VideoAnalysisResult represents a video analysis result.
type VideoAnalysisResult struct {
	Duration    float64                `json:"duration"`
	FrameCount  int                    `json:"frame_count"`
	Scenes      []VideoScene           `json:"scenes,omitempty"`
	Objects     []DetectedObject       `json:"objects,omitempty"`
	Transcripts []TranscriptionSegment `json:"transcripts,omitempty"`
	Summary     string                 `json:"summary,omitempty"`
}

// VideoScene represents a scene in a video.
type VideoScene struct {
	ID          int     `json:"id"`
	Start       float64 `json:"start"` // seconds
	End         float64 `json:"end"`   // seconds
	Description string  `json:"description"`
	Confidence  float64 `json:"confidence"`
}

// DetectedObject represents a detected object in video.
type DetectedObject struct {
	Label       string     `json:"label"`
	Confidence  float64    `json:"confidence"`
	BoundingBox *Rectangle `json:"bounding_box,omitempty"`
	Timestamp   float64    `json:"timestamp,omitempty"` // seconds
}

// Rectangle represents a bounding box.
type Rectangle struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// FrameExtractionRequest represents a frame extraction request.
type FrameExtractionRequest struct {
	Video     *VideoContent `json:"video"`
	FrameRate float64       `json:"frame_rate,omitempty"` // frames per second
	Times     []float64     `json:"times,omitempty"`      // specific timestamps
	Format    ImageFormat   `json:"format,omitempty"`
}

// DocumentAnalysisRequest represents a document analysis request.
type DocumentAnalysisRequest struct {
	Document *DocumentContent `json:"document"`
	Model    string           `json:"model,omitempty"`
	Tasks    []string         `json:"tasks,omitempty"` // e.g., ["extract_text", "table_detection"]
}

// DocumentAnalysisResult represents a document analysis result.
type DocumentAnalysisResult struct {
	Text      string             `json:"text"`
	Pages     []DocumentPage     `json:"pages,omitempty"`
	Tables    []Table            `json:"tables,omitempty"`
	Figures   []Figure           `json:"figures,omitempty"`
	Metadata  map[string]any     `json:"metadata,omitempty"`
	Structure *DocumentStructure `json:"structure,omitempty"`
}

// DocumentPage represents a page in a document.
type DocumentPage struct {
	Number int           `json:"number"`
	Text   string        `json:"text"`
	Width  int           `json:"width"`
	Height int           `json:"height"`
	Image  *ImageContent `json:"image,omitempty"`
}

// Table represents a detected table in a document.
type Table struct {
	Page        int        `json:"page"`
	Rows        [][]string `json:"rows"`
	Headers     []string   `json:"headers,omitempty"`
	BoundingBox *Rectangle `json:"bounding_box,omitempty"`
}

// Figure represents a detected figure in a document.
type Figure struct {
	Page        int           `json:"page"`
	Caption     string        `json:"caption,omitempty"`
	Image       *ImageContent `json:"image,omitempty"`
	BoundingBox *Rectangle    `json:"bounding_box,omitempty"`
}

// DocumentStructure represents the structure of a document.
type DocumentStructure struct {
	Title    string            `json:"title,omitempty"`
	Author   string            `json:"author,omitempty"`
	Sections []DocumentSection `json:"sections,omitempty"`
	TOC      []TOCEntry        `json:"toc,omitempty"` // Table of contents
}

// DocumentSection represents a section in a document.
type DocumentSection struct {
	Level   int    `json:"level"` // 1=H1, 2=H2, etc.
	Title   string `json:"title"`
	Content string `json:"content"`
	Page    int    `json:"page,omitempty"`
}

// TOCEntry represents a table of contents entry.
type TOCEntry struct {
	Level int    `json:"level"`
	Title string `json:"title"`
	Page  int    `json:"page"`
}

// Capability represents a provider capability.
type Capability struct {
	Type        ContentType `json:"type"`
	Supported   bool        `json:"supported"`
	MaxSize     int64       `json:"max_size,omitempty"`     // bytes
	MaxDuration float64     `json:"max_duration,omitempty"` // seconds (for audio/video)
	Formats     []string    `json:"formats,omitempty"`
	Description string      `json:"description,omitempty"`
}

// ProviderCapabilities describes all capabilities of a provider.
type ProviderCapabilities struct {
	Text     *Capability `json:"text"`
	Image    *Capability `json:"image"`
	Audio    *Capability `json:"audio"`
	Video    *Capability `json:"video"`
	Document *Capability `json:"document"`
}
