# 快速入门

Plumego 是一个标准库优先的 Go HTTP 工具包。

本页提供最小可运行示例。完成后，请立即阅读 `reference/standard-service`
了解规范应用结构，再阅读 `docs/start/adoption-path.md` 了解更完整的采用路径。

## 环境要求

- Go 1.26 或更高版本
- 主模块无需引入任何外部运行时依赖

安装：

```bash
go get github.com/birdordev/plumego@latest
```

## 最小可运行示例

创建 `main.go`：

```go
package main

import (
    "log"
    "net/http"

    "github.com/birdordev/plumego/contract"
    "github.com/birdordev/plumego/core"
)

func main() {
    cfg := core.DefaultConfig()
    cfg.Addr = ":8080"

    app := core.New(cfg, core.AppDependencies{})
    if err := app.Get("/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if err := contract.WriteResponse(w, r, http.StatusOK, map[string]string{
            "message": "pong",
        }, nil); err != nil {
            http.Error(w, "write response", http.StatusInternalServerError)
        }
    })); err != nil {
        log.Fatalf("注册路由: %v", err)
    }

    if err := app.Prepare(); err != nil {
        log.Fatalf("准备服务器: %v", err)
    }
    srv, err := app.Server()
    if err != nil {
        log.Fatalf("获取服务器: %v", err)
    }

    log.Println("服务器已启动，监听 :8080")
    log.Fatal(srv.ListenAndServe())
}
```

运行：

```bash
go run main.go
```

打开 `http://localhost:8080/ping`。

## 生产风格示例

包含 Request ID、panic 恢复、优雅关闭和规范生命周期装配：

```go
package main

import (
    "context"
    "errors"
    "log"
    "net/http"
    "os"
    "os/signal"
    "syscall"

    "github.com/birdordev/plumego/contract"
    "github.com/birdordev/plumego/core"
    plog "github.com/birdordev/plumego/log"
    "github.com/birdordev/plumego/middleware/accesslog"
    "github.com/birdordev/plumego/middleware/recovery"
    "github.com/birdordev/plumego/middleware/requestid"
)

func main() {
    if err := run(); err != nil {
        log.Printf("服务器停止: %v", err)
        os.Exit(1)
    }
}

func run() error {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    cfg := core.DefaultConfig()
    cfg.Addr = ":8080"
    app := core.New(cfg, core.AppDependencies{Logger: plog.NewLogger()})

    recoveryMw, err := recovery.Middleware(recovery.Config{Logger: app.Logger()})
    if err != nil {
        return err
    }
    accesslogMw, err := accesslog.Middleware(accesslog.Config{Logger: app.Logger()})
    if err != nil {
        return err
    }
    if err := app.Use(
        requestid.Middleware(), // 最外层，先运行
        recoveryMw,
        accesslogMw,
    ); err != nil {
        return err
    }

    if err := app.Get("/ping", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        _ = contract.WriteResponse(w, r, http.StatusOK, map[string]string{
            "message": "pong",
        }, nil)
    })); err != nil {
        return err
    }

    if err := app.Prepare(); err != nil { // 路由在此处冻结
        return err
    }
    srv, err := app.Server()
    if err != nil {
        return err
    }
    go func() {
        <-ctx.Done()
        _ = app.Shutdown(context.Background())
    }()

    log.Println("服务器已启动，监听 :8080")
    if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
        return err
    }
    return nil
}
```

## Bootstrap 形式的进展

随着应用程序的增长，有三种常见的 bootstrap 模式：

1. **`plumego.New()`** — 零配置示例：最适合学习和简单示例。
2. **`core.New` + 内联 `Prepare/Server/Shutdown`** — 显式生命周期控制（如上所示）：需要信号处理和自定义 logger 注入。
3. **`app.New` + `RegisterRoutes` 包装器** — 规范应用布局（见 `reference/standard-service`）：推荐用于生产服务，将 bootstrap、路由和中间件布线分离。

当内联示例不再足够时，从 `reference/standard-service` 复制结构，而不是扩展 `main.go`。

## 路由基础

Plumego 使用标准 `net/http` handler 形状，无需学习框架专属类型。

```go
// 注册不同 HTTP 方法的路由
_ = app.Get("/users", listUsersHandler)
_ = app.Post("/users", createUserHandler)
_ = app.Put("/users/{id}", updateUserHandler)
_ = app.Delete("/users/{id}", deleteUserHandler)
```

读取路径参数：

```go
import "github.com/birdordev/plumego/router"

app.Get("/users/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    id := router.Param(r, "id")
    // id 现在是路径中 {id} 的实际值
}))
```

路由分组（共享前缀和中间件）：

```go
api := app.Group("/api/v1")
api.Get("/users", listUsersHandler)
api.Post("/users", createUserHandler)
// 实际注册为 GET /api/v1/users 和 POST /api/v1/users
```

## 结构化响应

成功响应：

```go
type User struct {
    ID   string `json:"id"`
    Name string `json:"name"`
}

contract.WriteResponse(w, r, http.StatusOK, User{ID: "1", Name: "Alice"}, nil)
```

结构化错误响应：

```go
err := contract.NewErrorBuilder().
    WithType(contract.TypeNotFound).
    WithMessage("用户不存在").
    WithField("user_id", userID).
    Build()
contract.WriteError(w, r, err)
```

响应格式是规范固定的，不要自行定义 JSON 响应结构体。

## 中间件

中间件按注册顺序执行（先注册先运行）：

```go
app.Use(
    requestid.Middleware(), // 第一个运行
    recoveryMw,             // 第二个运行
    corsMiddleware,         // 第三个运行
)
```

所有中间件都是标准 `func(http.Handler) http.Handler` 形状，可以与任何兼容标准库的中间件互操作。

## 生命周期顺序

```
core.New(cfg, deps)        // 构造 App
app.Use(middleware...)     // 注册中间件
app.Get/Post/...(routes)   // 注册路由
app.Prepare()              // 冻结路由（此后不可再注册）
app.Server()               // 构建 *http.Server
srv.ListenAndServe()       // 开始服务
app.Shutdown(ctx)          // 优雅关闭
```

**Prepare 之后不能再注册路由或中间件**——这是最常见的错误，详见 `docs/start/troubleshooting.md`。

## 选择下一个模块

最小示例运行后，保持 `reference/standard-service` 的应用结构，按需添加扩展包：

| 需求 | 第一个读的文档 |
|---|---|
| CRUD 资源约定 | `docs/modules/x/rest/README.md` |
| 多租户：解析、策略、配额 | `docs/modules/x/tenant/README.md` |
| 反向代理、负载均衡 | `docs/modules/x/gateway/README.md` |
| WebSocket 实时功能 | `docs/modules/x/websocket/README.md` |
| AI 服务接入 | `docs/modules/x/ai/README.md` |
| 消息队列/Webhook | `docs/modules/x/messaging/README.md` |
| gRPC 传输 | `docs/modules/x/rpc/README.md` |
| Prometheus / OpenTelemetry | `docs/modules/x/observability/README.md` |

不要从 `x/*` 包出发构建应用结构。扩展包是对规范应用结构的显式增量，不是另一套启动框架。

## 规范的后续阅读顺序

1. `reference/standard-service/README.md` — 规范应用结构
2. `docs/start/adoption-path.md` — 5 分钟、30 分钟、1 天采用路径
3. `docs/reference/canonical-style-guide.md` — handler、路由、中间件和依赖注入约定
4. `docs/start/troubleshooting.md` — 常见问题排查

## 常见误区

- 不要自定义 JSON 响应结构体——使用 `contract.WriteResponse` 和 `contract.WriteError`。
- 不要在中间件中注入业务服务——中间件只做传输层处理。
- 不要在 `Prepare()` 之后注册路由。
- 不要从 `x/*` 包出发——先从 `reference/standard-service` 开始。
- 不要用 `http.Error` 替代 `contract.WriteError`——后者提供结构化错误代码。
