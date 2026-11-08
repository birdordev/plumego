package file

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"strings"

	storefile "github.com/birdordev/plumego/store/file"
)

type imageInfo struct {
	Width  int
	Height int
	Format string
}

type imageProcessor struct{}

func newImageProcessor() *imageProcessor {
	return &imageProcessor{}
}

func (p *imageProcessor) Resize(src io.Reader, width, height int) (io.Reader, error) {
	img, format, err := image.Decode(src)
	if err != nil {
		return nil, &storefile.Error{Op: "Resize", Err: err}
	}

	resized := resizeImage(img, width, height)

	buf := new(bytes.Buffer)
	switch format {
	case "jpeg":
		err = jpeg.Encode(buf, resized, &jpeg.Options{Quality: 85})
	case "png":
		err = png.Encode(buf, resized)
	case "gif":
		err = gif.Encode(buf, resized, nil)
	default:
		err = fmt.Errorf("unsupported format: %s", format)
	}
	if err != nil {
		return nil, err
	}

	return buf, nil
}

func (p *imageProcessor) Thumbnail(src io.Reader, maxWidth, maxHeight int) (io.Reader, error) {
	img, format, err := image.Decode(src)
	if err != nil {
		return nil, &storefile.Error{Op: "Thumbnail", Err: err}
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	if width <= maxWidth && height <= maxHeight {
		buf := new(bytes.Buffer)
		switch format {
		case "jpeg":
			err = jpeg.Encode(buf, img, &jpeg.Options{Quality: 85})
		case "png":
			err = png.Encode(buf, img)
		case "gif":
			err = gif.Encode(buf, img, nil)
		default:
			err = fmt.Errorf("unsupported format: %s", format)
		}
		if err != nil {
			return nil, err
		}
		return buf, nil
	}

	var newWidth, newHeight int
	if width > height {
		newWidth = maxWidth
		newHeight = height * maxWidth / width
		if newHeight > maxHeight {
			newHeight = maxHeight
			newWidth = width * maxHeight / height
		}
	} else {
		newHeight = maxHeight
		newWidth = width * maxHeight / height
		if newWidth > maxWidth {
			newWidth = maxWidth
			newHeight = height * maxWidth / width
		}
	}

	resized := resizeImage(img, newWidth, newHeight)

	buf := new(bytes.Buffer)
	switch format {
	case "jpeg":
		err = jpeg.Encode(buf, resized, &jpeg.Options{Quality: 85})
	case "png":
		err = png.Encode(buf, resized)
	case "gif":
		err = gif.Encode(buf, resized, nil)
	default:
		err = fmt.Errorf("unsupported format: %s", format)
	}
	if err != nil {
		return nil, err
	}

	return buf, nil
}

func (p *imageProcessor) GetInfo(src io.Reader) (*imageInfo, error) {
	config, format, err := image.DecodeConfig(src)
	if err != nil {
		return nil, &storefile.Error{Op: "GetInfo", Err: err}
	}

	return &imageInfo{
		Width:  config.Width,
		Height: config.Height,
		Format: format,
	}, nil
}

func (p *imageProcessor) IsImage(mimeType string) bool {
	mimeType = strings.ToLower(mimeType)
	return mimeType == "image/jpeg" ||
		mimeType == "image/png" ||
		mimeType == "image/gif" ||
		mimeType == "image/webp"
}

func (p *imageProcessor) SupportsThumbnail(mimeType string) bool {
	mimeType = strings.ToLower(mimeType)
	return mimeType == "image/jpeg" ||
		mimeType == "image/png" ||
		mimeType == "image/gif"
}

func resizeImage(src image.Image, width, height int) image.Image {
	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()

	dst := image.NewRGBA(image.Rect(0, 0, width, height))

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			srcX := x * srcW / width
			srcY := y * srcH / height
			dst.Set(x, y, src.At(srcX+srcBounds.Min.X, srcY+srcBounds.Min.Y))
		}
	}

	return dst
}
