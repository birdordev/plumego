module with-browser

go 1.26.0

require (
	github.com/birdordev/plumego v1.1.0
	github.com/coreos/go-oidc/v3 v3.21.0
	golang.org/x/oauth2 v0.36.0
)

require github.com/go-jose/go-jose/v4 v4.1.6 // indirect

replace github.com/birdordev/plumego => ../..
