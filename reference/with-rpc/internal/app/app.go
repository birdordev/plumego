// Package app wires the with-rpc demo: a gRPC server connected to a
// plumego HTTP app via an in-process bufconn and grpc-gateway transcoder.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/birdordev/plumego/core"
	rpcgateway "github.com/birdordev/plumego/x/rpc/gateway"
	rpcserver "github.com/birdordev/plumego/x/rpc/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	rpcinternal "with-rpc/internal/grpc"
)

const bufSize = 1024 * 1024

// App holds the gRPC server, transcoder, and HTTP app.
type App struct {
	grpcServer *rpcserver.Server
	lis        *bufconn.Listener
	conn       *grpc.ClientConn
	transcoder *rpcgateway.HTTPTranscoder
	Core       *core.App
}

// New creates the in-process gRPC server, dials it, and wires the HTTP
// transcoder. Call RegisterRoutes then use Core directly or call Start.
func New(ctx context.Context) (*App, error) {
	lis := bufconn.Listen(bufSize)
	grpcServer := rpcserver.New(rpcinternal.Runtime{Server: grpc.NewServer()})
	if err := grpcServer.RegisterService(&rpcinternal.HelloServiceDesc, rpcinternal.HelloService{}); err != nil {
		return nil, fmt.Errorf("register grpc service: %w", err)
	}
	go func() { _ = grpcServer.Serve(lis) }()

	conn, err := grpc.DialContext(ctx, "passthrough:///bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("dial grpc server: %w", err)
	}

	transcoder := rpcgateway.New("bufconn")
	if err := transcoder.Register(ctx, adaptHandler(helloGatewayHandler(conn)), "GET /v1/hello"); err != nil {
		return nil, fmt.Errorf("register transcoder: %w", err)
	}

	cfg := core.DefaultConfig()
	cfg.Addr = "127.0.0.1:0"
	httpApp := core.New(cfg, core.AppDependencies{})

	return &App{
		grpcServer: grpcServer,
		lis:        lis,
		conn:       conn,
		transcoder: transcoder,
		Core:       httpApp,
	}, nil
}

// RegisterRoutes wires the HTTP → gRPC transcoding route onto Core.
func (a *App) RegisterRoutes() error {
	return a.Core.Get("/v1/hello", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.transcoder.Handler().ServeHTTP(w, r)
	}))
}

// Stop shuts down the gRPC server and client connection.
func (a *App) Stop(ctx context.Context) error {
	if err := a.conn.Close(); err != nil {
		return fmt.Errorf("close grpc client: %w", err)
	}
	return a.grpcServer.GracefulStop(ctx)
}

func helloGatewayHandler(conn *grpc.ClientConn) runtime.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		var out wrapperspb.StringValue
		if err := conn.Invoke(r.Context(), rpcinternal.HelloMethod, &emptypb.Empty{}, &out); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": out.Value})
	}
}

func adaptHandler(handler runtime.HandlerFunc) rpcgateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, params map[string]string) {
		handler(w, r, params)
	}
}

// newTestTimeout returns a context with a reasonable test/demo timeout.
func newTestTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}
