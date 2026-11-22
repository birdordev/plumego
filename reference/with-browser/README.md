# 浏览器表单、会话与 OIDC

此独立模块演示显式挂载的 HTML 页面、表单提交、浏览器 Cookie 和企业身份接入。OIDC 客户端依赖位于本模块。

## 运行

在身份提供方将此服务注册为机密客户端，允许回调 `<BROWSER_PUBLIC_ORIGIN>/auth/callback`。设置以下环境变量后，从本目录运行 `go run .`。

| 变量 | 用途 |
| --- | --- |
| `BROWSER_PUBLIC_ORIGIN` | 浏览器使用的 HTTPS 源，开发时可用 `http://127.0.0.1:8091` |
| `BROWSER_LISTEN_ADDR` | 服务监听地址，默认 `127.0.0.1:8091` |
| `OIDC_ISSUER` | 身份提供方配置的发行方 URL |
| `OIDC_CLIENT_ID` | 客户端标识 |
| `OIDC_CLIENT_SECRET` | 机密客户端的密钥 |
| `OIDC_ADMIN_GROUP` | 可选。将受信任的 `groups` 声明中的指定组映射为管理员角色 |

HTTPS 部署使用保留原始 Host 的反向代理。登录验证 state、PKCE、nonce 和 ID 令牌。OIDC 回调的身份验证最多等待 10 秒。OIDC 访问令牌和刷新令牌不进入应用会话。会话凭据由服务端生成，并通过 HttpOnly 和 SameSite=Lax Cookie 交给浏览器。HTTPS 源还会设置 Secure 属性。

主页可提交显示名，验证失败在表单回显，成功后回到主页。资料更新、会话续期和退出均验证 CSRF。登录与 `POST /session/renew` 会生成新的本地会话标识和 CSRF 令牌，旧标识随即撤销。会话有效期为一小时，可在到期前手动续期。自登录起满八小时后需要重新通过 OIDC 登录。退出时撤销会话。会话保存在有上限的进程内存中，重启后需要重新登录。跨副本部署需要共享会话存储。

资料、续期和退出表单通过 `x/validate.BindForm` 读取最多 4 KiB 的 URL 编码正文。CSRF 和表单字段取自正文。格式错误、超过上限和媒体类型错误分别返回 400、413 和 415。

运行 `go test ./...` 可验证本地登录、角色映射、CSRF、表单输出转义和退出路径。
