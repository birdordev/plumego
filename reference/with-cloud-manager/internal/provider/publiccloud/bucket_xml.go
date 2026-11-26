package publiccloud

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"

	"with-cloud-manager/internal/provider"
)

func (c *Client) readBucketXML(req *http.Request, dst *bucketList) error {
	req.Header.Set("Accept", "application/xml")
	res, err := c.http.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return req.Context().Err()
		}
		return &provider.Failure{Kind: provider.Transient}
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return cloudHTTPFailure(res)
	}
	body := &io.LimitedReader{R: res.Body, N: (4 << 20) + 1}
	decoder := xml.NewDecoder(body)
	if decoder.Decode(dst) != nil {
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF && body.N > 0 {
			return nil
		}
		switch value := token.(type) {
		case xml.CharData:
			if err == nil && len(bytes.TrimSpace(value)) == 0 {
				continue
			}
		case xml.Comment:
			if err == nil {
				continue
			}
		}
		return &provider.Failure{Kind: provider.InvalidResponse}
	}
}
