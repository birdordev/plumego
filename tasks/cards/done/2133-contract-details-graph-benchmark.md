# Card 2133 — 测量错误详情共享子图的复制和编码开销

Milestone: 稳定模块第 9 节复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P3
State: done
Primary Module: contract
Owned Files: `contract/write_bench_test.go`
Depends On: 无

Goal: 测量错误详情共享子图的复制和编码开销。
Scope: 用小到中等固定子图跑重复基准，分别量化 Build、Details 与 WriteError。
Non-goals: 本卡提交取证结果。后续行为变更由独立修复卡执行。
Files: `contract/write_bench_test.go`
Tests: 核对别名隔离、JSON 字段一致及循环图的安全回退，然后报告 ns/op、B/op、allocs/op。
Docs Sync: 核对既有文档与验收结果一致，记录需要后续同步的位置。
Done Definition: 记录命令、结果、证据限制和是否需要独立修复卡。
Outcome: 三轮 Build、Details、WriteError 基准记录了普通与命名切片的共享图。Build 普通为 504 B/op，命名为 936 B/op。WriteError 两组均保留字段形状，既有测试覆盖别名与循环回退。未观察到需要调整公开契约的信号。

## 证据与影响

证据状态为待补证。第 9 节检查号为 12,34,35,38,48。快路径和反射路径都有共享引用处理。相同净数据在普通容器与命名容器下的时间和分配差异缺少对照。 [contract/errors.go:489](../../../contract/errors.go#L489) [contract/errors.go:576](../../../contract/errors.go#L576) [contract/errors_test.go:17](../../../contract/errors_test.go#L17) [contract/write_bench_test.go:50](../../../contract/write_bench_test.go#L50)

[复审报告](../../../docs/evidence/stable-quality-review-6e8d53f9.md)

## 验收与验证

在同名基准下分组记录 Build、Details 和 WriteError。

- [x] 核对别名隔离、JSON 字段一致及循环图的安全回退，然后报告 ns/op、B/op、allocs/op。
- [x] 记录命令、结果、证据限制和是否需要独立修复卡。

```sh
go test -race -timeout 60s ./contract
go test ./contract -run '^$' -bench '^BenchmarkWriteErrorSharedDetailGraph$' -benchmem -benchtime=200ms -count=3
```
