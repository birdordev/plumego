package scaffold

import (
	"fmt"
)

func getCanonicalAPIHandlerContent(name string) string {
	return fmt.Sprintf(`// Package handler contains the HTTP handlers.
package handler

import (
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
)

// APIHandler handles the core JSON API endpoints.
type APIHandler struct{}

type helloResponse struct {
	Message   string            `+"`json:\"message\"`"+`
	Service   string            `+"`json:\"service\"`"+`
	Mode      string            `+"`json:\"mode\"`"+`
	Timestamp string            `+"`json:\"timestamp\"`"+`
	Version   string            `+"`json:\"version\"`"+`
	Features  []string          `+"`json:\"features\"`"+`
	Endpoints map[string]string `+"`json:\"endpoints\"`"+`
}

type greetResponse struct {
	Message string `+"`json:\"message\"`"+`
}

type statusResponse struct {
	Status    string          `+"`json:\"status\"`"+`
	Service   string          `+"`json:\"service\"`"+`
	Version   string          `+"`json:\"version\"`"+`
	Timestamp string          `+"`json:\"timestamp\"`"+`
	Structure statusStructure `+"`json:\"structure\"`"+`
	Modules   []string        `+"`json:\"modules\"`"+`
}

type statusStructure struct {
	Bootstrap  string `+"`json:\"bootstrap\"`"+`
	Extensions string `+"`json:\"extensions\"`"+`
	Handlers   string `+"`json:\"handlers\"`"+`
	Routes     string `+"`json:\"routes\"`"+`
}

// Hello responds with service metadata and available endpoints.
func (h APIHandler) Hello(w http.ResponseWriter, r *http.Request) {
	resp := helloResponse{
		Message:   "hello from %s standard-service",
		Service:   "%s",
		Mode:      "canonical",
		Timestamp: time.Now().Format(time.RFC3339),
		Version:   "1.0.0",
		Features: []string{
			"stable_root_only",
			"explicit_routes",
			"stdlib_handlers",
			"minimal_bootstrap",
		},
		Endpoints: map[string]string{
			"root":         "/",
			"healthz":      "/healthz",
			"readyz":       "/readyz",
			"api_hello":    "/api/hello",
			"api_status":   "/api/status",
			"api_greet":    "/api/v1/greet",
			"items_create": "/api/v1/items",
			"items_get":    "/api/v1/items/:id",
		},
	}
	_ = contract.WriteResponse(w, r, http.StatusOK, resp, nil)
}

// Greet demonstrates the canonical error response pattern.
func (h APIHandler) Greet(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeRequired).
			Detail("field", "name").
			Message("name is required").
			Build())
		return
	}
	_ = contract.WriteResponse(w, r, http.StatusOK, greetResponse{Message: "hello, " + name}, nil)
}

// Status responds with a summary of system health and component state.
func (h APIHandler) Status(w http.ResponseWriter, r *http.Request) {
	resp := statusResponse{
		Status:    "healthy",
		Service:   "%s",
		Version:   "1.0.0",
		Timestamp: time.Now().Format(time.RFC3339),
		Structure: statusStructure{
			Bootstrap:  "explicit",
			Extensions: "excluded_from_canonical_path",
			Handlers:   "net/http",
			Routes:     "one_method_one_path_one_handler",
		},
		Modules: []string{
			"core",
			"router",
			"contract",
			"middleware",
		},
	}
	_ = contract.WriteResponse(w, r, http.StatusOK, resp, nil)
}
`, name, name, name)
}

func getCanonicalItemDomainContent() string {
	return `// Package item contains the application item domain model and store.
package item

import (
	"fmt"
	"sync"
	"time"
)

// Item is the sample item resource in this application.
type Item struct {
	ID        string ` + "`json:\"id\"`" + `
	Name      string ` + "`json:\"name\"`" + `
	CreatedAt string ` + "`json:\"created_at\"`" + `
}

// MemoryStore is a thread-safe in-memory item repository.
type MemoryStore struct {
	mu    sync.RWMutex
	items map[string]Item
	next  int
}

// NewMemoryStore returns a ready-to-use in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{items: make(map[string]Item)}
}

// Create stores an item with the provided name.
func (s *MemoryStore) Create(name string) Item {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.next++
	item := Item{
		ID:        fmt.Sprintf("item-%d", s.next),
		Name:      name,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	s.items[item.ID] = item
	return item
}

// Get returns an item by id.
func (s *MemoryStore) Get(id string) (Item, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	item, ok := s.items[id]
	return item, ok
}

func (s *MemoryStore) Update(id, name string) (Item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if ok {
		item.Name = name
		s.items[id] = item
	}
	return item, ok
}

func (s *MemoryStore) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.items[id]
	delete(s.items, id)
	return ok
}
`
}

func getCanonicalItemHandlerContent(module string) string {
	return fmt.Sprintf(`package handler

import (
	"net/http"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/router"
	"%s/internal/domain/item"
)

// ItemRepository is the minimal persistence contract that ItemHandler depends on.
type ItemRepository interface {
	Create(name string) item.Item
	Get(id string) (item.Item, bool)
	Update(id, name string) (item.Item, bool)
	Delete(id string) bool
}

// ItemHandler demonstrates constructor injection for a domain repository.
type ItemHandler struct {
	Repo ItemRepository
}

type createItemReq struct {
	Name string `+"`json:\"name\"`"+`
}

// Create 处理 POST /api/v1/items。
func (h ItemHandler) Create(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeItemName(w, r)
	if !ok {
		return
	}
	item := h.Repo.Create(name)
	_ = contract.WriteResponse(w, r, http.StatusCreated, item, nil)
}

func decodeItemName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req createItemReq
	if err := contract.DecodeJSONRequest(r, &req, contract.JSONRequestOptions{}); err != nil {
		apiErr, _ := contract.AsAPIError(err)
		_ = contract.WriteError(w, r, apiErr)
		return "", false
	}
	if req.Name == "" {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeRequired).
			Detail("field", "name").
			Message("name is required").
			Build())
		return "", false
	}
	return req.Name, true
}

// GetByID handles GET /api/v1/items/:id.
func (h ItemHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id := router.Param(r, "id")
	item, ok := h.Repo.Get(id)
	if !ok {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeNotFound).
			Detail("id", id).
			Message("item not found").
			Build())
		return
	}
	_ = contract.WriteResponse(w, r, http.StatusOK, item, nil)
}

// Update 替换条目的名称。
func (h ItemHandler) Update(w http.ResponseWriter, r *http.Request) {
	name, ok := decodeItemName(w, r)
	if !ok {
		return
	}
	item, ok := h.Repo.Update(router.Param(r, "id"), name)
	if !ok {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeNotFound).Message("item not found").Build())
		return
	}
	_ = contract.WriteResponse(w, r, http.StatusOK, item, nil)
}

// Delete 删除指定条目。
func (h ItemHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.Repo.Delete(router.Param(r, "id")) {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeNotFound).Message("item not found").Build())
		return
	}
	_ = contract.WriteResponse(w, r, http.StatusNoContent, nil, nil)
}
`, module)
}

func getAPIUsersResourceContent() string {
	return `package resource

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/birdordev/plumego/x/rest"
)

// User is the sample resource exposed by the API template.
type User struct {
	ID    string ` + "`json:\"id\"`" + `
	Name  string ` + "`json:\"name\"`" + `
	Email string ` + "`json:\"email\"`" + `
}

// UserRepository is an in-memory rest.Repository implementation.
type UserRepository struct {
	mu    sync.Mutex
	seq   int
	items map[string]User
}

// NewUserRepository returns a seeded in-memory repository.
func NewUserRepository() *UserRepository {
	repo := &UserRepository{items: make(map[string]User)}
	_ = repo.Create(context.Background(), &User{Name: "Ada", Email: "ada@example.com"})
	return repo
}

// FindAll returns all users.
func (r *UserRepository) FindAll(_ context.Context, _ *rest.QueryParams) ([]User, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	users := make([]User, 0, len(r.items))
	for _, user := range r.items {
		users = append(users, user)
	}
	return users, int64(len(users)), nil
}

// FindByID returns one user by ID.
func (r *UserRepository) FindByID(_ context.Context, id string) (*User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	user, ok := r.items[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	return &user, nil
}

// Create inserts a user.
func (r *UserRepository) Create(_ context.Context, data *User) error {
	if data == nil {
		return fmt.Errorf("user is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if data.ID == "" {
		r.seq++
		data.ID = fmt.Sprintf("%d", r.seq)
	}
	r.items[data.ID] = *data
	return nil
}

// Update replaces a user.
func (r *UserRepository) Update(_ context.Context, id string, data *User) error {
	if data == nil {
		return fmt.Errorf("user is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.items[id]; !ok {
		return sql.ErrNoRows
	}
	data.ID = id
	r.items[id] = *data
	return nil
}

// Delete removes a user.
func (r *UserRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.items[id]; !ok {
		return sql.ErrNoRows
	}
	delete(r.items, id)
	return nil
}

// Count returns the number of users.
func (r *UserRepository) Count(_ context.Context, _ *rest.QueryParams) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return int64(len(r.items)), nil
}

// Exists reports whether a user exists.
func (r *UserRepository) Exists(_ context.Context, id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.items[id]
	return ok, nil
}
`
}

func getCanonicalHealthHandlerContent() string {
	return `package handler

import (
	"net/http"
	"time"

	"github.com/birdordev/plumego/contract"
	"github.com/birdordev/plumego/health"
)

// HealthHandler serves liveness and readiness endpoints.
type HealthHandler struct {
	ServiceName string
	Checkers []health.ComponentChecker
}

type healthResponse struct {
	Status    string ` + "`json:\"status\"`" + `
	Service   string ` + "`json:\"service\"`" + `
	Check     string ` + "`json:\"check\"`" + `
	Timestamp string ` + "`json:\"timestamp\"`" + `
}

// Live reports that the process is serving HTTP traffic.
func (h HealthHandler) Live(w http.ResponseWriter, r *http.Request) {
	service := h.ServiceName
	if service == "" {
		service = "plumego-reference"
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, healthResponse{
		Status:    "ok",
		Service:   service,
		Check:     "liveness",
		Timestamp: time.Now().Format(time.RFC3339),
	}, nil)
}

// Ready reports that the application is ready to accept requests.
func (h HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	failed := make(map[string]bool)
	for _, checker := range h.Checkers {
		if err := checker.Check(r.Context()); err != nil {
			failed[checker.Name()] = false
		}
	}
	if len(failed) != 0 {
		_ = contract.WriteError(w, r, contract.NewErrorBuilder().
			Type(contract.TypeUnavailable).
			Message("one or more components are not ready").
			Detail("components", failed).
			Build())
		return
	}
	service := h.ServiceName
	if service == "" {
		service = "plumego-reference"
	}

	_ = contract.WriteResponse(w, r, http.StatusOK, healthResponse{
		Status:    "ready",
		Service:   service,
		Check:     "readiness",
		Timestamp: time.Now().Format(time.RFC3339),
	}, nil)
}
`
}

func getCanonicalConfigGoContent(_ string) string {
	return `// Package config loads and validates the application configuration.
package config

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/birdordev/plumego/core"
)

// Config holds all application configuration.
type Config struct {
	Core core.AppConfig
	App  AppConfig
}

// AppConfig holds app-local, non-kernel configuration.
type AppConfig struct {
	EnvFile         string
	Debug           bool
	ShutdownTimeout time.Duration
}

// Defaults returns safe configuration values for local development.
func Defaults() Config {
	coreCfg := core.DefaultConfig()
	coreCfg.Addr = ":8080"
	return Config{
		Core: coreCfg,
		App: AppConfig{
			EnvFile:         ".env",
			ShutdownTimeout: 5 * time.Second,
		},
	}
}

// Load reads configuration from environment variables and flags.
func Load() (Config, error) {
	return load(os.Args, os.LookupEnv)
}

func load(args []string, lookupEnv func(string) (string, bool)) (Config, error) {
	cfg := Defaults()
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}

	cfg.App.EnvFile = resolveEnvFile(args, lookupEnv, cfg.App.EnvFile)
	fileEnv, err := readEnvFile(cfg.App.EnvFile)
	if err != nil {
		return cfg, err
	}

	if err := applyEnv(&cfg, func(key string) (string, bool) {
		value, ok := fileEnv[key]
		return value, ok
	}); err != nil {
		return cfg, err
	}
	if err := applyEnv(&cfg, lookupEnv); err != nil {
		return cfg, err
	}
	if err := applyFlags(&cfg, args); err != nil {
		return cfg, err
	}

	return cfg, Validate(cfg)
}

// Validate returns an error if cfg is unusable.
func Validate(cfg Config) error {
	if cfg.Core.Addr == "" {
		return fmt.Errorf("addr is required")
	}
	if cfg.App.ShutdownTimeout <= 0 {
		return fmt.Errorf("shutdown timeout must be positive")
	}
	return nil
}

func applyEnv(cfg *Config, lookupEnv func(string) (string, bool)) error {
	if value, ok := lookupEnv("APP_ADDR"); ok && strings.TrimSpace(value) != "" {
		cfg.Core.Addr = value
	}
	if value, ok := lookupEnv("APP_ENV_FILE"); ok && strings.TrimSpace(value) != "" {
		cfg.App.EnvFile = value
	}
	if value, ok := lookupEnv("APP_DEBUG"); ok && strings.TrimSpace(value) != "" {
		cfg.App.Debug = value == "true" || value == "1"
	}
	if value, ok := lookupEnv("APP_SHUTDOWN_TIMEOUT_MS"); ok {
		ms, err := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
		if err != nil || ms <= 0 {
			return fmt.Errorf("APP_SHUTDOWN_TIMEOUT_MS must be a positive integer")
		}
		cfg.App.ShutdownTimeout = time.Duration(ms) * time.Millisecond
	}
	return nil
}

func applyFlags(cfg *Config, args []string) error {
	fs := flag.NewFlagSet("app", flag.ContinueOnError)
	fs.StringVar(&cfg.Core.Addr, "addr", cfg.Core.Addr, "listen address")
	fs.StringVar(&cfg.App.EnvFile, "env-file", cfg.App.EnvFile, "path to .env file")
	fs.BoolVar(&cfg.App.Debug, "debug", cfg.App.Debug, "enable debug mode")
	if len(args) == 0 {
		return fs.Parse(nil)
	}
	return fs.Parse(configFlagArgs(args[1:]))
}

func configFlagArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, hasValue := flagName(arg)
		switch name {
		case "addr", "env-file":
			out = append(out, arg)
			if !hasValue && i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		case "debug":
			out = append(out, arg)
			if !hasValue && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				out = append(out, args[i])
			}
		}
	}
	return out
}

func flagName(arg string) (name string, hasValue bool) {
	if strings.HasPrefix(arg, "--") {
		arg = strings.TrimPrefix(arg, "--")
	} else if strings.HasPrefix(arg, "-") {
		arg = strings.TrimPrefix(arg, "-")
	} else {
		return "", false
	}
	if idx := strings.IndexByte(arg, '='); idx >= 0 {
		return arg[:idx], true
	}
	return arg, false
}

func resolveEnvFile(args []string, lookupEnv func(string) (string, bool), defaultPath string) string {
	if envPath, ok := lookupEnv("APP_ENV_FILE"); ok && strings.TrimSpace(envPath) != "" {
		defaultPath = envPath
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--env-file" || arg == "-env-file" {
			if i+1 < len(args) {
				return args[i+1]
			}
		}
		if strings.HasPrefix(arg, "--env-file=") {
			return strings.TrimPrefix(arg, "--env-file=")
		}
		if strings.HasPrefix(arg, "-env-file=") {
			return strings.TrimPrefix(arg, "-env-file=")
		}
	}
	return defaultPath
}

func readEnvFile(path string) (map[string]string, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		key, value, ok := parseEnvLine(scanner.Text())
		if !ok {
			continue
		}
		values[key] = value
	}
	return values, scanner.Err()
}

func parseEnvLine(line string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}

	idx := strings.IndexByte(line, '=')
	if idx < 0 {
		return "", "", false
	}

	key = strings.TrimSpace(line[:idx])
	if key == "" {
		return "", "", false
	}

	value = strings.TrimSpace(line[idx+1:])
	if len(value) >= 2 {
		q := value[0]
		if (q == '"' || q == '\'') && value[len(value)-1] == q {
			value = value[1 : len(value)-1]
			value = strings.ReplaceAll(value, string([]byte{'\\', q}), string(q))
		}
	}

	return key, value, true
}
	`
}
