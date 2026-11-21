# Card 2021 — StaticFS Stat 失败仍归还文件

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: router
Owned Files: `router/static_test.go`
Depends On: 无

Goal: StaticFS Stat 失败仍归还文件
Scope: 注入返回普通 Stat 错误的假文件并记录 Close 次数。
Non-goals: 范围限于本卡涉及的契约，保持公开签名和依赖。
Files: `router/static_test.go`。
Tests: GET 和 HEAD 均终止该次查找，关闭一次且响应遵循既有错误分支。
Docs Sync: 核对新增验收与已有模块说明一致，记录证据和限制。
Done Definition: 完成以下验收，记录命令、结果与剩余限制。
Outcome: 普通 Stat 错误下 GET 与 HEAD 均 404 且关闭文件一次，HEAD 正文为空。

## 证据与影响

证据状态为补证。第 9 节检查号为 22,29,37,46。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

StaticFS 接受自定义文件对象。Open 成功后 Stat 失败路径应仍归还文件。 [router/static.go:203](../../../router/static.go#L203)

[复审报告](../../../docs/evidence/stable-quality-review-c3a77410.md)

## 验收与验证

- [x] GET 和 HEAD 均终止该次查找，关闭一次且响应遵循既有错误分支。
- [x] 已有测试覆盖相同触发和断言时可直接复用，记录具体用例。

```sh
go test -race -timeout 60s ./router
```

## 实施证据

- [TestStaticFSStatErrorClosesEachHeadAndGetFile](../../../router/static_test.go#L808)
