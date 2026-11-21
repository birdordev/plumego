# Card 1636 — Additional 的规范化同名项导致输出取决于 map 顺序

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/headers/headers.go`, `security/headers/headers_test.go`, `docs/modules/security/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: Additional 的规范化同名项导致输出取决于 map 顺序。
Scope: 在 Validate 统一对 Additional 规范化名字并拒绝冲突，保持单个 Additional 对标准字段的既有优先级。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `security/headers/headers.go`, `security/headers/headers_test.go`, `docs/modules/security/README.md`, `website/src/generated/modules.ts`。
Tests: 以普通无关自定义头覆盖重复配置、单个大写变体和无写入的错误返回。
Docs Sync: 复核 `docs/modules/security/README.md`，模块说明改动后核对生成文件差异。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: Additional 按 HTTP 规范名校验唯一性。冲突配置无部分输出，单个合法自定义头仍可应用。

## 证据

检查号为 14,32,34,40,46，状态为源码确认。

校验分别接受大小写不同且均合法的字段名，应用时把两项规范化为同一名并依次覆盖。冲突配置的最终响应缺少可预测优先级。 [security/headers/headers.go:286](../../../security/headers/headers.go#L286)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 以普通无关自定义头覆盖重复配置、单个大写变体和无写入的错误返回。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
go test -race -timeout 60s ./security/headers
make website-sync
make website-check-generated
```

## 实施证据

[TestAdditionalHeaderNamesMustBeUniqueAfterCanonicalization](../../../security/headers/headers_test.go#L683)
