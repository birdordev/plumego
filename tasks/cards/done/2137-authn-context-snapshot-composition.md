# Card 2137 — 核对认证主体覆盖与请求取消的组合

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P3
State: done
Primary Module: security/authn
Owned Files: `security/authn/authn_test.go`
Depends On: 无

Goal: 核对认证主体覆盖与请求取消的组合。
Scope: 用 http.Request 绑定父上下文，构造两个派生请求，验证更新主体前后取消、请求值传播及属性所有权。
Non-goals: 本卡提交取证结果。后续行为变更由独立修复卡执行。
Files: `security/authn/authn_test.go`
Tests: 断言父、子请求保留各自主体，派生请求共享父取消和截止时间，读取后的属性修改彼此隔离。
Docs Sync: 核对既有文档与验收结果一致，记录需要后续同步的位置。
Done Definition: 记录命令、结果、证据限制和是否需要独立修复卡。
Outcome: 临时请求派生探针确认父子都接收父取消信号，并分别保留自己的 Principal 与可变属性快照。原有 nil 与空形状测试通过。

## 证据与影响

证据状态为待补证。第 9 节检查号为 25,27,34,40,45。现有主体复制测试和 HTTP 适配测试分布在两个包。已有覆盖测试使用普通 context，父请求取消与两次派生请求附加主体叠加时的所有权和信号传播待核对。 [security/authn/authn.go:48](../../../security/authn/authn.go#L48) [security/authn/authn_test.go:1](../../../security/authn/authn_test.go#L1) [middleware/auth/auth.go:52](../../../middleware/auth/auth.go#L52)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

- [x] 断言父、子请求保留各自主体，派生请求共享父取消和截止时间，读取后的属性修改彼此隔离。
- [x] 记录命令、结果、证据限制和是否需要独立修复卡。

```sh
go test -race -timeout 60s ./security/authn
```
