# Error Handling Example

Demonstrates common error handling patterns in Plumego: validation errors, request binding errors, panic recovery, and structured error responses.

## Run it

```bash
cd examples/error-handling
go run main.go
```

## Test valid request

```bash
curl -X POST http://localhost:8080/users \
  -H "Content-Type: application/json" \
  -d '{"name":"Alice","email":"alice@example.com","age":30}'
```

Response (201 Created):
```json
{"message":"user created","user":{"name":"Alice","email":"alice@example.com","age":30}}
```

## Test validation errors

Missing required field:

```bash
curl -X POST http://localhost:8080/users \
  -H "Content-Type: application/json" \
  -d '{"name":"Bob"}'
```

Response (400 Bad Request):
```json
{"error":"email is required"}
```

Invalid age:

```bash
curl -X POST http://localhost:8080/users \
  -H "Content-Type: application/json" \
  -d '{"name":"Charlie","email":"c@example.com","age":200}'
```

Response (400 Bad Request):
```json
{"error":"age must be between 0 and 150"}
```

## Test invalid JSON

```bash
curl -X POST http://localhost:8080/users \
  -H "Content-Type: application/json" \
  -d '{"name":"Dave"'  # Truncated JSON
```

Response (400 Bad Request):
```json
{"error":"unexpected end of JSON input"}
```

## Test path param validation

Valid ID:

```bash
curl http://localhost:8080/error/50
```

Response (200 OK):
```json
{"id":50}
```

Invalid ID format:

```bash
curl http://localhost:8080/error/abc
```

Response (400 Bad Request):
```json
{"error":"invalid ID format"}
```

Not found (simulated):

```bash
curl http://localhost:8080/error/200
```

Response (400 Bad Request):
```json
{"error":"user not found"}
```

## Test panic recovery

This handler panics, but the recovery middleware catches it:

```bash
curl http://localhost:8080/panic
```

Response (500 Internal Server Error):
```json
{"error":"internal server error"}
```

Check the server logs — you'll see the panic and stack trace.

## Key patterns

### Input validation

```go
if req.Name == "" {
    contract.WriteError(w, r, errors.New("name is required"))
    return
}
```

### JSON binding with error handling

```go
if err := contract.BindJSON(r, &req); err != nil {
    contract.WriteError(w, r, err)
    return
}
```

### Path param extraction with validation

```go
idStr := contract.Param(r, "id")
id, err := strconv.Atoi(idStr)
if err != nil {
    contract.WriteError(w, r, errors.New("invalid ID format"))
    return
}
```

### Panic recovery middleware

```go
app.Use(recovery.Handler())
```

This catches all panics, logs them, and returns a 500 error with a structured response.

## Copy these patterns

- Always check `contract.BindJSON()` error
- Use `contract.WriteError()` for all error responses (ensures consistent format)
- Validate inputs explicitly — don't assume the request is well-formed
- Use recovery middleware in production to handle unexpected panics

## Next steps

- Read `docs/modules/middleware/recovery/` for details on the recovery middleware
- Read `docs/modules/contract/` for all available response helpers
- See [the standard service tutorial](../../reference/standard-service/TUTORIAL.md) for a complete CRUD example with proper error handling in context
