# Error Handling Example

Demonstrates common error handling patterns in Plumego: validation errors, request binding errors, panic recovery, and structured error responses.

## Run it

The example uses Plumego from this checkout.

```bash
cd examples/error-handling
go run .
```

## Test valid request

```bash
curl -X POST http://localhost:8080/users \
  -H "Content-Type: application/json" \
  -d '{"name":"Alice","email":"alice@example.com","age":30}'
```

Response (201 Created):
```json
{"data":{"message":"user created","user":{"name":"Alice","email":"alice@example.com","age":30}}}
```

## Test validation errors

Missing required field:

```bash
curl -X POST http://localhost:8080/users \
  -H "Content-Type: application/json" \
  -d '{"name":"Bob"}'
```

Response excerpt (400 Bad Request):
```json
{"error":{"message":"email is required"}}
```

Invalid age:

```bash
curl -X POST http://localhost:8080/users \
  -H "Content-Type: application/json" \
  -d '{"name":"Charlie","email":"c@example.com","age":200}'
```

Response excerpt (400 Bad Request):
```json
{"error":{"message":"age must be between 0 and 150"}}
```

## Test invalid JSON

```bash
curl -X POST http://localhost:8080/users \
  -H "Content-Type: application/json" \
  -d '{"name":"Dave"'  # Truncated JSON
```

Response excerpt (400 Bad Request):
```json
{"error":{"message":"invalid JSON request body"}}
```

## Test path param validation

Valid ID:

```bash
curl http://localhost:8080/error/50
```

Response (200 OK):
```json
{"data":{"id":50}}
```

Invalid ID format:

```bash
curl http://localhost:8080/error/abc
```

Response excerpt (400 Bad Request):
```json
{"error":{"message":"invalid ID format"}}
```

Not found (simulated):

```bash
curl http://localhost:8080/error/200
```

Response excerpt (404 Not Found):
```json
{"error":{"message":"user not found"}}
```

## Test panic recovery

This handler panics, but the recovery middleware catches it:

```bash
curl http://localhost:8080/panic
```

Response excerpt (500 Internal Server Error):
```json
{"error":{"message":"internal server error"}}
```

Check the server logs for the recovery diagnostic.

## Key patterns

### Input validation

```go
if req.Name == "" {
    _ = contract.WriteError(w, r, contract.NewErrorBuilder().
        Type(contract.TypeRequired).Message("name is required").Build())
    return
}
```

### JSON binding with error handling

```go
if err := contract.DecodeJSONRequest(r, &req, contract.JSONRequestOptions{}); err != nil {
    apiErr, _ := contract.AsAPIError(err)
    _ = contract.WriteError(w, r, apiErr)
    return
}
```

### Path param extraction with validation

```go
idStr := router.Param(r, "id")
id, err := strconv.Atoi(idStr)
if err != nil {
    _ = contract.WriteError(w, r, contract.NewErrorBuilder().
        Type(contract.TypeBadRequest).Message("invalid ID format").Build())
    return
}
```

### Panic recovery middleware

```go
recoverPanics, err := recovery.Middleware(recovery.Config{Logger: app.Logger()})
if err != nil {
    log.Fatal(err)
}
if err := app.Use(recoverPanics); err != nil {
    log.Fatal(err)
}
```

This catches all panics, logs them, and returns a 500 error with a structured response.

## Copy these patterns

- Always check `contract.DecodeJSONRequest()` error
- Use `contract.WriteError()` for all error responses (ensures consistent format)
- Validate inputs explicitly — don't assume the request is well-formed
- Use recovery middleware in production to handle unexpected panics

## Next steps

- Read `docs/modules/middleware/recovery/` for details on the recovery middleware
- Read `docs/modules/contract/` for all available response helpers
- See [the standard service tutorial](../../reference/standard-service/TUTORIAL.md) for a complete CRUD example with proper error handling in context
