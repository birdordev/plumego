# Card 1643 — KV 主文档仍推荐已标记废弃的配置入口

Milestone: 稳定模块质量复审
Recipe: specs/change-recipes/fix-bug.yaml
Context Package: implementation
Priority: P3
State: done
Primary Module: store
Owned Files: `docs/modules/store/README.md`, `website/src/generated/modules.ts`
Depends On: 无

Goal: KV 主文档仍推荐已标记废弃的配置入口。
Scope: 改用 DefaultConfig 做首选并在兼容段引用旧入口；保留已有清单决定。
Non-goals: 维持公开签名和依赖；涉及现有兼容契约的调整须先核对调用边界及迁移影响。
Files: `docs/modules/store/README.md`, `website/src/generated/modules.ts`。
Tests: 核对 Options 兼容测试和 API 快照，运行生成文档检查。
Docs Sync: 复核 `docs/modules/store/README.md`，模块说明改动后核对生成文件差异。
Done Definition: 完成测试与验收记录，并标明剩余取证范围。
Outcome: 模块文档将 DefaultConfig 设为首选，DefaultOptions 继续兼容。

## 证据

检查号为 6,7,16,52，状态为源码确认。

模块说明推荐 DefaultOptions，但实现的废弃注释明确替代为 DefaultConfig。读主文档的新增接入方会继续采用兼容符号。 [docs/modules/store/README.md:158](../../../docs/modules/store/README.md#L158)

[复审报告](../../../docs/evidence/stable-quality-review-09a7f5d3.md)

## 验收与验证

- [x] 核对 Options 兼容测试和 API 快照，运行生成文档检查。
- [x] 记录验证命令、状态和剩余范围。若使用既有用例满足验收，记录对应证据。

```sh
make api-check
make website-sync
make website-check-generated
```

## 实施证据

[TestDefaultOptions](../../../store/kv/kv_test.go#L404)
