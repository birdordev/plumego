package projecthttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode"
)

func Run(ctx context.Context, plan Request, token string, input io.Reader, output io.Writer, transport http.RoundTripper) error {
	if !ValidToken(token) {
		return errors.New("a valid CLOUD_MANAGER_API_TOKEN is required")
	}
	body := plan.Body
	if plan.InputLimit > 0 {
		var err error
		body, err = ReadInput(input, plan.InputLimit)
		if err != nil {
			return err
		}
	}
	timeout := plan.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, plan.Method, plan.Target.String(), reader)
	if err != nil {
		return errors.New("invalid operations request")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	expectedMedia, responseLimit := "application/json", 1<<20
	if plan.CSV {
		expectedMedia, responseLimit = "text/csv", 16<<20
	}
	if plan.ResponseLimit > 0 {
		responseLimit = plan.ResponseLimit
	}
	request.Header.Set("Accept", expectedMedia)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if plan.Method != http.MethodGet {
		request.GetBody = nil
	}
	if plan.Key != "" {
		request.Header.Set("Idempotency-Key", plan.Key)
	}
	if plan.Match != "" {
		request.Header.Set("If-Match", plan.Match)
	}
	client := NewClient(transport)
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("operations API request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != plan.Status {
		return fmt.Errorf("operations API returned HTTP %d", response.StatusCode)
	}
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != expectedMedia {
		return errors.New("invalid operations API response")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(responseLimit)+1))
	if err != nil || len(data) > responseLimit {
		return errors.New("cannot read operations API response")
	}
	if plan.CSV {
		_, err := output.Write(data)
		return err
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil || len(envelope.Data) == 0 || envelope.Data[0] != '{' {
		return errors.New("invalid operations API response")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, envelope.Data); err != nil {
		return errors.New("invalid operations API response")
	}
	_, err = fmt.Fprintln(output, compact.String())
	return err
}

func NewClient(transport http.RoundTripper) *http.Client {
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func ValidToken(token string) bool {
	return token != "" && len(token) <= 4096 &&
		strings.IndexFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

func BaseURL(address string) (*url.URL, error) {
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || u.Opaque != "" || u.RawPath != "" {
		return nil, errors.New("CLOUD_MANAGER_API_URL must be an HTTPS API base URL or a loopback HTTP URL")
	}
	if u.Scheme == "https" {
		return u, nil
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if u.Scheme == "http" && err == nil && ip.Unmap().IsLoopback() {
		return u, nil
	}
	return nil, errors.New("CLOUD_MANAGER_API_URL must be an HTTPS API base URL or a loopback HTTP URL")
}

type Request struct {
	Target        *url.URL
	Method        string
	Status        int
	Key           string
	Match         string
	Body          []byte
	InputLimit    int
	ResponseLimit int
	CSV           bool
	Timeout       time.Duration
}

func ReadInput(input io.Reader, limit int) ([]byte, error) {
	if input == nil {
		return nil, errors.New("a JSON request on standard input is required")
	}
	raw, err := io.ReadAll(io.LimitReader(input, int64(limit)+1))
	if err != nil || len(raw) > limit {
		return nil, fmt.Errorf("could not read a request of at most %d bytes", limit)
	}
	body := bytes.TrimSpace(raw)
	if len(body) == 0 || body[0] != '{' || !json.Valid(body) {
		return nil, errors.New("standard input must contain one JSON object")
	}
	return body, nil
}
