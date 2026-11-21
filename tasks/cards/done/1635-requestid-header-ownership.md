# Card 1635 — 附加请求 ID 时原请求头与衍生请求共用写入

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/requestid/helpers.go`, `middleware/requestid/request_id.go`, `middleware/requestid/request_id_test.go`, `middleware/requestid/README.md`, `docs/modules/middleware/README.md`
Depends On: 无

Goal: 附加请求 ID 时原请求头与衍生请求共用写入。
Scope: 先明确原请求头是有意回写还是只配置下游；选择隔离时只在修改头的分支克隆 Header，并迁移明确依赖回写的调用。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `middleware/requestid/helpers.go`, `middleware/requestid/request_id.go`, `middleware/requestid/request_id_test.go`, `middleware/requestid/README.md`, `docs/modules/middleware/README.md`。
Tests: 分别断言启用、禁用、原始 Header 为 nil 及带既有字段时两侧的值与隔离。
Docs Sync: 复核 `docs/modules/middleware/README.md`，模块说明改动后核对生成文件差异。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 盘点仓库调用后，在回写下游 ID 的分支复制 Header，保留 nil 初始化、已有字段和计数语义。

## 证据

检查号为 6,16,27,34，状态为已复现。

WithContext 只浅拷贝请求。Header 非 nil 时，给返回请求写入 ID 同时改变传入请求。现有测试断言下游可见，且测试注释把启用描述为回写。原请求是否应被修改尚无明确公开约定。 [middleware/requestid/helpers.go:37](../../../middleware/requestid/helpers.go#L37)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 分别断言启用、禁用、原始 Header 为 nil 及带既有字段时两侧的值与隔离。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./middleware/requestid
make website-sync
make website-check-generated
```

## 实施证据

[TestAttachRequestIDDetachesHeadersWhenWriting](../../../middleware/requestid/request_id_test.go#L178)
