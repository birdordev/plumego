# Card 1599 — 验证 gzip 写失败的字节计数与收尾

Milestone: 本轮质量审查
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: middleware
Owned Files: `middleware/compression/gzip_test.go`
Depends On: 无

Goal: 验证 gzip 写失败的字节计数与收尾。
Scope: 用人工短写或明确出错的底层 writer 覆盖压缩头与收尾。
Non-goals: 保留现有公开签名、依赖与模块边界。测试若证实新的缺陷，先记录证据再单独安排修复。
Files: `middleware/compression/gzip_test.go`。
Tests: 断言返回计数在输入范围内、传输错误可见且终态仅提交一次。
Docs Sync: 本卡按现有契约验证。若取证表明契约需调整，先更新后续卡范围。
Done Definition: 完成下列验收和验证，记录证据与剩余风险。
Outcome: 受控底层写错误通过 gzip 写入返回，正文计数在输入范围内且终态只提交一次。

## 证据与影响

- 检查号：22,23,24,39。证据状态：补证。
- 压缩启动路径和已启动路径返回写入数的方式不同，收尾还可能失败。 [middleware/compression/gzip.go:359](../../../middleware/compression/gzip.go#L359)
- [审查报告](../../../docs/evidence/stable-quality-review-dee7aa0f.md)

## 验收与验证

- [x] 断言返回计数在输入范围内、传输错误可见且终态仅提交一次。
- [x] 运行以下命令并记录结果、失败路径和未验证范围。修改 Go 文件前完成仓库预检，验证前先执行仓库格式化。

```sh
go test -race -timeout 60s ./middleware/compression
```

## 实施证据

[TestGzipWriteFailureCommitsAtMostOnce](../../../middleware/compression/gzip_test.go#L1689)
