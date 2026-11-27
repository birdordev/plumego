package publiccloud

import (
	"bytes"
	"encoding/xml"
	"io"
	"net/http"

	"with-cloud-manager/internal/provider"
)

func (c *Client) readBucketXML(req *http.Request, dst *bucketList) error {
	return c.readObjectStorageXML(req, dst, 4<<20, false)
}

func (c *Client) readObjectStorageXML(req *http.Request, dst any, limit int64, allowEmpty bool) error {
	res, err := c.openObjectStorageXML(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return cloudHTTPFailure(res)
	}
	return decodeObjectStorageXML(res.Body, dst, limit, allowEmpty)
}

func (c *Client) openObjectStorageXML(req *http.Request) (*http.Response, error) {
	req.Header.Set("Accept", "application/xml")
	res, err := c.http.Do(req)
	if err != nil {
		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		return nil, &provider.Failure{Kind: provider.Transient}
	}
	return res, nil
}

func decodeObjectStorageXML(source io.Reader, dst any, limit int64, allowEmpty bool) error {
	body := &io.LimitedReader{R: source, N: limit + 1}
	decoder := xml.NewDecoder(body)
	if err := decoder.Decode(dst); err != nil {
		if allowEmpty && err == io.EOF && decoder.InputOffset() == 0 {
			return nil
		}
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
