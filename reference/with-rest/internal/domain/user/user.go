// Package user contains the User domain model and its in-memory repository.
package user

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/birdordev/plumego/x/rest"
)

// User is a minimal domain model for the with-rest demo.
type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Repository is a thread-safe in-memory store that satisfies rest.Repository[User].
type Repository struct {
	mu    sync.RWMutex
	users map[string]User
	next  int
}

// NewRepository returns a Repository seeded with one record.
func NewRepository() *Repository {
	return &Repository{
		users: map[string]User{
			"u_1": {ID: "u_1", Name: "Ada"},
		},
		next: 2,
	}
}

func (r *Repository) FindAll(_ context.Context, _ *rest.QueryParams) ([]User, int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	users := make([]User, 0, len(r.users))
	for _, item := range r.users {
		users = append(users, item)
	}
	return users, int64(len(users)), nil
}

func (r *Repository) FindByID(_ context.Context, id string) (*User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	item, ok := r.users[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return &item, nil
}

func (r *Repository) Create(_ context.Context, item *User) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if item.ID == "" {
		item.ID = fmt.Sprintf("u_%d", r.next)
		r.next++
	}
	r.users[item.ID] = *item
	return nil
}

func (r *Repository) Update(_ context.Context, id string, item *User) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.users[id]; !ok {
		return sql.ErrNoRows
	}
	item.ID = id
	r.users[id] = *item
	return nil
}

func (r *Repository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.users[id]; !ok {
		return sql.ErrNoRows
	}
	delete(r.users, id)
	return nil
}

func (r *Repository) Count(_ context.Context, params *rest.QueryParams) (int64, error) {
	_, total, err := r.FindAll(context.Background(), params)
	return total, err
}

func (r *Repository) Exists(_ context.Context, id string) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.users[id]
	return ok, nil
}
