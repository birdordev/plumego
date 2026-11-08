# Migrating from http.ServeMux

This guide helps developers move from Go's standard library `http.ServeMux` to Plumego.

Good news: Plumego builds on the same shapes as stdlib, so migration is straightforward.

## What Stays the Same

### Handler signatures

```go
// stdlib
func MyHandler(w http.ResponseWriter, r *http.Request) {
    fmt.Fprint(w, "hello")
}

// Plumego (same!)
func MyHandler(w http.ResponseWriter, r *http.Request) {
    fmt.Fprint(w, "hello")
}
```

Plumego handlers are ordinary `http.Handler` functions. No special context wrapping, no custom types.

### Middleware patterns

```go
// stdlib middleware
func LoggingMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        log.Println(r.Method, r.URL.Path)
        next.ServeHTTP(w, r)
    })
}

// Plumego (same!)
func LoggingMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        log.Println(r.Method, r.URL.Path)
        next.ServeHTTP(w, r)
    })
}
```

### Testing

```go
// stdlib
func TestMyHandler(t *testing.T) {
    req := httptest.NewRequest("GET", "/", nil)
    w := httptest.NewRecorder()
    MyHandler(w, req)
}

// Plumego (same!)
func TestMyHandler(t *testing.T) {
    req := httptest.NewRequest("GET", "/", nil)
    w := httptest.NewRecorder()
    MyHandler(w, req)
}
```

## What's Different (Better)

### 1. Route Registration

**Before (stdlib):**
```go
mux := http.NewServeMux()

// Register handlers by path
mux.HandleFunc("GET /users", listUsers)
mux.HandleFunc("POST /users", createUser)
mux.HandleFunc("GET /users/{id}", getUser)
mux.HandleFunc("PUT /users/{id}", updateUser)
mux.HandleFunc("DELETE /users/{id}", deleteUser)

log.Fatal(http.ListenAndServe(":8080", mux))
```

**After (Plumego):**
```go
app := plumego.New()

// Clearer HTTP method API
app.Get("/users", listUsers)
app.Post("/users", createUser)
app.Get("/users/{id}", getUser)
app.Put("/users/{id}", updateUser)
app.Delete("/users/{id}", deleteUser)

log.Fatal(http.ListenAndServe(":8080", app))
```

**Why better:** Explicit method names instead of string patterns.

### 2. Route Groups & Common Prefix

**Before (stdlib):**
```go
// Repetitive prefixes
mux.HandleFunc("GET /api/v1/users", listUsers)
mux.HandleFunc("POST /api/v1/users", createUser)
mux.HandleFunc("GET /api/v1/posts", listPosts)
mux.HandleFunc("POST /api/v1/posts", createPost)
```

**After (Plumego):**
```go
// Use groups for common prefix
api := app.Group("/api/v1")
{
    users := api.Group("/users")
    users.Get("", listUsers)
    users.Post("", createUser)

    posts := api.Group("/posts")
    posts.Get("", listPosts)
    posts.Post("", createPost)
}
```

**Why better:** DRY principle, clear hierarchy, easier to refactor.

### 3. Path Parameters

**Before (stdlib):**
```go
// Manual parsing
func getUser(w http.ResponseWriter, r *http.Request) {
    // Stdlib doesn't extract path params automatically
    // You have to parse the path manually:
    parts := strings.Split(r.URL.Path, "/")
    id := parts[len(parts)-1]  // Error-prone!
    
    // Or use a router library alongside stdlib
}
```

**After (Plumego):**
```go
// Automatic extraction via context
func getUser(w http.ResponseWriter, r *http.Request) {
    id := contract.Param(r, "id")  // Clean, type-safe
    // ...
}

app.Get("/users/{id}", getUser)
```

**Why better:** No manual string parsing, less error-prone.

### 4. Response Writing

**Before (stdlib):**
```go
// Manual JSON encoding
func listUsers(w http.ResponseWriter, r *http.Request) {
    users := []User{{ID: 1, Name: "Alice"}}
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(users)
}

// Manual error response
func createUser(w http.ResponseWriter, r *http.Request) {
    if err := validateUser(data); err != nil {
        w.WriteHeader(http.StatusBadRequest)
        json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
        return
    }
}
```

**After (Plumego):**
```go
// Structured response helpers
func listUsers(w http.ResponseWriter, r *http.Request) {
    users := []User{{ID: 1, Name: "Alice"}}
    contract.WriteResponse(w, r, http.StatusOK, users, nil)
}

// Consistent error responses
func createUser(w http.ResponseWriter, r *http.Request) {
    if err := validateUser(data); err != nil {
        contract.WriteError(w, r, err)
        return
    }
}
```

**Why better:** Less boilerplate, consistent error format across your service.

### 5. Middleware Order

**Before (stdlib):**
```go
// Stdlib doesn't have a clean middleware stack
mux := http.NewServeMux()
// ... register handlers ...

// Wrap mux with middleware manually
handler := loggingMiddleware(recoveryMiddleware(mux))
log.Fatal(http.ListenAndServe(":8080", handler))
```

**After (Plumego):**
```go
// Clean middleware chain
app := plumego.New()
app.Use(recovery.Handler())      // First: catch panics
app.Use(requestid.Handler())     // Add request ID
app.Use(accesslog.Handler(log))  // Log requests
// ... register routes ...
log.Fatal(http.ListenAndServe(":8080", app))
```

**Why better:** Middleware order is explicit and clear.

### 6. Route Freeze

**Before (stdlib):**
```go
mux := http.NewServeMux()

// Routes can be registered at any time:
go func() {
    time.Sleep(5 * time.Second)
    mux.HandleFunc("GET /new", newHandler)  // Added after server started!
}()

log.Fatal(http.ListenAndServe(":8080", mux))
```

**After (Plumego):**
```go
app := plumego.New()
// ... register all routes upfront ...
app.Prepare()  // Freeze routes, validate, optimize

log.Fatal(http.ListenAndServe(":8080", app))
```

**Why better:** Route registration is finalized before serving, preventing subtle bugs.

## Step-by-Step Migration

### Step 1: Replace ServeMux with plumego.New()

**Before:**
```go
mux := http.NewServeMux()
```

**After:**
```go
app := plumego.New()
```

### Step 2: Convert HandleFunc to method calls

**Before:**
```go
mux.HandleFunc("GET /users", listUsers)
mux.HandleFunc("POST /users", createUsers)
mux.HandleFunc("DELETE /users/{id}", deleteUser)
```

**After:**
```go
app.Get("/users", listUsers)
app.Post("/users", createUsers)
app.Delete("/users/{id}", deleteUser)
```

### Step 3: Convert path param extraction

**Before:**
```go
func getUser(w http.ResponseWriter, r *http.Request) {
    // Parse path manually
    id := strings.TrimPrefix(r.URL.Path, "/users/")
    user, err := db.GetUser(id)
    // ...
}
```

**After:**
```go
func getUser(w http.ResponseWriter, r *http.Request) {
    // Get param from context
    id := contract.Param(r, "id")
    user, err := db.GetUser(id)
    // ...
}
```

### Step 4: Convert JSON response writing

**Before:**
```go
w.Header().Set("Content-Type", "application/json")
json.NewEncoder(w).Encode(users)
```

**After:**
```go
contract.WriteResponse(w, r, http.StatusOK, users, nil)
```

### Step 5: Convert error responses

**Before:**
```go
if err != nil {
    w.WriteHeader(http.StatusBadRequest)
    json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
    return
}
```

**After:**
```go
if err != nil {
    contract.WriteError(w, r, err)
    return
}
```

### Step 6: Add middleware

**Before:**
```go
handler := loggingMiddleware(recoveryMiddleware(mux))
log.Fatal(http.ListenAndServe(":8080", handler))
```

**After:**
```go
app.Use(recovery.Handler())
app.Use(accesslog.Handler(logger))
log.Fatal(http.ListenAndServe(":8080", app))
```

### Step 7: Test handlers (no changes needed!)

Your handlers are still ordinary `http.Handler` functions, so tests don't change:

```go
func TestGetUser(t *testing.T) {
    req := httptest.NewRequest("GET", "/users/123", nil)
    w := httptest.NewRecorder()
    getUser(w, req)
    
    if w.Code != http.StatusOK {
        t.Errorf("got %d, want 200", w.Code)
    }
}
```

## Common Patterns

### Request binding with validation

**stdlib way:**
```go
var user struct {
    Name  string
    Email string
}
if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
    http.Error(w, "bad request", http.StatusBadRequest)
    return
}
if user.Name == "" {
    http.Error(w, "name required", http.StatusBadRequest)
    return
}
```

**Plumego way:**
```go
var user struct {
    Name  string
    Email string
}
if err := contract.BindJSON(r, &user); err != nil {
    contract.WriteError(w, r, err)
    return
}
if user.Name == "" {
    contract.WriteError(w, r, errors.New("name required"))
    return
}
```

### Health check endpoint

**stdlib way:**
```go
mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]string{"status": "healthy"})
})
```

**Plumego way:**
```go
app.Get("/health", func(w http.ResponseWriter, r *http.Request) {
    contract.WriteResponse(w, r, http.StatusOK, 
        map[string]string{"status": "healthy"}, nil)
})

// Or better, use the health module:
import "github.com/birdordev/plumego/health"

app.Get("/health", func(w http.ResponseWriter, r *http.Request) {
    h := health.Status{Status: health.StatusHealthy}
    contract.WriteResponse(w, r, http.StatusOK, h, nil)
})
```

### Graceful shutdown

**stdlib way:**
```go
server := &http.Server{Addr: ":8080", Handler: mux}
go server.ListenAndServe()

sigChan := make(chan os.Signal, 1)
signal.Notify(sigChan, os.Interrupt)
<-sigChan

ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
server.Shutdown(ctx)
```

**Plumego way:**
```go
app := plumego.New()
// ... register routes ...
app.Prepare()

server := app.Server()
go server.ListenAndServe()

sigChan := make(chan os.Signal, 1)
signal.Notify(sigChan, os.Interrupt)
<-sigChan

ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
server.Shutdown(ctx)
```

## Staying stdlib-compatible

Plumego handlers, middleware, and tests are 100% stdlib-compatible. You can:

- Mix Plumego routes with third-party stdlib middleware
- Use `httptest` testing patterns (no special test helpers)
- Deploy to any platform that supports `net/http.Handler`
- Gradually migrate parts of your service to Plumego

## Next Steps

1. Copy your stdlib handlers to a Plumego project (they're already compatible!)
2. Replace `mux.HandleFunc()` with `app.Get()`, `app.Post()`, etc.
3. Convert path param extraction to use `contract.Param()`
4. Convert response writing to use `contract.WriteResponse()` and `contract.WriteError()`
5. Add middleware with `app.Use()`
6. Run your tests (they work unchanged!)

See `docs/start/adoption-path.md` for the full learning path.

---

For the canonical Plumego layout, see `reference/standard-service/`.  
For response helpers, see `docs/modules/contract/README.md`.  
For middleware, see `docs/modules/middleware/README.md`.
