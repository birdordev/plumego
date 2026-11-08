# Multimodal AI Package

The `multimodal` package provides comprehensive support for multimodal AI interactions, enabling applications to work with text, images, audio, video, and documents in a unified way.

## Features

- **Multiple Content Types**: Text, images, audio, video, documents, tool use
- **Flexible Message Structure**: Support for multiple content blocks per message
- **Provider Abstraction**: Unified interface for multimodal AI providers
- **Content Validation**: Built-in validation for all content types
- **Helper Functions**: Format detection, loading, encoding, and utilities
- **Type Safety**: Strongly-typed content structures

## Supported Content Types

### Text
Standard text content with full Unicode support.

### Images
- **Formats**: JPEG, PNG, WebP, GIF
- **Sources**: Base64-encoded data or URLs
- **Detection**: Automatic format detection from file headers
- **Metadata**: Dimensions, size, MIME type

### Audio
- **Formats**: MP3, WAV, OGG, FLAC, M4A
- **Sources**: Binary data or URLs
- **Metadata**: Duration, transcript, sample rate, channels

### Video
- **Formats**: MP4, WebM, AVI, MOV
- **Sources**: Binary data or URLs
- **Metadata**: Duration, dimensions, frame rate, thumbnails

### Documents
- **Formats**: PDF, DOCX, TXT, HTML, Markdown, CSV, JSON, XML
- **Sources**: Binary data, URLs, or extracted text
- **Metadata**: Page count, title, author

## Quick Start

### Creating Messages

```go
import "github.com/birdordev/plumego/x/ai/multimodal"

// Simple text message
msg := multimodal.NewTextMessage(multimodal.RoleUser, "Hello, AI!")

// Message with image
img := &multimodal.ImageContent{
    Format: multimodal.ImageFormatJPEG,
    Source: multimodal.ImageSourceURL,
    URL:    "https://example.com/image.jpg",
}
imgMsg := multimodal.NewImageMessage(multimodal.RoleUser, img, "What's in this image?")

// Complex message with multiple content blocks
msg := &multimodal.Message{
    Role: multimodal.RoleUser,
    Content: []multimodal.ContentBlock{
        {Type: multimodal.ContentTypeText, Text: "Analyze this:"},
        {Type: multimodal.ContentTypeImage, Image: img},
        {Type: multimodal.ContentTypeText, Text: "What do you see?"},
    },
}
```

### Loading Content from Files

```go
// Load image
img, err := multimodal.LoadImageFromFile("photo.jpg")
if err != nil {
    log.Fatal(err)
}

// Load audio
audio, err := multimodal.LoadAudioFromFile("recording.mp3")
if err != nil {
    log.Fatal(err)
}

// Load document
doc, err := multimodal.LoadDocumentFromFile("report.pdf")
if err != nil {
    log.Fatal(err)
}
```

### Loading Content from URLs

```go
// Load image from URL
img, err := multimodal.LoadImageFromURL("https://example.com/image.png")
if err != nil {
    log.Fatal(err)
}
```

### Adding Content to Messages

```go
msg := &multimodal.Message{
    Role:    multimodal.RoleUser,
    Content: []multimodal.ContentBlock{},
}

// Add text
msg.AddText("Here's my question:")

// Add image
img, _ := multimodal.LoadImageFromFile("diagram.png")
msg.AddImage(img)

// Add audio
audio, _ := multimodal.LoadAudioFromFile("voice.mp3")
msg.AddAudio(audio)

// Add document
doc, _ := multimodal.LoadDocumentFromFile("contract.pdf")
msg.AddDocument(doc)
```

### Content Helpers

```go
// Get all text from message
text := msg.GetText()

// Check for specific content types
hasImages := msg.HasImages()
hasAudio := msg.HasAudio()
hasVideo := msg.HasVideo()
hasDocs := msg.HasDocuments()

// Get all content types
types := msg.ContentTypes() // []ContentType{"text", "image"}

// Extract text from multiple content blocks
text := multimodal.ContentBlocksToText(msg.Content)

// Estimate token count
tokens := multimodal.CountTokensApprox(msg.Content)
```

### Validation

```go
// Validate message
if err := msg.Validate(); err != nil {
    log.Printf("Invalid message: %v", err)
}

// Validate image content
if err := img.Validate(); err != nil {
    log.Printf("Invalid image: %v", err)
}

// Validate size
maxSize := int64(10 * 1024 * 1024) // 10 MB
if err := multimodal.ValidateSize(img.SizeBytes, maxSize); err != nil {
    log.Printf("Image too large: %v", err)
}
```

## Provider Interface

The package defines a `Provider` interface that extends the base AI provider with multimodal capabilities:

```go
type Provider interface {
    provider.Provider

    // Get supported modalities
    SupportedModalities() []ContentType
    SupportsModality(modality ContentType) bool

    // Multimodal completion
    CompleteMultimodal(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)
    CompleteMultimodalStream(ctx context.Context, req *CompletionRequest) (*StreamReader, error)
}
```

### Specialized Provider Interfaces

#### Image Provider
```go
type ImageProvider interface {
    GenerateImage(ctx context.Context, req *ImageGenerationRequest) (*ImageGenerationResponse, error)
    EditImage(ctx context.Context, req *ImageEditRequest) (*ImageGenerationResponse, error)
    CreateVariation(ctx context.Context, req *ImageVariationRequest) (*ImageGenerationResponse, error)
}
```

#### Audio Provider
```go
type AudioProvider interface {
    SynthesizeSpeech(ctx context.Context, req *SpeechSynthesisRequest) (*AudioContent, error)
    TranscribeAudio(ctx context.Context, req *TranscriptionRequest) (*TranscriptionResult, error)
    TranslateAudio(ctx context.Context, req *TranslationRequest) (*TranscriptionResult, error)
}
```

#### Video Provider
```go
type VideoProvider interface {
    AnalyzeVideo(ctx context.Context, req *VideoAnalysisRequest) (*VideoAnalysisResult, error)
    ExtractFrames(ctx context.Context, req *FrameExtractionRequest) ([]*ImageContent, error)
}
```

#### Document Provider
```go
type DocumentProvider interface {
    ExtractText(ctx context.Context, doc *DocumentContent) (string, error)
    AnalyzeDocument(ctx context.Context, req *DocumentAnalysisRequest) (*DocumentAnalysisResult, error)
}
```

## Utility Functions

### Format Detection

```go
// Detect image format from binary data
format, err := multimodal.DetectImageFormat(imageData)

// Detect audio format from file extension
format := multimodal.DetectAudioFormat("audio.mp3")

// Detect document format from file extension
format := multimodal.DetectDocumentFormat("document.pdf")

// Get MIME type
mimeType := multimodal.GetMimeType("png") // "image/png"
```

### Base64 Encoding

```go
// Encode binary data to base64
encoded := multimodal.EncodeBase64(imageData)

// Decode base64 to binary
decoded, err := multimodal.DecodeBase64(encoded)

// Get base64 string from image content
b64 := img.Base64String()
```

### Formatting

```go
// Format duration
duration := multimodal.FormatDuration(3665.0) // "1:01:05"

// Format file size
size := multimodal.FormatFileSize(1048576) // "1.0 MB"
```

### Validation

```go
// Validate URL
isValid := multimodal.IsValidURL("https://example.com") // true

// Sanitize filename
safe := multimodal.SanitizeFilename("my/file:name?.pdf") // "my_file_name_.pdf"
```

## Complete Example

```go
package main

import (
    "context"
    "log"

    "github.com/birdordev/plumego/x/ai/multimodal"
)

func main() {
    // Create a multimodal message
    msg := multimodal.NewTextMessage(multimodal.RoleUser, "Analyze this image and audio:")

    // Load and add image
    img, err := multimodal.LoadImageFromFile("screenshot.png")
    if err != nil {
        log.Fatal(err)
    }
    msg.AddImage(img)

    // Load and add audio
    audio, err := multimodal.LoadAudioFromFile("recording.mp3")
    if err != nil {
        log.Fatal(err)
    }
    msg.AddAudio(audio)

    // Add follow-up question
    msg.AddText("What's happening in this scene?")

    // Validate message
    if err := msg.Validate(); err != nil {
        log.Fatal(err)
    }

    // Estimate tokens
    tokens := multimodal.CountTokensApprox(msg.Content)
    log.Printf("Estimated tokens: %d", tokens)

    // Create completion request
    req := &multimodal.CompletionRequest{
        Model:       "claude-3-5-sonnet-20241022",
        Messages:    []multimodal.Message{*msg},
        MaxTokens:   1024,
        Temperature: 0.7,
    }

    // Use with a multimodal provider
    // resp, err := provider.CompleteMultimodal(context.Background(), req)
}
```

## Image Generation Example

```go
package main

import (
    "context"
    "log"
    "os"

    "github.com/birdordev/plumego/x/ai/multimodal"
)

func main() {
    // Assume we have an ImageProvider implementation
    var provider multimodal.ImageProvider

    // Generate image
    req := &multimodal.ImageGenerationRequest{
        Prompt:  "A serene mountain landscape at sunset",
        Model:   "dall-e-3",
        Size:    "1024x1024",
        Quality: "hd",
        Style:   "vivid",
        N:       1,
    }

    resp, err := provider.GenerateImage(context.Background(), req)
    if err != nil {
        log.Fatal(err)
    }

    // Save generated image
    if len(resp.Images) > 0 {
        img := resp.Images[0]
        err = os.WriteFile("generated.png", img.Data, 0644)
        if err != nil {
            log.Fatal(err)
        }
        log.Printf("Image saved: %s (%s)",
            multimodal.FormatFileSize(img.SizeBytes),
            img.Format)
    }
}
```

## Speech Synthesis Example

```go
package main

import (
    "context"
    "log"
    "os"

    "github.com/birdordev/plumego/x/ai/multimodal"
)

func main() {
    // Assume we have an AudioProvider implementation
    var provider multimodal.AudioProvider

    // Synthesize speech
    req := &multimodal.SpeechSynthesisRequest{
        Text:   "Hello, welcome to multimodal AI!",
        Model:  "tts-1",
        Voice:  "alloy",
        Format: multimodal.AudioFormatMP3,
        Speed:  1.0,
    }

    audio, err := provider.SynthesizeSpeech(context.Background(), req)
    if err != nil {
        log.Fatal(err)
    }

    // Save audio
    err = os.WriteFile("speech.mp3", audio.Data, 0644)
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Speech synthesized: %.2f seconds, %s",
        audio.Duration,
        multimodal.FormatFileSize(audio.SizeBytes))
}
```

## Audio Transcription Example

```go
package main

import (
    "context"
    "log"

    "github.com/birdordev/plumego/x/ai/multimodal"
)

func main() {
    // Assume we have an AudioProvider implementation
    var provider multimodal.AudioProvider

    // Load audio file
    audio, err := multimodal.LoadAudioFromFile("interview.mp3")
    if err != nil {
        log.Fatal(err)
    }

    // Transcribe
    req := &multimodal.TranscriptionRequest{
        Audio:    audio,
        Model:    "whisper-1",
        Language: "en",
        Format:   "json",
    }

    result, err := provider.TranscribeAudio(context.Background(), req)
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Transcription (%s, %.2f seconds):\n%s",
        result.Language,
        result.Duration,
        result.Text)

    // Print segments
    for _, seg := range result.Segments {
        log.Printf("[%.2f - %.2f] %s", seg.Start, seg.End, seg.Text)
    }
}
```

## Document Analysis Example

```go
package main

import (
    "context"
    "log"

    "github.com/birdordev/plumego/x/ai/multimodal"
)

func main() {
    // Assume we have a DocumentProvider implementation
    var provider multimodal.DocumentProvider

    // Load document
    doc, err := multimodal.LoadDocumentFromFile("report.pdf")
    if err != nil {
        log.Fatal(err)
    }

    // Analyze document
    req := &multimodal.DocumentAnalysisRequest{
        Document: doc,
        Model:    "claude-3-5-sonnet-20241022",
        Tasks:    []string{"extract_text", "table_detection", "structure_analysis"},
    }

    result, err := provider.AnalyzeDocument(context.Background(), req)
    if err != nil {
        log.Fatal(err)
    }

    // Print results
    log.Printf("Document: %d pages", len(result.Pages))
    log.Printf("Tables: %d", len(result.Tables))
    log.Printf("Figures: %d", len(result.Figures))

    if result.Structure != nil {
        log.Printf("Title: %s", result.Structure.Title)
        log.Printf("Sections: %d", len(result.Structure.Sections))
    }

    // Print extracted text
    log.Printf("\nExtracted text:\n%s", result.Text)
}
```

## Best Practices

### 1. Always Validate Content
```go
if err := msg.Validate(); err != nil {
    return fmt.Errorf("invalid message: %w", err)
}
```

### 2. Check Size Limits
```go
const maxImageSize = 10 * 1024 * 1024 // 10 MB
if err := multimodal.ValidateSize(img.SizeBytes, maxImageSize); err != nil {
    return fmt.Errorf("image too large: %w", err)
}
```

### 3. Use Appropriate Formats
- Images: Prefer PNG for screenshots, JPEG for photos, WebP for web
- Audio: MP3 for general use, WAV for high quality, OGG for web
- Documents: PDF for formatted documents, Markdown for structured text

### 4. Handle Errors Gracefully
```go
img, err := multimodal.LoadImageFromFile(path)
if err != nil {
    log.Printf("Failed to load image %s: %v", path, err)
    // Use fallback or skip
    return nil
}
```

### 5. Estimate Token Usage
```go
tokens := multimodal.CountTokensApprox(msg.Content)
if tokens > maxTokens {
    return fmt.Errorf("content too large: %d tokens (max %d)", tokens, maxTokens)
}
```

### 6. Clean Up Filenames
```go
filename := multimodal.SanitizeFilename(userInput)
path := filepath.Join(uploadDir, filename)
```

## Content Type Reference

| Type | Constant | Description |
|------|----------|-------------|
| Text | `ContentTypeText` | Plain text content |
| Image | `ContentTypeImage` | Image data or URL |
| Audio | `ContentTypeAudio` | Audio data or URL |
| Video | `ContentTypeVideo` | Video data or URL |
| Document | `ContentTypeDocument` | Document data, URL, or text |
| Tool Use | `ContentTypeToolUse` | Tool invocation request |
| Tool Result | `ContentTypeToolResult` | Tool invocation result |

## Message Roles

| Role | Constant | Description |
|------|----------|-------------|
| System | `RoleSystem` | System instructions |
| User | `RoleUser` | User input |
| Assistant | `RoleAssistant` | AI assistant response |
| Tool | `RoleTool` | Tool execution result |

## Format Constants

### Image Formats
- `ImageFormatJPEG` - JPEG/JPG
- `ImageFormatPNG` - PNG
- `ImageFormatWebP` - WebP
- `ImageFormatGIF` - GIF

### Audio Formats
- `AudioFormatMP3` - MP3
- `AudioFormatWAV` - WAV
- `AudioFormatOGG` - OGG
- `AudioFormatFLAC` - FLAC
- `AudioFormatM4A` - M4A

### Video Formats
- `VideoFormatMP4` - MP4
- `VideoFormatWebM` - WebM
- `VideoFormatAVI` - AVI
- `VideoFormatMOV` - MOV

### Document Formats
- `DocumentFormatPDF` - PDF
- `DocumentFormatDOCX` - Word DOCX
- `DocumentFormatTXT` - Plain text
- `DocumentFormatHTML` - HTML
- `DocumentFormatMD` - Markdown
- `DocumentFormatCSV` - CSV
- `DocumentFormatJSON` - JSON
- `DocumentFormatXML` - XML

## Error Handling

The package returns standard Go errors. Common error scenarios:

```go
// File not found
img, err := multimodal.LoadImageFromFile("missing.jpg")
if errors.Is(err, os.ErrNotExist) {
    log.Println("File not found")
}

// Invalid format
format, err := multimodal.DetectImageFormat(data)
if err != nil {
    log.Println("Unknown or invalid image format")
}

// Validation failure
err := img.Validate()
if err != nil {
    log.Printf("Validation failed: %v", err)
}
```

## License

Part of the Plumego framework. See main LICENSE file for details.
