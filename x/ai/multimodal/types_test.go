package multimodal

import (
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/birdordev/plumego/x/ai/provider"
	"github.com/birdordev/plumego/x/ai/tokenizer"
)

func TestNewTextMessage(t *testing.T) {
	msg := NewTextMessage(RoleUser, "Hello, world!")

	if msg.Role != RoleUser {
		t.Errorf("expected role %s, got %s", RoleUser, msg.Role)
	}

	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg.Content))
	}

	if msg.Content[0].Type != ContentTypeText {
		t.Errorf("expected content type %s, got %s", ContentTypeText, msg.Content[0].Type)
	}

	if msg.Content[0].Text != "Hello, world!" {
		t.Errorf("expected text 'Hello, world!', got '%s'", msg.Content[0].Text)
	}
}

func TestMessage_AddContent(t *testing.T) {
	msg := &Message{
		Role:      RoleUser,
		Content:   []ContentBlock{},
		Timestamp: time.Now(),
	}

	// Add text
	msg.AddText("Hello")
	if len(msg.Content) != 1 {
		t.Errorf("expected 1 content block, got %d", len(msg.Content))
	}

	// Add image
	img := &ImageContent{
		Format: ImageFormatPNG,
		Source: ImageSourceURL,
		URL:    "https://example.com/image.png",
	}
	msg.AddImage(img)
	if len(msg.Content) != 2 {
		t.Errorf("expected 2 content blocks, got %d", len(msg.Content))
	}

	// Check content types
	types := msg.ContentTypes()
	if len(types) != 2 {
		t.Errorf("expected 2 content types, got %d", len(types))
	}
}

func TestMessage_Validate(t *testing.T) {
	tests := []struct {
		name    string
		msg     *Message
		wantErr bool
	}{
		{
			name: "valid text message",
			msg: &Message{
				Role: RoleUser,
				Content: []ContentBlock{
					{Type: ContentTypeText, Text: "Hello"},
				},
			},
			wantErr: false,
		},
		{
			name: "missing role",
			msg: &Message{
				Content: []ContentBlock{
					{Type: ContentTypeText, Text: "Hello"},
				},
			},
			wantErr: true,
		},
		{
			name: "no content",
			msg: &Message{
				Role:    RoleUser,
				Content: []ContentBlock{},
			},
			wantErr: true,
		},
		{
			name: "empty text",
			msg: &Message{
				Role: RoleUser,
				Content: []ContentBlock{
					{Type: ContentTypeText, Text: ""},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.msg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestImageContent_Validate(t *testing.T) {
	tests := []struct {
		name    string
		img     *ImageContent
		wantErr bool
	}{
		{
			name: "valid base64 image",
			img: &ImageContent{
				Format: ImageFormatPNG,
				Source: ImageSourceBase64,
				Data:   []byte("fake data"),
			},
			wantErr: false,
		},
		{
			name: "valid URL image",
			img: &ImageContent{
				Format: ImageFormatJPEG,
				Source: ImageSourceURL,
				URL:    "https://example.com/image.jpg",
			},
			wantErr: false,
		},
		{
			name: "missing format",
			img: &ImageContent{
				Source: ImageSourceBase64,
				Data:   []byte("fake data"),
			},
			wantErr: true,
		},
		{
			name: "base64 without data",
			img: &ImageContent{
				Format: ImageFormatPNG,
				Source: ImageSourceBase64,
			},
			wantErr: true,
		},
		{
			name: "URL without URL",
			img: &ImageContent{
				Format: ImageFormatPNG,
				Source: ImageSourceURL,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.img.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestMessage_ContentHelpers(t *testing.T) {
	msg := &Message{
		Role: RoleUser,
		Content: []ContentBlock{
			{Type: ContentTypeText, Text: "Hello "},
			{Type: ContentTypeImage, Image: &ImageContent{Format: ImageFormatPNG}},
			{Type: ContentTypeText, Text: "world!"},
		},
	}

	// Test GetText
	text := msg.GetText()
	expected := "Hello world!"
	if text != expected {
		t.Errorf("GetText() = %q, want %q", text, expected)
	}

	// Test HasImages
	if !msg.HasImages() {
		t.Error("HasImages() = false, want true")
	}

	// Test HasAudio
	if msg.HasAudio() {
		t.Error("HasAudio() = true, want false")
	}

	// Test ContentTypes
	types := msg.ContentTypes()
	if len(types) != 2 { // text and image
		t.Errorf("ContentTypes() returned %d types, want 2", len(types))
	}
}

func TestAudioContent_Validate(t *testing.T) {
	tests := []struct {
		name    string
		audio   *AudioContent
		wantErr bool
	}{
		{
			name: "valid audio with data",
			audio: &AudioContent{
				Format: AudioFormatMP3,
				Data:   []byte("fake audio data"),
			},
			wantErr: false,
		},
		{
			name: "valid audio with URL",
			audio: &AudioContent{
				Format: AudioFormatWAV,
				URL:    "https://example.com/audio.wav",
			},
			wantErr: false,
		},
		{
			name: "missing format",
			audio: &AudioContent{
				Data: []byte("fake audio data"),
			},
			wantErr: true,
		},
		{
			name: "no data or URL",
			audio: &AudioContent{
				Format: AudioFormatMP3,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.audio.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDocumentContent_Validate(t *testing.T) {
	tests := []struct {
		name    string
		doc     *DocumentContent
		wantErr bool
	}{
		{
			name: "valid document with data",
			doc: &DocumentContent{
				Format: DocumentFormatPDF,
				Data:   []byte("fake pdf data"),
			},
			wantErr: false,
		},
		{
			name: "valid document with text",
			doc: &DocumentContent{
				Format: DocumentFormatTXT,
				Text:   "Document text content",
			},
			wantErr: false,
		},
		{
			name: "valid document with URL",
			doc: &DocumentContent{
				Format: DocumentFormatDOCX,
				URL:    "https://example.com/doc.docx",
			},
			wantErr: false,
		},
		{
			name: "missing format",
			doc: &DocumentContent{
				Data: []byte("fake data"),
			},
			wantErr: true,
		},
		{
			name: "no data, URL, or text",
			doc: &DocumentContent{
				Format: DocumentFormatPDF,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.doc.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestImageContent_Base64String(t *testing.T) {
	data := []byte("test data")
	img := &ImageContent{
		Format: ImageFormatPNG,
		Source: ImageSourceBase64,
		Data:   data,
	}

	b64 := img.Base64String()
	if b64 == "" {
		t.Error("Base64String() returned empty string")
	}

	// Verify it's valid base64
	expected := "dGVzdCBkYXRh" // base64 of "test data"
	if b64 != expected {
		t.Errorf("Base64String() = %s, want %s", b64, expected)
	}
}

type streamCloseBody struct {
	calls int
	err   error
}

func (b *streamCloseBody) Read([]byte) (int, error) { return 0, io.EOF }
func (b *streamCloseBody) Close() error             { b.calls++; return b.err }

type streamCloseTransport struct{ body io.ReadCloser }

func (tr streamCloseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
	return &http.Response{StatusCode: http.StatusOK, Body: tr.body, Header: make(http.Header)}, nil
}

func TestMultimodalStreamClosePreservesUnderlyingError(t *testing.T) {
	sentinel := errors.New("test close failure")
	body := &streamCloseBody{err: sentinel}
	client := &http.Client{Transport: streamCloseTransport{body: body}}
	p := provider.NewOpenAIProvider("", provider.WithOpenAIHTTPClient(client))
	s, err := p.CompleteStream(t.Context(), &provider.CompletionRequest{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	reader := &StreamReader{stream: s}
	if err := reader.Close(); !errors.Is(err, sentinel) || body.calls != 1 {
		t.Errorf("close=%v, underlying calls=%d", err, body.calls)
	}
}

func TestMessageMixedMediaBlocks(t *testing.T) {
	message := Message{Role: RoleUser}
	audio := &AudioContent{Data: []byte("audio")}
	video := &VideoContent{Data: []byte("video")}
	document := &DocumentContent{Text: "document"}

	if message.HasVideo() || message.HasDocuments() {
		t.Fatal("empty message reported media")
	}
	message.AddAudio(audio)
	message.AddVideo(video)
	if !message.HasVideo() || message.HasDocuments() {
		t.Fatal("expected video only")
	}
	message.AddDocument(document)
	if !message.HasVideo() || !message.HasDocuments() || len(message.Content) != 3 {
		t.Fatalf("unexpected mixed content: %v", message.Content)
	}
	if got := message.Content[0]; got.Type != ContentTypeAudio || got.Audio != audio {
		t.Errorf("audio block = %+v", got)
	}
	if got := message.Content[1]; got.Type != ContentTypeVideo || got.Video != video {
		t.Errorf("video block = %+v", got)
	}
	if got := message.Content[2]; got.Type != ContentTypeDocument || got.Document != document {
		t.Errorf("document block = %+v", got)
	}
}

func TestStreamReaderPreservesChunksAndEOF(t *testing.T) {
	mock := provider.NewMockProvider("fixture")
	usage := tokenizer.TokenUsage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}
	mock.SetStreamChunks([]*provider.StreamChunk{
		{Delta: &provider.ContentDelta{Type: provider.ContentTypeText, Text: "hello"}},
		{Delta: &provider.ContentDelta{Type: provider.ContentTypeText, Text: " world"}},
		{Usage: &usage, StopReason: provider.StopReason("end_turn")},
	})
	upstream, err := mock.CompleteStream(t.Context(), &provider.CompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	stream := &StreamReader{stream: upstream}
	t.Cleanup(func() {
		if err := stream.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, want := range []string{"hello", " world"} {
		got, err := stream.Next()
		if err != nil || got == nil || len(got.Content) != 1 || got.Content[0].Type != provider.ContentTypeText || got.Content[0].Text != want {
			t.Fatalf("chunk = %+v, %v, want %q", got, err, want)
		}
	}
	last, err := stream.Next()
	if err != nil || last == nil || len(last.Content) != 0 || last.Usage != usage || last.StopReason != "end_turn" {
		t.Errorf("final chunk = %+v, %v", last, err)
	}
	if next, err := stream.Next(); next != nil || !errors.Is(err, io.EOF) {
		t.Errorf("end of stream = %v, %v", next, err)
	}
}
