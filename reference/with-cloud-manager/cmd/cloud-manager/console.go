package main

import (
	_ "embed"
	"errors"
	"net/http"

	"github.com/birdordev/plumego/core"
	plumelog "github.com/birdordev/plumego/log"
	"github.com/birdordev/plumego/x/openapi"

	"with-cloud-manager/internal/identity"
)

//go:embed console/index.html
var consolePage []byte

//go:embed console/styles.css
var consoleStyles []byte

//go:embed console/app.js
var consoleScript []byte

func registerConsole(app *core.App, cfg consoleConfig, sessions identity.BrowserSessions) error {
	logger := app.Logger()
	return errors.Join(
		app.Use(cfg.apiSession(logger)),
		app.Get("/console", consoleStatic("text/html; charset=utf-8", consolePage, logger)),
		app.Get("/console/styles.css", consoleStatic("text/css; charset=utf-8", consoleStyles, logger)),
		app.Get("/console/app.js", consoleStatic("text/javascript; charset=utf-8", consoleScript, logger)),
		app.Post("/console/login", cfg.login(sessions, logger)),
		app.Get("/console/session", consoleProfile(sessions, cfg, logger)),
		app.Post("/console/logout", cfg.logout(sessions, logger)),
	)
}

func consoleStatic(media string, content []byte, logger plumelog.StructuredLogger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", media)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if _, err := w.Write(content); err != nil {
			logger.Warn("console asset write failed", plumelog.Fields{})
		}
	})
}

func consoleOpenAPI(hints map[string]openapi.Op) {
	hints["GET /console"] = openapi.Op{Responses: map[string]openapi.Response{"200": {Description: "浏览器控制台"}}}
	hints["GET /console/styles.css"] = openapi.Op{Responses: map[string]openapi.Response{"200": {Description: "控制台样式"}}}
	hints["GET /console/app.js"] = openapi.Op{Responses: map[string]openapi.Response{"200": {Description: "控制台脚本"}}}
	hints["POST /console/login"] = openapi.Op{OperationID: "loginCloudConsole", Summary: "同源浏览器通过已认证的 OIDC 网关建立会话",
		Responses: map[string]openapi.Response{"200": {Description: "已签发本地会话 Cookie"}, "400": {Description: "无效租户或正文"},
			"403": {Description: "网关、源或身份未经授权"}, "413": {Description: "正文过大"},
			"415": {Description: "媒体类型错误"}, "503": {Description: "会话存储不可用"}}}
	hints["GET /console/session"] = openapi.Op{OperationID: "getCloudConsoleSession", Security: []openapi.SecurityRequirement{{"consoleSession": {}}},
		Responses: map[string]openapi.Response{"200": {Description: "当前租户、项目和 CSRF 值"}, "401": {Description: "会话缺失或已失效"}, "503": {Description: "会话存储不可用"}}}
	hints["POST /console/logout"] = openapi.Op{OperationID: "logoutCloudConsole",
		Security: []openapi.SecurityRequirement{{"consoleSession": {}, "consoleCSRF": {}}},
		Responses: map[string]openapi.Response{"200": {Description: "已撤销本地会话"}, "403": {Description: "缺少有效会话、源或 CSRF 值"},
			"503": {Description: "会话存储不可用"}}}
}
