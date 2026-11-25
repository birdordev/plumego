# 调度上线验收与运行手册

首期拓扑为单地域的多 API、多调度实例，短任务、CPU 和长 I/O 使用独立 Worker 池。终态恢复、Outbox、过期清理与归档作为独立角色部署。PostgreSQL 提供单写端点和同步备库。先确定真实业务负载、SLO、可接受的数据丢失量及恢复时间，再填写容量档案。

## 1. 业务副作用与对账

库存参考场景用同一个 PostgreSQL 事务校验领取租约、提交库存变更、落完成回执和登记 Outbox。先升级任务库到 v39，使用 `receiver-migrate` 升级独立接收库，再启动兼容的 API、Worker、Outbox 和接收方。给试点租户配置 SKU 库存，例如在独立环境执行以下初始化。

```sql
INSERT INTO scheduler_inventory (tenant_id, sku, available)
VALUES ('pilot', 'sku-1', 20);
```

通过 `POST /runs` 按现有协议提交 `demo.inventory` 的版本 1 请求。预留示例如下。

```json
{
  "topic": "demo.inventory",
  "payload": {"operation_id": "order-1", "action": "reserve", "sku": "sku-1", "quantity": 6}
}
```

同一个请求的提交结果不确定时，继续使用原 HTTP 幂等键。跨运行实例的业务去重由租户、操作 ID 和动作类型负责。相同业务键与参数再次执行时返回完成回执，库存只变更一次。相同业务键改用其他 SKU 或数量会永久失败。释放使用相同操作 ID、SKU、数量以及 `release` 动作，在预留确认后提交。释放原子校验原预留并恢复库存。业务事件携带有限的字段，独立接收库在一笔事务内记录接收回执、业务效果账和库存投影。预留和释放分别累计，支持投递先后顺序变化。

使用具备只读权限的任务库和接收库账号运行 `inventory-reconcile`。通过受控环境设置 `SCHEDULER_DATABASE_URL`、`SCHEDULER_RECEIVER_DATABASE_URL` 及 `SCHEDULER_INVENTORY_TENANT`。

```sh
go -C reference/with-scheduler run . -role inventory-reconcile
```

每页默认 100 项，最多 500 项。将 `next.operation_id` 和 `next.action` 分别传入 `SCHEDULER_INVENTORY_AFTER_OPERATION` 与 `SCHEDULER_INVENTORY_AFTER_ACTION`，直到遍历结束。宽限期由 `SCHEDULER_INVENTORY_GRACE` 指定，默认一分钟。正常确认中的记录继续观察。超期未确认、两库缺失或业务参数冲突均返回非零并输出有限的业务键。对账输出含租户与业务操作标识，应进入受控存储。将生产排查附上受理账、领取和执行尝试、Outbox 投递次数以及接收方实际业务变更次数。

只有在独立接收库明确没有该回执或业务效果、任务已终态且无活跃投递租约，并度过宽限期时，专用操作角色才可使用 `inventory-redrive` 重新开放原事件。设置 `SCHEDULER_INVENTORY_TENANT`、`SCHEDULER_INVENTORY_RUN_ID`、`SCHEDULER_INVENTORY_COMMAND_KEY` 和 `SCHEDULER_INVENTORY_ACTOR`，并使用授权的任务库操作账号和只读接收库账号。每租户每秒至多一条新命令，重试复用命令键，审核可从 `scheduler_inventory_redrives` 中读取。两库任一不可用时停止人工补发。与第三方集成时应复用该方的稳定幂等键。结果未知且无法独立查询的不可逆操作留在人工对账队列中。

## 2. 容量档案与隔离配置

按 [混合负载认证](load-baseline.md#混合负载认证) 记录目标部署版本、数据库规格和历史数据量、各角色最大副本数、任务耗时和载荷分布、下游速率、正常与峰值任务量。将 API、物化、排队、终态和排空的门槛分别写入档案。流量发生器本身的延迟、超时和过载要纳入判断。

短任务、CPU、长 I/O 分池，长 I/O 使用独立下游。数据库预算至少计入每个角色最大副本数乘连接池上限，以及 Worker 每个通知连接、复制和维护所需余量。保留续租、终态确认与接管的连接。自动扩容通过 `SCHEDULER_AUTOSCALE_DB_RESERVE` 保留额外空闲客户端连接，默认 8。多个独立扩容控制器的最大副本数仍需统一计入拓扑预算。

在预发先测试低流量租户单跑，再测热点与短、长任务共同运行。可为热点配置挂起量、字节和执行并发，以及入口速率。例如以下值仅适用于演练。

```sql
INSERT INTO scheduler_tenant_ingress
  (tenant_id, regular_per_second, urgent_per_second, urgent_actor)
VALUES ('hot', 120, 12, 'urgent-api');

INSERT INTO scheduler_execution_budgets (scope, budget_key, max_concurrent, min_start_interval_ms)
VALUES ('tenant', 'hot', 8, 0), ('retry', 'demo.record', 1, 25);
```

入口规则以 PostgreSQL 时间按秒计账，已受理键重放不扣新额度。预算中的紧急额度只供配置的认证部署 actor。总配额仍适用于各优先级。若共享队列在限额下仍使低流量租户超标，可用 `SCHEDULER_WORKER_PROTECTED_TENANTS` 在短任务池配置逗号分隔的受保护租户，最多 32 个。每个 Worker 在受保护租户与共享队列之间轮转。一个保护轮次最多领取该租户一个任务，缺少候选时共享队列使用空出的容量。每个轮次保留现有优先级、老化、暂停、租约和执行重叠规则。用同一物理 Topic 的混合负载验证保护配置。

```sh
PYTHONDONTWRITEBYTECODE=1 python3 reference/with-scheduler/certify_capacity.py --hotspot
```

对允许误差的计划使用稳定错峰，并为严格定点任务独立记录物化 SLO。重试与初次执行分开统计并使用独立额度。下游恢复时限制重投速率，关注待确认 Outbox、最老可运行年龄、DLQ 增速和执行预算饱和。

同负载对比 ACK 收集窗口、批大小和 API 批量提交大小。一起记录有效完成速率、同步复制延迟、WAL 字节／完成任务、连接等待、锁等待、终态 p99 和长稳期间的表膨胀。归档、报表和重放以独立批次运行，长稳档覆盖维护窗口。容量结论记录通过 SLO 的最高持续档、已验证的短峰强度和持续时间、故障后实测排空时长。持续接纳速率达到完成速率时，先压低准入或恢复处理能力。

## 3. 高可用、备份恢复与回切

1. 隔离旧主的写路径，并从实际客户端位置验证旧地址写入失败。记录已确认提交水位，在候选备库核对回放水位和唯一写入权限后提升。零丢失目标以同步策略、合格备库和正确隔离同时成立为条件。
2. 切换写端点，API 在同步保护恢复前停止受理。分别记录写端点恢复、首个确认、旧租约回收、积压回到目标、Outbox 清空和业务对账完成的时间。读状态接口在其数据库可用时继续工作。
3. 让旧主以只读备库方式恢复，确认恢复保护后再放量。再切一次并按原实例 ID 对账。尝试使用旧 Worker 租约提交，必须被拒绝。已提交的业务效果与可能重投的 HTTP 事件分别统计。
4. 将基准备份及归档 WAL 恢复到隔离端点，保持恢复侧 Outbox 停止。核对迁移版本、游标、在途租约、结果和 Outbox，独立接收库找出恢复点后的业务效果。重建受理记录或人工批准补发时沿用稳定业务键，再启动恢复侧投递。
5. 多地域演练另查实际网络分区、旧分片代次拒写、跨地域接收方全局去重和回切。目标包含执行驻留时，核对 Worker 领取范围、专用凭据和下游位置。

本机两次切换与备份恢复命令见 [参考应用说明](README.md#postgresql-failover-drill)。预发运行实际隔离和提升流程，保存原始报告并核对业务账。对于已确认任务丢失、重复业务变更及旧代次写入成功，放行阈值均为零。

本机切换演练在替代 Worker 的新租约生效后，用旧租约调用同一结果事务，必须返回租约失效。时间点恢复演练先暂停发送，通过对账定位恢复点后已生效的库存操作，再以原请求幂等键和业务操作键重建运行。恢复侧重新发送后，独立接收库新增投递回执，库存只保留一笔该操作的实际变更。最终两库对账差异必须归零。

## 4. 预发、回退与放量

预发依次跑混合长稳、少一个短池副本、真实数据库接管后的负载和兼容版本回退。演练期间保留原实例 ID、任务版本、计划游标与业务幂等记录。在途新版本任务由兼容 Worker 排空。v39 参考程序采用精确版本探针，直接切回只支持 v38 的二进制会拒绝启动。先保存升级前备份，准备能读当前 schema 的回退制品，核对旧任务与新在途任务的处理矩阵。

先选择试点租户和任务池，使用低比例准入并保持正常租户对照。每档覆盖最低观测窗口与业务对账周期，再扩大流量。在任何一档发生确认丢失、重复业务修改或旧写入者成功，停止放量并保持证据。尾延迟和积压超标时先收紧准入，暂停低优先分发与批量重放，继续提供状态查询、续租和终态确认。完成补救后用相同档位复测再恢复放量。

### 执行放行判定

准备已批准的容量档案、发布策略和证据索引。在放行试点前核对 `preflight`，试点完成有代表性的观察和对账周期后核对 `promote`。判定器读取文件，输出 JSON，门槛失败时返回非零。

```sh
PYTHONDONTWRITEBYTECODE=1 python3 reference/with-scheduler/release_gate.py \
  --policy release-policy.json --capacity-profile staging-capacity.json \
  --evidence release-evidence.json --stage preflight
```

策略记录 `deployment`、`revision`、两种降级拓扑描述 `degraded_topologies`、已批准的 `recovery_limits_seconds`、`canary_min_seconds` 和本次开放的 `features`。降级拓扑分别用 `worker_loss` 和 `database_failover` 键。RTO 按故障演练报告中的 `writer_ready_seconds`、`synchronous_protection_restored_seconds`、`first_new_ack_seconds`、`lease_recovered_seconds`、`backlog_drained_seconds`、`outbox_confirmed_seconds`、`fully_reconciled_seconds`、`second_writer_ready_seconds` 及 `restore_seconds` 分别配置。

证据索引每项使用 `{"deployment":"环境标识","revision":"制品版本","report":{"mode":"target"}}`。`report` 填入该环境对应作业的原始结果或经审核的现场记录。仓库自带的回环切换、恢复和回退演练会标记 `mode: local`，发布判定会拒绝这些本机报告。

| 证据键 | 记录内容 |
| --- | --- |
| `control`、`sustained`、`burst`、`soak`、`worker_loss`、`database_failover` | 保留对应目标模式的完整容量 JSON。两类降级证据各自使用实测拓扑和批准的降级负载。判定器复核档位、负载、连接预算、各流量组计量与 SLO。 |
| `business` | 已核对实例及业务效果数，确认任务丢失、重复效果、旧写入通过、未结差异。附受理和结果提交窗口、接收方提交后断连的业务账本核对结果。 |
| `failover`、`restore` | 各阶段实测耗时、两次切换核对数、旧主与旧 Worker 写入拒绝、同步保护和提交水位。恢复记录包含游标、租约、恢复点后效果、暂停发送、原业务键及最终对账结果。 |
| `rollback`、`canary` | 回退保留运行 ID、任务版本和原幂等记录，并由兼容 Worker 排空。推广阶段另核对试点观察时长、延迟积压目标、账本差异和零容忍计数。 |

字段契约由 [判定器](release_gate.py) 和 [结构验证示例](test_release_gate.py) 定义。发布作业应从可信环境收集并保存原始证据和审计记录。部署标识与版本标签用于交叉核对，现场故障隔离、业务账本和观察窗口由对应作业证明。

用一次性 PostgreSQL 数据库运行 `smoke.py --json` 可生成本机回退 JSON。演练校验同一幂等键在回退前后返回相同运行 ID，改用其他任务版本返回 409，最终由兼容 Worker 排空。

未来选择开放的能力单独留证：

- 业务日历核对时区、夏令时、例外调休、固定版本、换版边界和漏跑。
- 多地域核对网络分区和只写地域、业务全局去重、回切及声明的驻留边界。
- 不可信执行在实际目标运行时核对资源上限、任务取消、残留清理和凭据边界，验证后再开启准入。

## 本机复现结果

| 验收 | 记录 |
| --- | --- |
| 混合容量 | 五档均通过。持续混合最高通过档实测 62.58 条终态／秒，长稳期间迁出 512 条历史队列记录。少一个短任务 Worker 的档位实测 60.5 条终态／秒。 |
| 热点 A/B | 热点短任务计划投放 580 条／秒，持续 6 秒。两组使用相同的租户准入与并发预算。低流量租户终态 p99 在共享领取组为 547.52 ms，在保护领取组为 39.23 ms。共享组超出排队阈值，保护组通过全部阈值。 |
| 双主备切换 | 第一次核对 66 条已确认实例，丢失 0 条。第二次核对 67 条结果。旧端点拒绝写入，替代领取生效后的旧租约无法提交结果。库存事件投递两次，实际只预留一次共 6 个单位。恢复同步保护耗时 1.98 秒，完成对账耗时 10.05 秒。 |
| 时间点恢复 | 核对计划游标、活跃租约、原结果及 Outbox。独立账本保留恢复点之后的 1 笔业务效果。用原请求和业务键重放后，对账差异和重复效果均为 0。 |

使用本机夹具可复现上述路径。真实业务账本、目标拓扑容量、生产隔离机制和逐档放量均以对应环境的实际记录验收。
