# Card 1947 — 核对同一修改时间的备份淘汰与活动链接优先级

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: log
Owned Files: `log/glog_test.go`
Depends On: 无

Goal: 活动目标保留，备份数量正确。记录当下平局表现和是否需外部承诺。
Scope: 创建同级同 mtime 的档案和活动目标，多轮以不同创建顺序运行同一清理。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/glog_test.go`。
Tests: 活动目标保留，备份数量正确。记录当下平局表现和是否需外部承诺。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 同等级同 mtime 的两个候选在本机夹具中都保留索引 0；改变创建顺序不改变本次结果。活动链接目标不参与淘汰，文件名平局选择未成为公开约定。

## 证据与影响

第 9 节检查号为 16,29,31,43。证据状态为补证。

备份排序只比较 mtime，两个同时间文件哪一个保留未在契约定义。需要与被保护的活动链接一起验证。 [log/glog_rotation.go:286](../../../log/glog_rotation.go#L286)

[复审报告](../../../docs/evidence/stable-quality-review-2b94cf86.md)

## 验收与验证

- [x] 活动目标保留，备份数量正确。记录当下平局表现和是否需外部承诺。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

## 实施证据

Go overlay 仅向本机 log 测试追加夹具。两个创建顺序中始终保留一个归档和一个活动目标，夹具里的索引 0 均保留。若未来需要公开可重复平局规则，可以补文件名比较和跨平台验收。

```sh
go test -overlay=/tmp/2b94-log-overlay.json -race -timeout 40s -v ./log -run '^TestPrivateRetentionSameMtimePreservesActiveTarget$'
```

[批次验证与基准](../review-2b94cf86/README.md)
