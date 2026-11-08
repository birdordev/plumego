# x/ai/multimodal

> **Import path:** `github.com/birdordev/plumego/x/ai/multimodal` — sub-package of [`x/ai`](../README.md).

## Purpose

`x/ai/multimodal` extends the `x/ai/provider` content contracts with audio,
video, and document content types, multimodal message construction, and helpers
for loading content from files and URLs with MIME detection.

## Status

`experimental surface` — APIs may change; parent family `x/ai` is experimental.
See [`docs/EXTENSION_MATURITY.md`](../../../../EXTENSION_MATURITY.md).

## Use this module when

- building messages with audio, video, or document content blocks
- loading multimodal content from files or URLs
- detecting MIME types and negotiating content formats

## Do not use this module for

- text/image/tool content — use `x/ai/provider` directly
- provider adapter implementation or session lifecycle
- image processing / transcoding or file storage backends

## Public entrypoints

- `ContentType` — extended content-type constants (audio, video, document)
- `ContentBlock`, `Message` — multimodal payload types
- `ImageContent`, `AudioContent`, `VideoContent`, `DocumentContent`, `ToolUse` — content blocks
- `LoadFromFile`, `LoadFromURL` — content loaders
- `DetectContentType` — MIME detection helper

## Validation

```bash
go test -race -timeout 60s ./x/ai/multimodal/...
go test -timeout 20s ./x/ai/multimodal/...
go vet ./x/ai/multimodal/...
```
