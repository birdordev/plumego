package jwt_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/birdordev/plumego/security/jwt"
)

type exampleKeyStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

// Verify is a test-only documentation anchor for ExampleVerify.
func Verify() {}

func newExampleKeyStore() *exampleKeyStore {
	return &exampleKeyStore{data: make(map[string][]byte)}
}

func (s *exampleKeyStore) GetContext(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.data[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return append([]byte(nil), value...), nil
}

func (s *exampleKeyStore) SetContext(_ context.Context, key string, value []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = append([]byte(nil), value...)
	return nil
}

func (s *exampleKeyStore) KeysContext(context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.data))
	for key := range s.data {
		keys = append(keys, key)
	}
	return keys, nil
}

func ExampleVerify() {
	ctx := context.Background()
	manager, err := jwt.NewJWTManager(newExampleKeyStore(), jwt.DefaultJWTConfig())
	if err != nil {
		panic(err)
	}

	pair, err := manager.GenerateTokenPair(ctx,
		jwt.IdentityClaims{Subject: "user-123"},
		jwt.AuthorizationClaims{Roles: []string{"admin"}},
	)
	if err != nil {
		panic(err)
	}

	claims, err := manager.VerifyToken(ctx, pair.AccessToken, jwt.TokenTypeAccess)
	if err != nil {
		panic(err)
	}

	fmt.Println(claims.Identity.Subject)
	fmt.Println(claims.TokenType)

	// Output:
	// user-123
	// access
}
