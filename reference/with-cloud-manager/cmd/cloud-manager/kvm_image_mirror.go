package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"with-cloud-manager/internal/kvm"
)

type kvmImageMirrorProfile struct {
	MirrorURL string `json:"mirror_url"`
	ImageRoot string `json:"image_root"`
}

type kvmImageMirror struct {
	base *url.URL
	root string
}

func readKVMImageMirror(input io.Reader) (kvmImageMirror, error) {
	raw, err := readOperationInput(input, 4096)
	if err != nil {
		return kvmImageMirror{}, err
	}
	var profile kvmImageMirrorProfile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&profile) != nil || decoder.Decode(new(any)) != io.EOF {
		return kvmImageMirror{}, errors.New("invalid local KVM image mirror profile")
	}
	mirror, err := operationClientBase(profile.MirrorURL)
	if err != nil || mirror.Scheme != "https" {
		return kvmImageMirror{}, errors.New("KVM image mirror must be an HTTPS base URL")
	}
	return kvmImageMirror{base: mirror, root: profile.ImageRoot}, nil
}

func (mirror kvmImageMirror) publish(ctx context.Context, image kvm.BootImageSpec, authorize func() error, transport http.RoundTripper) error {
	name := image.SHA256 + ".raw"
	return publishKVMImage(ctx, mirror.root, name, image, authorize, func() (io.ReadCloser, error) {
		return openKVMImageMirror(ctx, mirror.base.JoinPath(name), image.SizeBytes, transport)
	})
}

func runKVMImageMirror(ctx context.Context, api kvmVMAPI, site, id string, input io.Reader, output io.Writer) error {
	mirror, err := readKVMImageMirror(input)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	var image kvm.BootImage
	if err := api.request(ctx, api.target, http.MethodGet, nil, &image); err != nil {
		return err
	}
	if !image.Valid() || image.ID != id || image.Site != site {
		return errors.New("KVM image catalog response does not match the requested image")
	}

	authorize := func() error {
		var current kvm.BootImage
		if err := api.request(ctx, api.target, http.MethodGet, nil, &current); err != nil {
			return err
		}
		if current.BootImageSpec != image.BootImageSpec || current.Site != image.Site {
			return errors.New("KVM image catalog identity changed")
		}
		return nil
	}
	if err := mirror.publish(ctx, image.BootImageSpec, authorize, api.transport); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(image)
}

func openKVMImageMirror(ctx context.Context, target *url.URL, bytes int64, transport http.RoundTripper) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, errors.New("invalid KVM image mirror request")
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("Accept-Encoding", "identity")
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("KVM image mirror request failed")
	}
	if response.StatusCode != http.StatusOK || response.Uncompressed || response.Header.Get("Content-Encoding") != "" {
		return nil, errors.Join(errors.New("invalid KVM image mirror response"), response.Body.Close())
	}
	if response.ContentLength >= 0 && response.ContentLength != bytes {
		return nil, errors.Join(errKVMImageMismatch, response.Body.Close())
	}
	return response.Body, nil
}
