# Card 1995 — 查证文件轮换遇到已存在名称时的大小归属

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/analysis-only.yaml
Context Package: review
Priority: P2
State: done
Primary Module: log
Owned Files: `log/glog_rotation.go`, `log/glog_test.go`
Depends On: 无

Goal: 记录可达条件、文件身份和字节计量。证实影响后再决定是否需要实现任务。
Scope: 先用私有受控文件名或时间输入验证重复路径，并审阅重新打开时的旧文件语义。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `log/glog_rotation.go`, `log/glog_test.go`。
Tests: 记录可达条件、文件身份和字节计量。证实影响后再决定是否需要实现任务。
Docs Sync: 记录核验结论；只在现有契约文字需要澄清时另提文档变更。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 受控相同时钟的本机探针确认重用同一个路径且保留 7 字节实体，内部计量被置零。系统实时时钟上发生频率未测。后续若加固，宜独占创建轮换目标，冲突时换唯一名称，预留归档解析兼容和失败回滚校验。

## 证据与影响

第 9 节检查号为 32,34,43,46，当前证据状态为补证。 [质量规则第 9 节](../../../docs/operations/agent-code-quality-rules.md#L267)

轮换以追加模式打开路径并从零开始计量。文件名包含时钟和 PID，当前尚未验证重复名称在正常条件下的可达性。 [log/glog_rotation.go:136](../../../log/glog_rotation.go#L136)

[复审报告](../../../docs/evidence/stable-quality-review-999d5e4d.md)

## 验收与验证

- [x] 记录可达条件、文件身份和字节计量。证实影响后再决定是否需要实现任务。
- [x] 记录挂载入口、具体断言和结果。已有测试能覆盖时复用并注明。

## 实施证据

用 synctest 固定同一进程的两次轮换时钟，在第一次打开的 INFO 路径写入 7 字节。第二次轮换后，文件路径与磁盘内容保持，计量归零。 探针通过，临时 overlay 位于工作区外。

```sh
go test -overlay=/tmp/999d-analysis-overlay.json -race -v -timeout 45s ./log -run '^TestReviewExistingRotationTarget$'
```
