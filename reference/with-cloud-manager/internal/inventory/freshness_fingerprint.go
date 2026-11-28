package inventory

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"with-cloud-manager/migrations"
)

type FreshnessScopeKey struct {
	Provider string `json:"provider"`
	Account  string `json:"account"`
	Region   string `json:"region"`
	Kind     string `json:"kind"`
}

const freshnessFingerprintVersion = "scope-v1:"

func FreshnessScopeFingerprint(scopes []FreshnessScopeKey) (string, error) {
	keys := slices.Clone(scopes)
	slices.SortFunc(keys, func(a, b FreshnessScopeKey) int {
		return cmp.Or(cmp.Compare(a.Provider, b.Provider), cmp.Compare(a.Account, b.Account),
			cmp.Compare(a.Region, b.Region), cmp.Compare(a.Kind, b.Kind))
	})
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	for i, key := range keys {
		if key.Provider == "" || key.Account == "" || key.Kind == "" || (i > 0 && key == keys[i-1]) {
			return "", ErrInvalidQuery
		}
		if err := encodeFreshnessScope(encoder, key); err != nil {
			return "", err
		}
	}
	return freshnessFingerprintVersion + hex.EncodeToString(hash.Sum(nil)), nil
}

func ValidFreshnessScopeFingerprint(value string) bool {
	digest, ok := strings.CutPrefix(value, freshnessFingerprintVersion)
	if !ok || len(digest) != sha256.Size*2 || strings.ToLower(digest) != digest {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func encodeFreshnessScope(encoder *json.Encoder, key FreshnessScopeKey) error {
	return encoder.Encode([4]string{key.Provider, key.Account, key.Region, key.Kind})
}

func readFreshnessFingerprint(ctx context.Context, tx *sql.Tx, args []any) (string, error) {
	// C 排序与 Go 的字节顺序一致，确保异地核对同一清单时指纹相同。
	rows, err := tx.QueryContext(ctx, migrations.Freshness()+`
		SELECT provider,account_id,region,kind FROM freshness
		ORDER BY provider COLLATE "C",account_id COLLATE "C",region COLLATE "C",kind COLLATE "C"`, args...)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	for rows.Next() {
		var key FreshnessScopeKey
		if err := rows.Scan(&key.Provider, &key.Account, &key.Region, &key.Kind); err != nil {
			return "", err
		}
		if err := encodeFreshnessScope(encoder, key); err != nil {
			return "", err
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return freshnessFingerprintVersion + hex.EncodeToString(hash.Sum(nil)), nil
}
