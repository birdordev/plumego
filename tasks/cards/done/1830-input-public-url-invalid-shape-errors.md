# Card 1830 — 核对公开 URL 校验分类及安全诊断

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/add-acceptance-tests.yaml
Context Package: implementation
Priority: P2
State: done
Primary Module: security
Owned Files: `security/input/input_test.go`
Depends On: 无

Goal: 正常配置通过，非法形状失败且固定诊断，不进行外部访问。
Scope: 仅使用无网络的正常网址形状与人工非法配置，核对错误类别和无回显。
Non-goals: 维持稳定公开签名与标准库依赖，将验证限于本卡范围。
Files: `security/input/input_test.go`。
Tests: 正常配置通过，非法形状失败且固定诊断，不进行外部访问。
Docs Sync: 复核本卡涉及的已公开约定。
Done Definition: 完成下列验收，记录验证命令、状态及剩余限制。
Outcome: 用本机合成 URL 核对合法配置返回 true，缺失主机、非法端口、无效转义和相对路径返回 false。稳定入口仅返回布尔值，测试不发起解析或联网。

## 证据与影响

第 9 节检查号为 18,22,25,40。证据状态为补证。

公开校验组合 URL 形状与字面来源限制，文档明确不验证实际连接。 [security/input/input.go:296](../../../security/input/input.go#L296)

[复审报告](../../../docs/evidence/stable-quality-review-a074eadb.md)

## 验收与验证

- [x] 正常配置通过，非法形状失败且固定诊断，不进行外部访问。
- [x] 记录运行结果。若已有用例覆盖目标，记录用例及相关断言。

```sh
go test -race -timeout 60s ./security/input
```

## 实施证据

- [TestValidatePublicURLRejectsMalformedConfigurationWithoutResolution](../../../security/input/input_test.go#L543)

[批次验证与基准](../review-a074eadb/README.md)
