# Card 1807 — 核对 handler 构建期间 Shutdown 与重试

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: core
Owned Files: `core/core_public_test.go`
Depends On: 无

Goal: 失败可识别，builder 一次完成后可取得 Server，最终顺利关闭。
Scope: 用阻塞 builder 和两次公开生命周期操作。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `core/core_public_test.go`。
Tests: 失败可识别，builder 一次完成后可取得 Server，最终顺利关闭。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 构建 handler 时的 Shutdown 返回受控结果，后续 Prepare 可继续。

## 证据与影响

第 9 节检查号为 28,31,33,36。证据状态为补证。

builder 运行于准备锁之外，应用服务器可尚未发布。尚需核对同期 Shutdown 失败是否影响后续正常 Prepare。 [core/http_handler.go:45](../../../core/http_handler.go#L45)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 失败可识别，builder 一次完成后可取得 Server，最终顺利关闭。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./core
```

## 实施证据

- [TestShutdownDuringHandlerConstructionAllowsPreparation](../../../core/core_public_test.go#L674)

[批次验证与基准](../review-a074eadb/README.md)
