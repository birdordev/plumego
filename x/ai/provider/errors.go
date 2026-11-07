package provider

import "io"

// maxErrorBodyBytes bounds how much of a non-2xx response body is read for
// inclusion in the returned error. Providers occasionally return large error
// payloads; without a limit a hostile or misbehaving upstream could inflate
// memory on every failure.
const maxErrorBodyBytes = 64 << 10 // 64 KiB

// readErrorBody drains at most maxErrorBodyBytes from r and returns it as a
// string for embedding in an error. Read failures yield an empty string so the
// primary error stays informative.
func readErrorBody(r io.Reader) string {
	body, err := io.ReadAll(io.LimitReader(r, maxErrorBodyBytes))
	if err != nil {
		return ""
	}
	return string(body)
}
