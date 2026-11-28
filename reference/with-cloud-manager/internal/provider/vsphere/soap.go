package vsphere

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"with-cloud-manager/internal/provider"
)

const (
	soapNamespace   = "http://schemas.xmlsoap.org/soap/envelope/"
	vimNamespace    = "urn:vim25"
	maxSOAPResponse = 4 << 20
)

type soapEnvelope struct {
	XMLName xml.Name `xml:"http://schemas.xmlsoap.org/soap/envelope/ Envelope"`
	Body    soapBody `xml:"http://schemas.xmlsoap.org/soap/envelope/ Body"`
}

type soapBody struct {
	expected string
	out      any
	found    bool
	fault    string
}

func (c *Client) call(ctx context.Context, method string, input, output any) error {
	var buf bytes.Buffer
	buf.WriteString(`<soap:Envelope xmlns:soap="` + soapNamespace + `" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"><soap:Body>`)
	encoder := xml.NewEncoder(&buf)
	if err := encoder.EncodeElement(input, xml.StartElement{Name: xml.Name{Space: vimNamespace, Local: method}}); err != nil {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	buf.WriteString(`</soap:Body></soap:Envelope>`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint.String(), &buf)
	if err != nil {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", `"urn:vim25/7.0"`)
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &provider.Failure{Kind: provider.Transient}
	}
	defer func() { _ = res.Body.Close() }()
	switch res.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &provider.Failure{Kind: provider.Unauthorized}
	case http.StatusTooManyRequests:
		return &provider.Failure{Kind: provider.Throttled, RetryAfter: retryAfter(res.Header.Get("Retry-After"))}
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return &provider.Failure{Kind: provider.Transient}
	case http.StatusOK, http.StatusInternalServerError:
	default:
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	limited := &io.LimitedReader{R: res.Body, N: maxSOAPResponse + 1}
	decoder := xml.NewDecoder(limited)
	envelope := soapEnvelope{Body: soapBody{expected: method + "Response", out: output}}
	if err := decoder.Decode(&envelope); err != nil || !envelope.Body.found {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		data, whitespace := token.(xml.CharData)
		if err != nil || !whitespace || strings.TrimSpace(string(data)) != "" {
			return &provider.Failure{Kind: provider.InvalidResponse}
		}
	}
	if limited.N == 0 {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	if envelope.Body.fault != "" {
		return classifyFault(envelope.Body.fault)
	}
	if res.StatusCode != http.StatusOK {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	return nil
}

func (b *soapBody) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	if b.found {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch element := token.(type) {
		case xml.EndElement:
			return nil
		case xml.StartElement:
			if b.found {
				return &provider.Failure{Kind: provider.InvalidResponse}
			}
			b.found = true
			switch element.Name {
			case xml.Name{Space: soapNamespace, Local: "Fault"}:
				var fault struct {
					Detail struct {
						Type struct{ XMLName xml.Name } `xml:",any"`
					} `xml:"detail"`
				}
				if err := decoder.DecodeElement(&fault, &element); err != nil {
					return err
				}
				b.fault = fault.Detail.Type.XMLName.Local
				if b.fault == "" {
					b.fault = "unknown"
				}
			case xml.Name{Space: vimNamespace, Local: b.expected}:
				if err := decoder.DecodeElement(b.out, &element); err != nil {
					return err
				}
			default:
				return &provider.Failure{Kind: provider.InvalidResponse}
			}
		}
	}
}

func classifyFault(name string) error {
	switch name {
	case "NoPermissionFault", "NotAuthenticatedFault", "InvalidLoginFault":
		return &provider.Failure{Kind: provider.Unauthorized}
	case "ManagedObjectNotFoundFault":
		return &provider.Failure{Kind: provider.OutcomeUnknown}
	case "RuntimeFault", "RequestCanceledFault":
		return &provider.Failure{Kind: provider.Transient}
	default:
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
}

func retryAfter(value string) time.Duration {
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 1 || seconds > 3600 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
