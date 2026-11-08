# Build context must be the repository root so the replace directive in go.mod
# can resolve github.com/birdordev/plumego to the local source tree:
#
#   docker build -f reference/standard-service/Dockerfile \
#                --build-arg VERSION=$(git describe --tags --always) \
#                -t standard-service .
#
# In production set VERSION to the release tag or commit SHA so the /api/info
# and /api/hello endpoints report the correct build identity.

FROM golang:1.26-alpine AS builder
WORKDIR /repo
# Copy the full monorepo so the replace directive in go.mod resolves correctly.
COPY . .
WORKDIR /repo/reference/standard-service
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
        -ldflags "-X main.version=${VERSION}" \
        -o /bin/service \
        .

FROM scratch
COPY --from=builder /bin/service /bin/service
# Default listen address. Override with -e APP_ADDR=:9090 at runtime.
EXPOSE 8080
ENTRYPOINT ["/bin/service"]
