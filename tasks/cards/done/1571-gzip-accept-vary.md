# Card 1571 — gzip 协商遗漏 Accept 缓存维度

Milestone: 本轮质量审查
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P1
State: done
Primary Module: middleware
Owned Files: `middleware/compression/gzip.go`, `middleware/compression/gzip_test.go`, `docs/modules/middleware/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: gzip 协商遗漏 Accept 缓存维度。
Scope: 将 Accept 与 Accept-Encoding 放入同一变体声明并覆盖 SSE 直通，复用已有 Vary writer。保留升级直通。
Non-goals: 保留现有公开签名和依赖。涉及未授权的行为或兼容契约调整时，先完成调用方核对与兼容决策。
Files: `middleware/compression/gzip.go`, `middleware/compression/gzip_test.go`, `docs/modules/middleware/README.md`, `website/src/generated/modules.ts`。
Tests: 对相同 URL 对比普通与 SSE Accept，检查编码、原有 Vary 去重及两个结果共同声明的变体维度。
Docs Sync: 核对 `docs/modules/middleware/README.md`。 修改模块说明后检查网站生成差异，并在同一交付中保留相应生成文件。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 所有非升级变体合并 Accept-Encoding 和 Accept，保留已有 Vary 值和 Accept-Encoding 的顺序。

## 证据与影响

- 检查号：6,24,39,45。证据状态：已复现。
- 普通 Accept 得到 gzip 和 Vary: Accept-Encoding；SSE Accept 得到无编码响应，且完全没有 Vary。共享缓存无法据响应头区分这两个变体。 [middleware/compression/gzip.go:71](../../../middleware/compression/gzip.go#L71)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 对相同 URL 对比普通与 SSE Accept，检查编码、原有 Vary 去重及两个结果共同声明的变体维度。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./middleware/compression
make website-sync
make website-check-generated
```

## 实施证据

[TestVaryCoversEventStreamBypass](../../../middleware/compression/gzip_test.go#L1640) · [TestVaryCoversEncodingVariants](../../../middleware/compression/gzip_test.go#L1604)
