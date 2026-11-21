# Card 1744 — 单组件入口解引用存在但为空的快照

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: x/observability
Owned Files: `x/observability/ops/healthhttp/handlers.go`, `x/observability/ops/healthhttp/handlers_test.go`, `docs/modules/x/observability/ops/README.md`
Depends On: 1743

Goal: 分别验证 nil,true、nil,false 和有效状态，响应只提交一次。
Scope: 区分明确缺失和声明存在但快照无效；后者在使用前走固定 503，沿用规范错误 writer。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `x/observability/ops/healthhttp/handlers.go`, `x/observability/ops/healthhttp/handlers_test.go`, `docs/modules/x/observability/ops/README.md`。
Tests: 分别验证 nil,true、nil,false 和有效状态，响应只提交一次。
Docs Sync: 复核模块说明并验证生成数据。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 声明存在但快照为空时返回规范 503 和 request_id。缺失仍为 404，有效状态仍按原健康协议输出，每条路径只提交一次。

## 证据与影响

第 9 节检查号为 6,20,21,42,46。证据状态为已复现。

合成 Manager 返回 nil、true，鲜活检查成功或失败时均 panic，端点没有形成规范的探测结果。明确未登记的分支已有 404。 [x/observability/ops/healthhttp/handlers.go:100](../../../x/observability/ops/healthhttp/handlers.go#L100)

[复审报告](../../../docs/evidence/stable-quality-review-d224acbc.md)

## 验收与验证

- [x] 分别验证 nil,true、nil,false 和有效状态，响应只提交一次。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./x/observability/ops/healthhttp
make website-sync
make website-check-generated
```

## 实施证据

- [TestAcceptanceComponentSnapshotAvailability](../../../x/observability/ops/healthhttp/handlers_test.go#L630)

[批次验证与基准](../review-d224acbc/README.md)
