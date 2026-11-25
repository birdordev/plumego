package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"math/bits"
	"strings"
	"time"
)

type SerialReceipt struct {
	Host       string    `json:"host"`
	Username   string    `json:"username"`
	Port       int       `json:"port"`
	ValidUntil time.Time `json:"valid_until"`
	RequestID  string    `json:"provider_request_id"`
}

type SerialDispatch func(context.Context) (SerialReceipt, error)

func NormalizeSerialPublicKey(raw string) (string, [32]byte, error) {
	invalid := &Failure{Kind: InvalidResponse}
	raw = strings.TrimSpace(raw)
	if len(raw) > 4096 || strings.ContainsAny(raw, "\r\n\x00") {
		return "", [32]byte{}, invalid
	}
	fields := strings.Fields(raw)
	if len(fields) < 2 || fields[0] != "ssh-rsa" {
		return "", [32]byte{}, invalid
	}
	blob, err := base64.StdEncoding.Strict().DecodeString(fields[1])
	if err != nil {
		return "", [32]byte{}, invalid
	}
	keyType, rest := sshField(blob)
	exponent, rest := sshField(rest)
	modulus, remainder := sshField(rest)
	if string(keyType) != "ssh-rsa" || !bytes.Equal(exponent, []byte{1, 0, 1}) || len(remainder) != 0 ||
		!validSerialModulus(modulus) {
		return "", [32]byte{}, invalid
	}
	return "ssh-rsa " + base64.StdEncoding.EncodeToString(blob), sha256.Sum256(blob), nil
}

func sshField(data []byte) ([]byte, []byte) {
	if len(data) < 4 {
		return nil, nil
	}
	n := binary.BigEndian.Uint32(data[:4])
	if n == 0 || uint64(n) > uint64(len(data)-4) {
		return nil, nil
	}
	return data[4 : 4+n], data[4+n:]
}

func validSerialModulus(data []byte) bool {
	if len(data) < 256 || len(data) > 513 || data[0]&0x80 != 0 || data[len(data)-1]&1 == 0 {
		return false
	}
	if data[0] == 0 {
		if data[1]&0x80 == 0 {
			return false
		}
		data = data[1:]
	}
	length := (len(data)-1)*8 + bits.Len8(data[0])
	return length >= 2048 && length <= 4096
}
