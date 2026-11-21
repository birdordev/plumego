# Card 1922 — 核对 CORS 构建后独立持有暴露头和方法配置

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/cors/cors_test.go`
Depends On: 无

Goal: 已构建中间件沿初始列表，拒绝或通过路径各自只执行一次。
Scope: 构建后修改原 methods、expose 和 allowed headers，交替发送普通合成 Origin 对应的预检及简单请求。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `middleware/cors/cors_test.go`。
Tests: 已构建中间件沿初始列表，拒绝或通过路径各自只执行一次。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 从 CORS 配置构建的中间件独立持有允许方法、请求头、Origin 和暴露头，预检和业务调用各走原分支。

## 证据与影响

第 9 节检查号为 14,20,32,34,39。证据状态为补证。

初始化规范化四个列表，应脱离调用方可变切片。需同时观察简单响应暴露头与合法预检。 [middleware/cors/cors.go:135](../../../middleware/cors/cors.go#L135)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 已构建中间件沿初始列表，拒绝或通过路径各自只执行一次。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

```sh
go test -race -timeout 60s ./middleware/cors
```

## 实施证据

- [TestCORSConstructionDetachesMethodsAndExposedHeaders](../../../middleware/cors/cors_test.go#L696)

[批次验证与基准](../review-2b94cf86/README.md)
