# 多云资源管理

本目录是独立的 PostgreSQL 产品模块。目前提供项目与云账号授权、可恢复的多云盘点、具备审批和对账的资源操作，以及项目操作查询。

## PostgreSQL 本地验证

在当前目录运行。应用迁移使用专用数据库；测试使用有建 schema 权限的一次性数据库连接，并为每个测试创建及清理独立 schema。

```sh
go run ./cmd/cloud-manager migrate
go run ./cmd/cloud-manager api
go test -race -timeout 60s ./...
```

迁移读取 `CLOUD_MANAGER_DATABASE_URL`，集成测试读取 `CLOUD_MANAGER_TEST_DATABASE_URL`。同一数据库可重复执行迁移。API 默认监听 `127.0.0.1:8096`，可用 `CLOUD_MANAGER_LISTEN_ADDR` 设置。`/healthz` 报告进程状态，`/readyz` 检查所需迁移是否完成。测试输出中的 `TestAcceptancePostgresAuthorization` 和 `TestAcceptancePostgresInventory` 应为通过，跳过无法证明 PostgreSQL 语义。

## 资产盘点与恢复

- 账号的远端身份一经校验即固定。凭据引用和账号模式变化会提高代次。项目成员撤销或角色变化也提高代次。
- 盘点按租户、账号、地域和种类领取。空地域用于全局种类，具体映射由后续适配器提供。相同范围只允许一份有效租约。租约到期后保留游标并生成新代次，过期游标或凭据代次变化会重新扫描。
- 每页事务同时保存资产和检查点。重复对象按外部 ID 合并，停滞和循环游标被拒绝。扫描有可核对的分页上限。
- 完整扫描只产生缺失候选。点查明确确认缺失且候选和账号代次仍有效时，才保留本地移除记录。授权和限流失败保留既有资产。云适配器负责将权威缺失与暂时不可见区分。
- 操作应用使用安装角色迁移。运行角色在完成权限定义后由各进程单独装配。产品以独立模块依赖 PostgreSQL 驱动。

首发合同见 [范围](scope.md)、[能力矩阵](capabilities.yaml)、[提交边界](state-transitions.md) 和 [验收](acceptance.md)。

## OpenStack 只读盘点

以安装角色完成迁移，再建立租户、项目并通过下方账号 API 登记 `openstack` 类型的待校验账号。账号的凭据引用必须和只读应用凭据文件的 `credential_ref` 相同。文件路径由 `CLOUD_MANAGER_OPENSTACK_PROFILE` 指定，文件权限为 `0600` 或更严格。

```json
{
  "credential_ref": "openstack/site-a/reader/v1",
  "site": "site-a",
  "project": "<Keystone 项目 UUID>",
  "identity_url": "https://keystone.example.com/v3",
  "application_id": "<应用凭据 ID>",
  "secret": "<由密钥管理系统写入>",
  "endpoint_origins": ["https://nova.example.com", "https://neutron.example.com", "https://cinder.example.com", "https://glance.example.com"]
}
```

```sh
go run ./cmd/cloud-manager verify-openstack tenant-id account-id
go run ./cmd/cloud-manager scan-openstack tenant-id account-id - region
go run ./cmd/cloud-manager scan-openstack tenant-id account-id RegionOne instance
```

地域从已授权的 Keystone 服务目录提取。区域内可盘点 `zone`、`instance`、`volume`、`network`、`subnet`、`security-group`、`security-group-rule` 和 `image`。站点标识由运维分配，在同一 Keystone 部署的全部配置中保持一致。服务目录仅使用允许的 HTTPS 源。账号校验成功后固定站点和 Keystone 项目身份，待校验账号转为只读。轮换时发布新版本文件、通过账号 API 更新凭据引用，再校验远端身份。

失败会退出并让出领取，后续执行从已提交游标续跑。调用失败、权限不足以及可能掩盖未授权结果的 404 保持原有资产记录。相关协议参见 [Keystone](https://docs.openstack.org/api-ref/identity/v3/) 与 [Nova](https://docs.openstack.org/api-ref/compute/)。

## AWS EC2 只读盘点

使用 AWS SDK 的工作负载身份加载基础凭据，由它承担待纳管账号中的专用读取角色。角色信任配置中为每个账号绑定分配的外部 ID。通过账号 API 登记 `aws-ec2` 类型的待校验账号，凭据引用与 `CLOUD_MANAGER_AWS_PROFILE` 指向的私有文件一致。

```json
{
  "credential_ref": "aws/account-a/reader/v1",
  "region": "us-east-1",
  "role_arn": "arn:aws:iam::<账号 ID>:role/CloudInventoryReader",
  "external_id": "<此云账号的外部 ID>"
}
```

```sh
go run ./cmd/cloud-manager verify-aws tenant-id account-id
go run ./cmd/cloud-manager scan-aws tenant-id account-id - region
go run ./cmd/cloud-manager scan-aws tenant-id account-id us-east-1 instance
```

校验确认实际承担的角色、账号和分区，远端身份为 `分区:账号 ID`。区域内盘点 `zone`、`instance`、`volume`、`network`、`subnet`、`security-group`、`security-group-rule` 和账号自有 `image`。云端暂时不可见的点查结果保持待核对。AWS 停滞或失效的分页标识由控制器按协议拒绝或重新扫描。相关协议见 [STS GetCallerIdentity](https://docs.aws.amazon.com/STS/latest/APIReference/API_GetCallerIdentity.html) 与 [EC2 最终一致性](https://docs.aws.amazon.com/ec2/latest/devguide/eventual-consistency.html)。

### 安全组规则盘点

可以为 `security-group` 和 `security-group-rule` 分别登记定时扫描，也可手动执行。AWS 读角色需增加 `ec2:DescribeSecurityGroupRules`，Neutron 只读配置需允许列出和查看 Keystone 项目内的安全组规则。两个适配器均核对返回的账号或项目及父安全组标识。列表包括在产品外创建的规则，保留 IPv4、IPv6、协议、端口或 ICMP 字段及厂商返回的远端条件。 [EC2 DescribeSecurityGroupRules](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeSecurityGroupRules.html) · [Neutron List security group rules](https://docs.openstack.org/api-ref/network/v2/#list-security-group-rules)

```sh
go run ./cmd/cloud-manager scan-aws tenant-id aws-account us-east-1 security-group
go run ./cmd/cloud-manager scan-aws tenant-id aws-account us-east-1 security-group-rule
go run ./cmd/cloud-manager scan-openstack tenant-id openstack-account RegionOne security-group
go run ./cmd/cloud-manager scan-openstack tenant-id openstack-account RegionOne security-group-rule
```

规则可先落库，等同一租户、账号和地域的父安全组完成有效观测后才按父组归属显示。列表、历史、CSV 和拓扑都使用同一授权。规则的 `ownership_source` 为 `security_group`，归属版本和项目继承父组，组归属移转后自动生效。父组待确认缺失、标签归属冲突或项目账号授权被撤销时，规则停止可见。完整扫描中未返回的规则会显示为待确认缺失，单次 404 保留记录。

## 腾讯云、阿里云和华为云只读盘点

登记账号时，腾讯云使用 `tencent-cvm`，阿里云使用 `alibaba-ecs`，华为云使用 `huawei-ecs`。三者使用现有账号登记 API、凭据代次、持久分页及项目资产 API。区域内可盘点 `zone`、`instance`、`network`、`subnet`、`security-group`、`security-group-rule`、`volume` 和 `image`。使用 `- region` 查看配置的地域范围。

| 厂商 | 私有配置环境变量 | 远端身份绑定 | 手动扫描示例 |
| --- | --- | --- | --- |
| 腾讯云 CVM | `CLOUD_MANAGER_TENCENT_PROFILE` | STS 返回的 `AccountId`，即所属主账号 UIN | `cloud-manager scan-tencent TENANT ACCOUNT ap-guangzhou instance` |
| 阿里云 ECS | `CLOUD_MANAGER_ALIBABA_PROFILE` | STS 返回的 `AccountId` | `cloud-manager scan-alibaba TENANT ACCOUNT cn-hangzhou instance` |
| 华为云 ECS | `CLOUD_MANAGER_HUAWEI_PROFILE` | IAM Token 返回的账号 Domain ID 和项目 ID | `cloud-manager scan-huawei TENANT ACCOUNT cn-north-4 instance` |

私有配置文件权限使用 `0600` 或更严格。通用字段为 `credential_ref`、`regions` 和 `account_id`。腾讯云与阿里云使用 `access_key_id`、`access_key_secret` 和临时凭据所需的 `security_token`，使用仅具备扫描所需权限的 CAM 或 RAM 身份。华为云配置一个地域及对应的 `project_id`，由凭据管理流程提供项目级 `iam_token`。华为云 Token 临近过期前需替换私有文件中的同项目 Token。更换凭据引用时先通过账号接口轮换，再核对远端身份。 [腾讯云 STS](https://cloud.tencent.com/document/api/1312/66098) · [阿里云 STS](https://api.alibabacloud.com/document/Sts/2015-04-01/GetCallerIdentity) · [华为云 Token 验证](https://support.huaweicloud.com/intl/en-us/ally-visitor-1-api-iam/en-us_topic_0057845585.html)

分别使用 `cloud-manager verify-tencent TENANT ACCOUNT`、`cloud-manager verify-alibaba TENANT ACCOUNT` 或 `cloud-manager verify-huawei TENANT ACCOUNT` 核验远端身份。核验通过后账号进入 `read_only`，后续扫描验证目标资源的读取权限。定时扫描沿用 `cmp_inventory_schedules`，并在 `CLOUD_MANAGER_CONTROLLER_PROFILES` 的映射文件中加入相同的凭据引用和私有配置文件路径。主机资产 API 返回厂商、地域、状态和最近观测时间。 [腾讯云 CVM](https://cloud.tencent.com/document/product/213/15728) · [阿里云 ECS](https://help.aliyun.com/en/ecs/developer-reference/api-ecs-2014-05-26-describeinstances) · [华为云 ECS](https://support.huaweicloud.com/intl/en-us/api-ecs/ecs_02_0107.html)

将上述命令末尾的 `instance` 改为其他资源种类即可盘点相应资源。网络、块存储、镜像、可用区与主机权限分别授予。

| 厂商 | 只读网络 API | 分页及作用域 |
|---|---|---|
| 腾讯云 | [DescribeVpcs](https://cloud.tencent.com/document/api/215/15778)、[DescribeSubnets](https://cloud.tencent.com/document/api/215/15784)、[DescribeSecurityGroups](https://cloud.tencent.com/document/product/215/15808) | 走 VPC API 的字符串 `Offset` 和 `Limit`，安全组响应中的地域须与配置相符 |
| 阿里云 | [DescribeVpcs](https://www.alibabacloud.com/help/en/vpc/developer-reference/api-vpc-2016-04-28-describevpcs)、[DescribeVSwitches](https://www.alibabacloud.com/help/en/vpc/developer-reference/api-vpc-2016-04-28-describevswitches)、[DescribeSecurityGroups](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-describesecuritygroups) | VPC 和交换机使用页号并按已验证账号筛选及核对资源拥有者。安全组使用 ECS 的 `NextToken` |
| 华为云 | [ListVpcs](https://support.huaweicloud.com/api-vpc/vpc_apiv3_0003.html)、[ListVirsubnets](https://support.huaweicloud.com/intl/en-us/api-vpc/ListVirsubnets.html)、[ListSecurityGroups](https://support.huaweicloud.com/api-vpc/vpc_apiv3_0011.html) | 使用项目级 VPC v3 API，逐条核对项目 ID，按 `page_info.next_marker` 续页 |

项目的 `/assets/topology` 会解析已经盘点且对该项目可见的目标。三家均提供子网到 VPC 以及云主机到 VPC、安全组的关系。腾讯云和阿里云还会记录云主机到子网的关系，阿里云会记录安全组到 VPC 的关系。只读列表未提供生命周期状态的资源以 `observed` 展示。

块存储盘点分别调用腾讯云 CBS、阿里云 ECS 和华为云 EVS。卷拓扑展示厂商返回的挂载关系，同一卷到同一主机只保留一条边。华为云续页从厂商返回的链接中提取校验后的标记，请求仍发送到区域 EVS 端点。 [腾讯云 CBS DescribeDisks](https://cloud.tencent.com/document/product/362/16315) · [阿里云 ECS DescribeDisks](https://www.alibabacloud.com/help/zh/ecs/developer-reference/api-ecs-2014-05-26-describedisks) · [华为云 EVS ListVolumes](https://support.huaweicloud.com/api-evs/evs_04_2006.html)

镜像盘点限定在已验证账号或 IAM 项目拥有的镜像。腾讯云发送并核对 `PRIVATE_IMAGE`，阿里云发送并核对 `ImageOwnerAlias=self`，华为云发送并核对项目 `owner`。拓扑会关联已纳管的实例启动镜像。 [腾讯云 CVM DescribeImages](https://cloud.tencent.com/document/api/213/15715) · [阿里云 ECS DescribeImages](https://www.alibabacloud.com/help/zh/ecs/developer-reference/api-ecs-2014-05-26-describeimages) · [华为云 IMS ListImages](https://support.huaweicloud.com/api-ims/ims_03_0602.html)

可用区使用厂商在实例、子网和卷中引用的区标识，支持三类资源的 `in_zone` 拓扑。阿里云和华为云的可用区列表仅提供区信息，资产状态记为 `observed`。 [腾讯云 CVM DescribeZones](https://cloud.tencent.com/document/product/213/15707) · [阿里云 ECS DescribeZones](https://www.alibabacloud.com/help/zh/ecs/developer-reference/api-ecs-2014-05-26-describezones) · [华为云 ECS ListServerAzInfo](https://support.huaweicloud.com/intl/zh-cn/api-ecs/ecs_02_1501.html)

安全组规则沿用父安全组的项目可见范围。部署时分别为 `security-group` 和 `security-group-rule` 配置扫描计划，使项目查询能解析规则归属。腾讯云和阿里云从已验证账号的安全组列表逐组读取，阿里云还检查规则页返回的地域和安全组。华为云按项目分页并逐条核对项目 ID。三家都保留入站、出站和允许或拒绝等字段。 [腾讯云 DescribeSecurityGroupPolicies](https://cloud.tencent.com/document/api/215/15804) · [阿里云 DescribeSecurityGroupAttribute](https://www.alibabacloud.com/help/zh/ecs/developer-reference/api-ecs-2014-05-26-describesecuritygroupattribute) · [华为云 ListSecurityGroupRules](https://support.huaweicloud.com/api-vpc/vpc_apiv3_0017.html)

阿里云、华为云使用厂商规则 ID。腾讯云规则资产使用基于父组、方向、匹配条件、动作及描述计算的 ID，重复规则按出现次数区分。单纯调整排序或优先级会更新资产展示，规则 ID 保持稳定。

已通过厂商协议 TLS 夹具和真实 PostgreSQL 验收，覆盖三家多页扫描、规则页限流后的断点续跑、作用域不匹配拒绝、卷挂载去重、镜像归属和拓扑查询。真实云账号的地域、权限及限流行为仍需在沙箱验收。

#### 云盘快照盘点

应用 `0040_snapshot_inventory` 和 `0041_snapshot_schedules` 后，用原账号和地域配置运行以下任一命令，也可登记 `kind=snapshot` 的定时计划。

```sh
cloud-manager scan-tencent TENANT ACCOUNT REGION snapshot
cloud-manager scan-alibaba TENANT ACCOUNT REGION snapshot
cloud-manager scan-huawei TENANT ACCOUNT REGION snapshot
```

| 厂商 | 盘点方式 |
| --- | --- |
| 腾讯云 | CBS 按偏移续页，核对返回的快照为账号拥有的私有快照。 [DescribeSnapshots](https://cloud.tencent.com/document/product/362/15647) |
| 阿里云 | ECS 原样续传 `NextToken`，逐条核对快照地域。 [DescribeSnapshots](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-describesnapshots) |
| 华为云 | 默认按项目使用 V2 存量接口。支持 V5 的地域可在凭据配置设置 `"huawei_snapshot_api": "v5"`，通过按 ID 递增的标记盘点标准及存量快照。V5 完成须看到末页空结果。切换接口时旧游标会失效，重跑时从首段开始。 [V2 存量快照](https://support.huaweicloud.com/api-evs/evs_04_2021.html) · [V5 快照详情](https://support.huaweicloud.com/api-evs/ListSnapshotsDetailsV5.html) |

控制台资产清单支持云盘快照类型和源卷检索。API 返回 `parent_id`、厂商快照类别 `snapshot.class` 和已知逻辑容量 `snapshot.size_gib`。历史接口可返回 `previous_snapshot` 和 `snapshot`。筛选 `kind=snapshot` 的 CSV 追加 `source_volume_id`、`snapshot_class` 和 `size_gib`，拓扑按项目权限展示已纳管的源卷。

快照沿用同账号、同地域源卷的项目归属和版本。跨地域复制或来源未确认的快照先持久保存，等待可验证的源卷归属。源卷已删除且本地保留归属记录时，项目仍可查看快照。盘点证据若试图改换已确认的源卷，保留原记录和检查点。完整扫描中的缺失只标记为待核对。

#### 创建云盘快照

应用 `0042_snapshot_creation` 并升级 API 和所有 Worker。账号须允许写入，凭据配置须显式设置 `"allow_snapshot_creation": true`。腾讯云、阿里云和华为云的云盘资产都可从控制台提交。API 使用 `POST /v1/tenants/{tenant}/projects/{project}/snapshots`。请求须带 `Idempotency-Key`，并在 `If-Match` 中传入源卷的当前归属版本。

```sh
cloud-manager operations create-snapshot TENANT PROJECT --idempotency-key KEY --if-match VOLUME_REVISION <<'JSON'
{"account":"account","region":"ap-guangzhou","volume_id":"disk-example","name":"backup-before-update"}
JSON
```

返回的 `Location` 指向统一项目操作记录。操作初始为 `awaiting_approval`，由独立审批人通过 `{Location}/approval` 批准。Worker 在发送前复核权限、源卷归属版本和厂商云盘状态。同一源卷的活动快照申请与挂卷、卸卷操作互斥。云盘已挂载时，申请须显式设置 `"allow_attached": true`，表示接受崩溃一致性。应用一致性依赖事前完成写入同步。平台内的互斥覆盖本系统发起的动作，云管平台外的变更需通过运维权限控制。

腾讯云使用 CBS 创建标签关联的快照。阿里云使用 ECS 创建标签关联的快照并传入持久化的 `ClientToken`。华为云由 `huawei_snapshot_api` 决定创建类型，默认 V2 创建存量快照并通过元数据关联。V5 创建标准快照并通过描述关联。V2 仅在预检确认卷已挂载且申请人已授权时发送 `force: true`。账号须能点查云盘、创建快照和读取本账号快照，并允许创建请求附带关联字段。 [腾讯云 CreateSnapshot](https://cloud.tencent.com/document/product/362/15648) · [阿里云 CreateSnapshot](https://help.aliyun.com/en/ecs/developer-reference/api-ecs-2014-05-26-createsnapshot) · [华为云 V2](https://support.huaweicloud.com/intl/zh-cn/api-evs/evs_04_2016.html) · [华为云 V5](https://support.huaweicloud.com/intl/en-us/api-evs/CreateSnapshotV5.html)

云端受理后，Worker 只读对账。确认账号地域、快照关联标记、源卷及可用状态后，同事务登记资产并发出 `snapshot.operation.succeeded`。回执丢失时仍可使用关联标记寻找唯一快照。证据不足进入 `needs_review`，独立复核人可通过 `{Location}/retry-observation` 恢复只读查询。本轮验收覆盖协议夹具和本地 PostgreSQL，真实厂商沙箱仍待验收。

#### 删除云盘快照

应用 `0043_snapshot_deletion`，升级 API 和所有 Worker，并在对应账号的凭据配置显式设置 `"allow_snapshot_deletion": true`。控制台可从云盘快照资产发起删除。API 使用 `POST /v1/tenants/{tenant}/projects/{project}/snapshots/delete`。请求须带 `Idempotency-Key`，`If-Match` 使用该快照继承的源卷归属版本。

```sh
cloud-manager operations delete-snapshot TENANT PROJECT --idempotency-key KEY --if-match VOLUME_REVISION <<'JSON'
{"account":"account","region":"ap-guangzhou","asset_id":"snap-example","volume_id":"disk-example"}
JSON
```

操作初始为 `awaiting_approval`，独立审批后才可能发出删除。Worker 复核快照与源卷的关联、项目归属、快照类别及云端状态。源卷已移除时，只要保留了可校验的源卷归属，仍可处理遗留快照。同一源卷的活动快照创建与删除互斥。

| 厂商 | 适用条件和接口 |
| --- | --- |
| 腾讯云 | 仅删除正常、未锁定、未共享、无镜像引用且无活动自动快照策略引用的私有快照。请求使用 `DeleteBindImages: false`。 [CBS DeleteSnapshots](https://cloud.tencent.com/document/api/362/15645) |
| 阿里云 | 仅删除创建完成且无派生资源引用的手动快照。请求使用 `Force=false`。 [ECS DeleteSnapshot](https://help.aliyun.com/en/ecs/developer-reference/api-ecs-2014-05-26-deletesnapshot) |
| 华为云 | 按已审批的类别选择 V2 存量或 V5 标准删除。要求快照可用，保护备份快照和快照一致性组成员。V5 要求手动创建。 [EVS V2 DeleteSnapshot](https://support.huaweicloud.com/api-evs/evs_04_2017.html) · [EVS V5 DeleteSnapshot](https://support.huaweicloud.com/api-evs/DeleteSnapshotV5.html) |

发送记录落库后只读对账。连续两轮精确查询均返回完整的空结果时，同一事务标记资产删除、终结操作并提交 `snapshot.operation.succeeded`。泛化 404、分页不完整或来源错误都不能作为缺失证据。证据不足时，独立复核人可以恢复只读观测。较旧的盘点结果不会恢复确认删除的资产。已在本机 TLS 夹具和 PostgreSQL 中验收，真实厂商沙箱仍待验收。

#### 从快照恢复到新云盘

应用 `0044_snapshot_restore` 并升级 API 和所有 Worker。凭据配置中的 `snapshot_restore_max_gib` 默认为 0。设置为正整数可启用恢复，同时限定单块恢复盘的容量。仅云账号所属项目可申请恢复，源快照须有可验证的源卷归属。控制台的快照资产行可发起恢复，API 使用 `POST /v1/tenants/{tenant}/projects/{project}/snapshots/restore`。

```sh
cloud-manager operations restore-snapshot TENANT PROJECT --idempotency-key KEY --if-match VOLUME_REVISION <<'JSON'
{"account":"account","region":"ap-guangzhou","asset_id":"snap-example","volume_id":"disk-example","name":"restored-volume","zone":"ap-guangzhou-2","disk_type":"CLOUD_SSD","size_gib":40,"encrypted":false}
JSON
```

`If-Match` 使用源卷归属版本。审批记录固定源快照、源卷、磁盘名称、可用区、盘型、容量和加密状态。恢复会创建一块未挂载的按需数据盘，源快照的加密状态必须与申请一致，容量须覆盖快照数据。源卷上的活动快照创建、删除和恢复互斥。

| 厂商 | 恢复与对账 |
| --- | --- |
| 腾讯云 | CBS 创建单块后付费数据盘，验证私有数据盘快照、关联标签及磁盘规格。目标盘型支持 `CLOUD_PREMIUM`、`CLOUD_SSD`、`CLOUD_BSSD`。 [CreateDisks](https://cloud.tencent.com/document/api/362/16312) |
| 阿里云 | ECS 创建后付费数据盘，以关联标签和 `SourceSnapshotId` 验证来源。目标盘型支持 `cloud_essd`、`cloud_ssd`、`cloud_efficiency`。归档快照须先恢复为可用类别。 [CreateDisk](https://help.aliyun.com/en/ecs/developer-reference/api-ecs-2014-05-26-createdisk) |
| 华为云 | 按类别核验 V2 存量或 V5 标准快照，再由 EVS 创建一块按需云硬盘。可用区和盘型须符合 EVS 的源快照约束。对账核对项目、描述标记与 `snapshot_id`，以原快照的加密属性创建。 [CreateVolume](https://support.huaweicloud.com/api-evs/evs_04_2003.html) |

放行发送后只读对账。Worker 通过厂商返回的资源 ID 或恢复专用标记核对唯一云盘，确认可用状态、目标规格与加密后，同事务写入项目归属、`restored_from` 拓扑关联及 `volume.operation.succeeded` 事件。回执丢失、证据不一致和独立复核的续查均沿用同一操作。真实厂商沙箱仍待验收。

#### 将快照回滚到原云盘

应用 `0045_snapshot_rollback` 并升级 API 和 Worker。在对应账号的凭据配置中显式设置 `allow_snapshot_rollback` 为 `true`。控制台可从快照资产发起，API 入口为 `POST /v1/tenants/{tenant}/projects/{project}/snapshots/rollback`。`If-Match` 使用源卷归属版本。回滚会覆盖原卷上快照之后的写入。申请须确认覆盖，并经独立审批。

```bash
cloud-manager operations rollback-snapshot TENANT PROJECT --idempotency-key KEY --if-match VOLUME_REVISION <<'JSON'
{"account":"account","region":"ap-guangzhou","asset_id":"snap-example","volume_id":"disk-example","confirm_overwrite":true}
JSON
```

Worker 点查源快照和原卷，只受理厂商确认当前未挂载的原数据盘。同一源卷的快照创建、删除、恢复、回滚以及挂卸载与活动回滚互斥。发送记录先于云端请求落库，回执丢失时保留操作和互斥状态，等待证据。

| 厂商 | 回滚和完成证据 |
| --- | --- |
| 腾讯云 | `ApplySnapshot` 返回请求 ID。独立复核人将厂商证据引用与原请求 ID 一同记录。 [腾讯云 ApplySnapshot](https://cloud.tencent.com/document/api/362/15643) |
| 阿里云 | `ResetDisk` 返回请求 ID。独立复核人根据账号、地域、源快照和原卷核对厂商证据。可回滚标准及极速快照。 [阿里云 ResetDisk](https://help.aliyun.com/zh/ecs/developer-reference/api-ecs-2014-05-26-resetdisk) |
| 华为云 | V2 存量快照回执确认目标卷，由独立复核人记录完成证据。V5 标准快照按原回执的 Job ID 在相同项目和地域内查询，终态须匹配原卷，支持独立复核人恢复只读查询。 [华为云 V2](https://support.huaweicloud.com/api-evs/evs_04_2020.html) · [华为云 V5](https://support.huaweicloud.com/api-evs/RollbackSnapshotV5.html) · [华为云 ShowJob](https://support.huaweicloud.com/api-evs/evs_04_0054.html) |

结果待复核时，审批人可通过控制台或 `/operations/{operation}/resolution` 记录独立核验的结果，引用工单或厂商审计记录。厂商请求 ID 已保存时须提供同一 ID。Job ID 仍可用的华为云 V5 操作也可调用 `/operations/{operation}/retry-observation`。终态与发件箱事件同事务记录。真实厂商沙箱仍待验收。

#### 从已关机实例制作系统盘镜像

运行 `cloud-manager migrate` 应用 `0048_instance_image_reservations` 和 `0049_image_creation` 后升级 API 和 Worker。在账号凭据中设置 `allow_image_creation=true` 启用制作。控制台的已关机实例行提供申请入口。API 使用 `POST /v1/tenants/{tenant}/projects/{project}/images`，提交 `account`、`region`、`instance_id` 和 `name`，并携带 `Idempotency-Key` 与源实例归属版本 `If-Match`。名称为 2 至 60 个字符，以英文字母开头，可包含数字、下划线和连字符。

申请需独立审批。同一实例的电源、挂卸盘、删除、显式归属转移和其他镜像制作与活动申请互斥。Worker 在发送前点查厂商状态，确认实例已关机。腾讯云发送 `ForcePoweroff=false`，华为云调用新版 IMS 系统盘镜像接口。阿里云按实例创建镜像会对所有挂载云盘创建快照，因此只受理厂商完整点查确认仅挂载系统云盘的实例。源实例在制作完成前须保持关机和磁盘配置稳定。[腾讯云 CreateImage](https://cloud.tencent.com/document/product/213/16726) · [阿里云 CreateImage](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-createimage) · [华为云 CreateImageInSafeMode](https://support.huaweicloud.com/api-ims/ims_03_0603_1.html)

发送记录先于写请求落库。Worker 在原账号与地域核对唯一的关联标记和镜像状态，已收到镜像 ID 时还须匹配原回执。华为云收到任务回执时，先读取同一 IMS 任务并核对类型和目标镜像。常规观测约 30 分钟。证据冲突或超出观测次数时进入待复核，保留源实例预约，可由独立复核人恢复只读查询。镜像可用后，同事务登记项目归属、源实例关系及 `image.operation.succeeded`，并释放源实例预约。真实厂商沙箱仍待验收。[华为云 ShowJobProgress](https://support.huaweicloud.com/api-ims/ims_03_0911.html)

#### 跨地域复制自有镜像

运行 `cloud-manager migrate` 应用 `0050_image_copy_reservations` 和 `0051_image_copy` 后升级 API 和 Worker。在源云账号凭据中设置 `allow_image_copy=true` 启用复制。控制台从自有镜像资产发起，API 使用 `POST /v1/tenants/{tenant}/projects/{project}/images/copy`。请求提交源 `account`、`region`、`asset_id`，以及 `destination_region`、`destination_account`、`name`，并携带 `Idempotency-Key` 和源镜像归属版本 `If-Match`。目标镜像名称为 2 至 60 个字符，以英文字母开头，可包含数字、下划线和连字符。两端账号须归当前平台项目且启用写入。

| 厂商 | 目标配置和恢复依据 |
| --- | --- |
| 腾讯云 | 源和目标使用同一账号，目标地域须在凭据配置中。使用 `SyncImages` 返回的目标地域和镜像 ID 对账。回执缺少可验证目标 ID 时保留待复核，禁止仅凭同名镜像认领。 [SyncImages](https://cloud.tencent.com/document/api/213/15711) |
| 阿里云 | 源和目标使用同一账号，目标地域须在凭据配置中。传入持久 `ClientToken` 和关联标记。省略加密选项，由厂商保留源加密状态或按目标账号的默认加密策略加强。 [CopyImage](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-copyimage) |
| 华为云 | 指定同一企业账号下另一个已登记、已验证的目标 IAM 项目。源凭据配置 `huawei_image_copy_agency`，并为该委托配置目标地域所需权限。Worker 从目标令牌响应获取项目名称。当前只提交可用的未加密私有系统盘镜像。使用任务和目标项目回执确认，回执丢失时按目标项目、名称和关联描述分页确认唯一结果。 [CopyImageCrossRegion](https://support.huaweicloud.com/api-ims/ims_03_0628.html) · [ShowJobProgress](https://support.huaweicloud.com/api-ims/ims_03_0911.html) |

独立审批前后均校验两端权限、源镜像归属版本和凭据代次。活动复制与源镜像删除、显式归属转移及其他复制互斥。写请求只发送一次。已保存目标 ID 时，观测结果须匹配原回执。证据不符时进入待复核并保留源镜像预约。独立复核可授权轮换后的凭据继续只读核查，源归属和标签映射仍须匹配冻结记录。确认目标镜像可用后，同事务登记目标资产、记录终态事件并释放预约。跨地域来源保存在同一项目的操作详情。真实厂商沙箱仍待验收。

#### 删除自有镜像

运行 `cloud-manager migrate` 应用 `0046_image_deletion` 和 `0047_image_deletion_outbox` 后升级 API 和 Worker。账号凭据中的 `allow_image_deletion` 默认为关闭。控制台可从自有镜像资产发起申请，也可调用 `POST /v1/tenants/{tenant}/projects/{project}/images/delete`，提交 `account`、`region`、`asset_id`，并携带 `Idempotency-Key` 和当前镜像归属版本 `If-Match`。

已纳管的实例、待执行的实例申请或启用的服务目录模板引用镜像时，删除申请返回 409。镜像删除待审批、执行或复核时，对同一镜像的新实例和目录发布申请也会被拦截。活动删除操作期间也禁止显式转移镜像的项目归属。返回的 `Location` 指向统一项目操作详情。独立审批后，Worker 再核对授权、依赖和厂商镜像状态。

| 厂商 | 点查与删除策略 |
| --- | --- |
| 腾讯云 | 点查 `PRIVATE_IMAGE` 且处于 `NORMAL` 的镜像。删除传 `DeleteBindedSnap=false`，共享限制由厂商执行。 [DescribeImages](https://cloud.tencent.com/document/api/213/15715) · [DeleteImages](https://cloud.tencent.com/document/api/213/15716) |
| 阿里云 | 点查本地域的自有、可用、未共享、未被实例使用的镜像，发送 `Force=false`。 [DescribeImages](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-describeimages) · [DeleteImage](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-deleteimage) |
| 华为云 | 按镜像 ID 查询并复核 IAM 项目，要求可用、未保护的私有镜像，发送 `delete_backup=false`。 [ListImages](https://support.huaweicloud.com/api-ims/ims_03_0602.html) · [GlanceDeleteImage](https://support.huaweicloud.com/api-ims/ims_03_0706.html) |

发送记录先于外部请求落库。Worker 连续两次按原镜像 ID 确认缺失后，在同一事务写入删除标记、审计终态及 `image.operation.succeeded` 事件。异常结果转入 `needs_review`。独立审批人可通过控制台或 `/operations/{operation}/retry-observation` 恢复只读核查。删除请求只发一次，关联快照与备份保留。真实厂商沙箱仍待验收。

#### 弹性公网 IP 盘点

应用 `0035_inventory_public_addresses` 和 `0036_network_asset_schedules` 后，使用 `scan-tencent`、`scan-alibaba` 或 `scan-huawei`，在账号和地域参数后传入 `elastic-ip`。也可用同一资源种类登记定时扫描。腾讯云使用 `DescribeAddresses` 的偏移分页并检查返回的地址类型。阿里云使用 `DescribeEipAddresses` 的页号分页并逐条校验地域。华为云使用原 IAM 项目的 V3 列表和厂商标记。厂商单页超过本地上限时，分批提交并使用内容指纹检测续读期间的变化。[腾讯云 API](https://cloud.tencent.com/document/api/215/16702) · [阿里云 API](https://www.alibabacloud.com/help/en/eip/developer-reference/api-vpc-2016-04-28-describeeipaddresses-eips) · [华为云 API](https://support.huaweicloud.com/api-eip/ListPublicipsV3.html)

项目资产列表与控制台支持按公网地址搜索。已分配的地址以规范化后的 `public_addresses` 返回。双栈资产按 IPv4、IPv6 顺序记录。地址变化会写入资产历史，保留变化前后的地址。筛选 `kind=elastic-ip` 的 CSV 在末尾追加 `public_addresses`，同一资产的多个地址用空格分隔。拓扑只展示可验证的云主机或 VPC 关联，并沿用项目可见范围。

#### 申请弹性公网 IP

应用 `0037_elastic_ip_creation` 后，可从控制台资产清单申请，也可向 `POST /v1/tenants/{tenant}/projects/{project}/network-operations` 提交。请求需要 `Idempotency-Key`，正文使用 `kind=elastic-ip`、`account`、`region`、`name` 和整数 `bandwidth_mbps`。带宽范围为 5 至 200 Mbps。华为云还需要 `address_pool`，例如 `5_bgp`。CLI 使用 `cloud-manager operations create-network TENANT PROJECT --idempotency-key KEY < elastic-ip.json` 调用同一入口。

账号须处于可写状态，Worker 凭据须配置 `elastic_ip_bandwidth_limit`。默认值为零，关闭申请。华为云配置中的 `huawei_elastic_ip_pool` 须与申请一致。每笔操作由独立审批人审核账号、地域、名称、带宽及地址池。批准后发送前会复核申请人与审批人的权限和凭据版本。

每笔操作申请一个未绑定的 IPv4 地址，采用按流量后付费。腾讯云申请常规 BGP 地址并使用共享地址池，阿里云申请 BGP 后付费地址，华为云使用已配置的地址池和独享带宽。腾讯云和阿里云携带持久 `ClientToken` 并在申请时打关联标签，华为云在首次请求写入关联描述。[腾讯云 AllocateAddresses](https://cloud.tencent.com/document/product/215/16699) · [阿里云 AllocateEipAddress](https://www.alibabacloud.com/help/en/eip/developer-reference/api-vpc-2016-04-28-allocateeipaddress-eips) · [华为云 CreatePublicIpV3](https://support.huaweicloud.com/api-eip/CreatePublicIpV3.html)

Worker 最多发送一次申请。用回执 ID 或关联标记只读核对，确认资源唯一、尚未绑定，且地址类型、带宽和计费设置与申请相符。结果不明时继续对账，证据冲突进入 `needs_review`。独立审批人可恢复只读观测。确认成功后，将地址、项目归属、终态与 `elastic-ip.operation.succeeded` 在同一事务内保存。

#### 绑定与解绑弹性公网 IP

应用 `0039_elastic_ip_binding` 并升级 Worker 后，可在控制台资产行提交申请，或向 `POST /v1/tenants/{tenant}/projects/{project}/network-operations/bind-elastic-ip` 和 `/unbind-elastic-ip` 提交。正文包含 `account`、`region`、`asset_id`、`public_address`、`instance_id` 和整数 `instance_revision`。华为云同时提供实例的 `port_id`。请求使用公网 IP 当前归属版本作为 `If-Match`，并提供 `Idempotency-Key`。控制台按同项目、账号和地域核对实例并填入版本。CLI 调用同一合同。

```sh
cloud-manager operations bind-elastic-ip TENANT PROJECT --idempotency-key KEY --if-match REVISION < bind-eip.json
cloud-manager operations unbind-elastic-ip TENANT PROJECT --idempotency-key OTHER_KEY --if-match REVISION < unbind-eip.json
```

Worker 凭据需显式开启 `allow_elastic_ip_binding`，每笔操作须独立审批。两个资源均须归属当前项目，且位于同一可写账号与地域。公网 IP 的其他活动操作、同实例的其他绑定或解绑，以及实例删除会与本申请冲突。实例删除预览通过 `active_elastic_ip_binding` 显示未结束的绑定操作。

- 腾讯云支持 CVM 实例绑定，拒绝封堵、隔离或配置联动释放的地址，解绑沿用不分配普通公网地址的厂商默认值。 [绑定](https://cloud.tencent.com/document/api/215/16700) · [解绑](https://cloud.tencent.com/document/product/215/16703)
- 阿里云支持同地域 VPC ECS 实例，使用持久的 `ClientToken`，解绑请求同时指定原实例 ID。 [绑定](https://www.alibabacloud.com/help/en/eip/developer-reference/api-vpc-2016-04-28-associateeipaddress-eips) · [解绑](https://www.alibabacloud.com/help/en/eip/developer-reference/api-vpc-2016-04-28-unassociateeipaddress-eips)
- 华为云核对项目、ECS 实例及网卡的 IPv4 地址，并按该网卡端口绑定。绑定前拒绝已有弹性公网 IP 的网卡。 [绑定](https://support.huaweicloud.com/intl/en-us/api-eip/AssociatePublicips.html) · [解绑](https://support.huaweicloud.com/api-eip/DisassociatePublicips.html) · [ECS 网卡查询](https://support.huaweicloud.com/intl/zh-cn/api-ecs/ecs_02_0505.html)

发送前点查原地址和绑定关系，记录发送后只读对账。连续两轮确认目标关系后，在同一事务更新拓扑、资产状态和终态事件，并使旧盘点租约失效。解绑须看到原 IPv4 地址仍存在且已解除关联。发现其他实例、端口或地址变化时进入人工复核，审批人可恢复只读观测。厂商解绑接口按地址执行时，应通过单一写入方避免外部控制台在点查与发送之间改绑。

#### 释放弹性公网 IP

应用 `0038_elastic_ip_release` 后，可在资产清单的未绑定弹性公网 IP 行申请释放，或向 `POST /v1/tenants/{tenant}/projects/{project}/network-operations/release-elastic-ip` 提交。正文必填 `account`、`region`、`asset_id` 和 `public_address`。使用该资产的当前归属版本作为 `If-Match`，并提供 `Idempotency-Key`。CLI 调用同一入口。

```sh
cloud-manager operations release-elastic-ip TENANT PROJECT --idempotency-key KEY --if-match REVISION < release-eip.json
```

账号须处于可写状态，Worker 凭据须显式开启 `allow_elastic_ip_release`，每笔申请须独立审批。发送前复核申请、审批及账号凭据代次，并按原账号、地域和资源 ID 核对公网地址、绑定、计费和厂商保护状态。腾讯云支持常规按流量后付费 EIP，拒绝封堵或隔离的地址。阿里云支持无共享带宽的按量 EIP，拒绝删除保护、服务托管或连续地址段。华为云支持独享带宽且未锁定的按需 IPv4 EIP。[腾讯云 ReleaseAddresses](https://cloud.tencent.com/document/product/215/16705) · [阿里云 ReleaseEipAddress](https://www.alibabacloud.com/help/en/eip/developer-reference/api-vpc-2016-04-28-releaseeipaddress-eips) · [华为云 DeletePublicIp](https://support.huaweicloud.com/api-eip/DeletePublicIp.html)

记录发送后只读对账。对原资源 ID 连续两次查询得到完整、无后续页的空结果后，资产移除、操作成功和终态事件一并提交。证据矛盾可由独立审批人恢复只读查询。释放后可能无法取回原地址。


## 项目资产 API

`GET /v1/tenants/{tenant}/projects/{project}/assets` 读取本地观测结果。以项目所属的服务主体凭据发送 `Authorization: Bearer …`。部署的凭据登记流程将至少 32 字节的随机凭据交付服务主体，并只在 `cmp_service_tokens` 保存其 SHA-256 二进制摘要、主体标识和有效期。更新撤销时间可立即停用后续请求。授权需同时满足同租户、有效项目成员，以及账号归属或共享授权。

可选查询参数包括 `account`、`region`、`kind`、`q`、`limit` 和 `cursor`。默认每页 50 条，上限 200 条。翻页沿用前页筛选参数。返回厂商状态、本地盘点状态、观测时间及最近完整扫描时间。资源列表为实时分页，期间发生的资产变更可能出现在后续页。合同由 `GET /openapi.json` 提供。

## 定时盘点控制器

完成迁移后，运维可在 `cmp_inventory_schedules` 中按云账号、地域和资源类型登记扫描计划。间隔范围为 60 秒至 7 天。全局 `region` 盘点使用空地域。

```sql
INSERT INTO cmp_inventory_schedules (tenant_id, account_id, region, kind, interval_seconds)
VALUES ('tenant-id', 'account-id', 'RegionOne', 'instance', 600);
```

`CLOUD_MANAGER_CONTROLLER_PROFILES` 指向私有 JSON 文件，格式为凭据引用到对应厂商私有配置文件路径的映射。以 `go run ./cmd/cloud-manager controller` 运行。每个控制器进程串行执行盘点，可由多个进程共享数据库领取不同计划。

成功后从完成时间计算下一次运行。失败按指数退避，厂商提供的限流等待会在 24 小时上限内纳入计算。计划记录最近结果和连续失败数。正常停机主动让出领取，进程异常退出则在租约到期后由其他实例接管。盘点沿用已提交的游标。停用计划或账号会停止后续领取。

## 资产差异历史

`GET /v1/tenants/{tenant}/projects/{project}/assets/changes` 使用与资产列表相同的授权和筛选参数，按时间倒序分页。事件包括新发现、名称或状态变化、待确认缺失、重新发现和确认移除。响应包含变化前后的名称与状态。

资产写入和差异记录在同一 PostgreSQL 事务提交。未变化的重复扫描更新观测时间。升级前的存量资产从升级后的第一次实际变化开始记录历史。历史按读取时的资源归属及项目授权计算。

## 项目资产 CSV 导出

`GET /v1/tenants/{tenant}/projects/{project}/assets/export` 支持 `account`、`region`、`kind` 和 `q`。导出按当前项目授权返回一个已提交的本地资产快照。单次最多 2000 条，超过上限返回 422，可使用筛选条件缩小范围。文本字段会处理电子表格可能识别为公式的前缀。

## 项目资产拓扑

`GET /v1/tenants/{tenant}/projects/{project}/assets/topology` 沿用资产筛选和游标，每页最多 50 个源资产。每个节点返回资产及其已解析的出向关系。关系来自厂商响应中可用的稳定引用，例如实例所在网络和子网、关联磁盘、镜像和安全组。不同厂商公开的关系字段各有差异。

只有同一租户、账号和地域中已经盘点且未确认移除的目标会出现在结果中。分页筛选应用于源资产，结果仍可包含它们关联的其他种类目标。已有资产在升级后重新盘点会补录关联。

## 云账号管理 API

项目管理员通过 `POST /v1/tenants/{tenant}/projects/{project}/cloud-accounts` 登记账号。正文提供 `id`、`provider` 和 `credential_ref`。程序返回账号元数据、`Location` 和 `ETag`。账号详情通过 `GET {Location}` 读取，凭据引用和厂商凭据不会出现在响应里。

`PATCH {Location}` 要求当前的强 `If-Match`。正文的 `action` 支持以下取值。

| 操作 | 行为 |
| --- | --- |
| `rotate` | 提供新 `credential_ref`。账号进入待校验，已停用账号保持停用。 |
| `suspend` | 停止下一次盘点领取，并拒绝旧代次提交。 |
| `resume` | 已停用账号重新进入待校验。 |
| `share` | 提供 `target_project` 并需要源、目标项目管理权，添加只读共享。 |
| `unshare` | 提供 `target_project`，由来源项目撤销共享。 |
| `transfer` | 提供同租户的 `target_project` 并需要双方管理权。转移归属时清除既有共享授权。 |

首次登记、轮换或恢复后，使用相应的 `verify-openstack` 或 `verify-aws` 命令验证绑定。验证成功后进入只读，已固定的远端标识须与厂商返回一致。账号 API 变更与脱敏审计记录同事务提交。缺少当前 ETag 或提交旧版本返回 412。

## 资源归属

资产列表返回 `project`、`ownership_revision` 和 `ownership_source`。默认归属继承云账号所属项目，其版本随账号版本变化。项目管理员可通过 `PATCH /v1/tenants/{tenant}/projects/{project}/assets/ownership` 将资源转给同租户的目标项目。使用属性 `account`、`region`、`kind`、`asset_id`、`target_project` 和当前的强 `If-Match`。来源是资源当前归属项目，调用方需同时具有双方管理权。

显式归属由资源独立管理。转移后，目标项目成员可通过资产列表、历史、导出和拓扑查看该资源。云账号共享继续适用于继承账号归属的资源，显式归属只向其所属项目开放。拓扑会独立校验来源与目标的可见性。转移和审计同事务提交，后续盘点保留归属。

### 标签归属规则

运维可在 `cmp_asset_tag_rules` 为指定云账号登记精确标签匹配。示例假设 `ops` 已获该账号的共享授权。

```sql
INSERT INTO cmp_asset_tag_rules (tenant_id, account_id, tag_key, tag_value, project_id)
VALUES ('tenant-id', 'account-id', 'team', 'ops', 'ops');
```

规则在盘点提交时应用。单一匹配项目必须已有该账号访问权。多项目冲突或目标项目失去账号授权时，资源在账号所属项目标记为 `pending`，可由管理员处理。确认无匹配时继承账号归属。厂商未返回标签时保留已有自动归属。手工转移后的资源保留显式归属。

AWS 使用受支持资源的标签。OpenStack 使用返回的 `key=value` 字符串标签。Nova 支持 2.26 微版本的站点可在账号配置中设置 `"nova_tags": true`。厂商标签仅用于和配置的规则匹配。资产 API 返回归属来源及版本。

## 项目操作中心

`GET /v1/tenants/{tenant}/projects/{project}/operations` 查询该项目收到的实例、网络、子网和安全组操作。具备项目查看权限的成员可查询历史，包括云账号后续停用后仍保留的操作。每次请求都会复核服务凭据和项目权限。

- `account`、`region`、`kind`、`action` 和 `status` 支持精确筛选，可组合使用。
- `status` 可取 `awaiting_approval`、`queued`、`applying`、`observing`、`succeeded`、`failed` 或 `needs_review`。
- 列表按创建时间和操作 ID 倒序返回，`limit` 默认为 50，范围为 1 至 100。用返回的 `next` 作为下一页的 `cursor`，保留相同项目和筛选条件。
- 每页读取当前状态。新受理的操作从列表首部获取，按状态翻页期间仍可能有任务变更状态。
- `GET /operations/{operation}` 返回详情，`GET /operations/{operation}/events` 按序返回状态事件。这两条路径仍需带上相同的租户和项目前缀。

CLI 从 `CLOUD_MANAGER_API_URL` 读取 API 基址，接受 HTTPS 或数字回环地址的 HTTP。通过环境变量 `CLOUD_MANAGER_API_TOKEN` 注入服务凭据。命令将响应的 `data` 输出为一行 JSON，可交给脚本处理。

```sh
export CLOUD_MANAGER_API_URL=http://127.0.0.1:8096
go run ./cmd/cloud-manager operations list tenant-id project-id --status needs_review --limit 20
go run ./cmd/cloud-manager operations get tenant-id project-id operation-id
go run ./cmd/cloud-manager operations events tenant-id project-id operation-id --after 0 --limit 50
```

列表命令也接受 `--account`、`--region`、`--kind`、`--action` 和 `--cursor`。事件命令用结果中的 `next_after` 续读。

### CLI 变更与审批

下列子命令沿用相同的 API 连接和 JSON 输出。所有命令的前两个位置参数均为租户和项目。

| 子命令 | 参数与结果 |
| --- | --- |
| `preview-delete` | 传入 `--account`、`--region`、`--asset-id`，查询保护状态、已知卷关联和资源版本 |
| `submit` | 传入 `--idempotency-key`、实例当前 `--if-match`，从标准输入读取实例动作 JSON |
| `create-instance` | 传入 `--idempotency-key`，从标准输入读取实例创建 JSON |
| `create-snapshot` | 传入 `--idempotency-key` 和源卷归属版本 `--if-match`，从标准输入读取快照 JSON |
| `create-image` | 传入 `--idempotency-key` 和源实例归属版本 `--if-match`，从标准输入读取账号、地域、实例 ID 和镜像名称 |
| `copy-image` | 传入 `--idempotency-key` 和源镜像归属版本 `--if-match`，从标准输入读取源与目标坐标和镜像名称 |
| `delete-image` | 传入 `--idempotency-key` 和镜像归属版本 `--if-match`，从标准输入读取账号、地域与镜像 ID |
| `delete-snapshot` | 传入 `--idempotency-key` 和源卷归属版本 `--if-match`，从标准输入读取快照和源卷标识 |
| `restore-snapshot` | 传入 `--idempotency-key` 和源卷归属版本 `--if-match`，从标准输入读取快照来源与新盘规格 |
| `rollback-snapshot` | 传入 `--idempotency-key` 和源卷归属版本 `--if-match`，从标准输入读取快照、原卷和覆盖确认 |
| `create-network` | 传入 `--idempotency-key`，从标准输入读取网络、子网或安全组 JSON。创建子网或 AWS、阿里云安全组还需父网络的 `--if-match` |
| `delete-network` | 传入 `--idempotency-key` 和资产归属版本 `--if-match`，从标准输入读取 VPC、子网或安全组删除 JSON |
| `approve` | 传入操作 ID 及 `--decision approve` 或 `--decision reject` |
| `resolve` | 传入操作 ID、`--decision succeeded` 或 `--decision failed`、`--evidence-ref`。若原操作记录了厂商请求 ID，还需相同的 `--provider-request-id` |
| `retry-observation` | 传入待复核的创建操作 ID 和 `--evidence-ref`，申请继续只读核实 |

```sh
go run ./cmd/cloud-manager operations preview-delete tenant-id project-id --account account-id --region us-east-1 --asset-id i-01234567
go run ./cmd/cloud-manager operations submit tenant-id project-id --idempotency-key change-2026-001 --if-match 2 < request.json
go run ./cmd/cloud-manager operations approve tenant-id project-id operation-id --decision approve
```

`--if-match` 接受数字版本，CLI 会将其转换为带引号的强版本头。创建与实例操作受理后返回操作 ID 和当前状态，进度通过 `get` 和 `events` 查询。标准输入最多 4096 字节。提交后连接中断时，使用原幂等键和原请求重试可取得同一收据。审批和处置使用独立审批人的服务凭据，对应接口位于 `/operations/{operation}` 下的 `/approval`、`/resolution` 和 `/retry-observation`。

### 批量实例操作

`POST /v1/tenants/{tenant}/projects/{project}/operation-batches/preview` 在同一项目快照中预览 1 至 25 项实例动作。支持启动、停止、重启、删除、挂卷和卸卷。每项提供 `account`、`region`、`asset_id`、`action` 和当前的 `ownership_revision`。卷动作沿用单项请求的卷版本、设备及卸载确认要求。一个实例或卷在同一批次中只能出现一次。

预览按输入顺序返回从零开始的 `index`、可读取的当前版本、审批要求及 `reason`。原因包括 `ready`、`stale`、`volume_stale`、`protected`、`busy`、`unavailable` 和 `unsupported_parameters`。只有每项就绪且 `remaining` 配额能容纳全批时，`can_submit` 为真。预览不会预留配额或创建操作。

向 `POST /v1/tenants/{tenant}/projects/{project}/operation-batches` 发送相同的 JSON，并附上单个 `Idempotency-Key`。服务重新核对当前授权、版本、保护、冲突和配额。整批接纳或整批回滚，HTTP 202 返回批次 ID、按输入顺序排列的操作及 `Location`。错误定位到具体输入时会附带 `details.index`。同一主体使用原键和原请求可查询同一批次，修改内容或顺序会得到 409。项目成员可通过 `GET {Location}` 查看批次及每项当前状态。

批次事务只承诺原子受理。厂商写入仍由各项操作独立执行。删除、卸载或命中项目规则的子操作仍需分别审批。

```sh
go run ./cmd/cloud-manager operations preview-batch tenant-id project-id < batch.json
go run ./cmd/cloud-manager operations submit-batch tenant-id project-id --idempotency-key batch-2026-001 < batch.json
go run ./cmd/cloud-manager operations get-batch tenant-id project-id batch-id
```

预览与批量提交的 JSON 上限为 32 KiB。

### 浏览器控制台

控制台位于 `/console`。可切换项目、筛选资源、查看操作与事件、预览和提交批次。独立审批人可在操作详情批准、拒绝或处置待复核项。项目角色由本地成员表决定。

运行 `migrate` 后为 API 进程设置以下配置以启用控制台。

| 环境变量 | 用途 |
| --- | --- |
| `CLOUD_MANAGER_CONSOLE_ORIGIN` | 浏览器访问的完整 HTTPS 源，例如 `https://cloud.example.com` |
| `CLOUD_MANAGER_CONSOLE_ISSUER` | 网关使用的 OIDC 发行者，与本地人类主体的 `issuer` 完全相同 |
| `CLOUD_MANAGER_CONSOLE_PROXY_DNS` | 网关客户端证书中允许的确切 DNS SAN |
| `CLOUD_MANAGER_CONSOLE_PROXY_CA` | 用于验证网关客户端证书的 PEM CA 文件 |
| `CLOUD_MANAGER_CONSOLE_CERT` 和 `CLOUD_MANAGER_CONSOLE_KEY` | API 服务端 TLS 证书和私钥文件 |

由 OIDC 网关执行登录流程，保护 `GET /console` 和 `POST /console/login`。网关验证用户身份后，从已验证的身份取得稳定 `sub`，覆盖上游登录请求的 `X-CMP-Subject`。网关须移除来自客户端的同名头，并使用配置的客户端证书通过 HTTPS 连接 API，同时验证 API 服务端证书。使用 Nginx 与 OAuth2 Proxy 时，可通过 `auth_request` 取得经过验证的用户标识。部署时需确认代理发出的标识确实对应 `sub`。浏览器访问路径保留原始 `Origin`。[OAuth2 Proxy 网关集成](https://oauth2-proxy.github.io/oauth2-proxy/7.11.x/configuration/integration/) · [Nginx 上游双向 TLS](https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_ssl_certificate)

登录使用租户、发行者与主体标识匹配已经登记的 `human` 主体。浏览器得到有效期一小时的 Secure、HttpOnly、SameSite=Strict Cookie，共用 PostgreSQL 的 API 副本可以读取或撤销该会话。写请求须同时提供同源 `Origin` 和会话 CSRF 头。项目权限随请求重新校验。退出撤销本地会话，网关和身份提供方的退出由各自管理。CLI 继续支持 Bearer 调用。可定期运行 `go run ./cmd/cloud-manager sessions-purge` 清理过期会话，每次最多清理 1000 条。

### 项目审计与导出

项目管理员可通过浏览器的「项目审计」、CLI 或 `GET /v1/tenants/{tenant}/projects/{project}/audit` 查询审计账本。记录涵盖云账号变更、资产归属、操作状态、审批、人工处置、创建续查和服务目录变更。归属转移或账号共享的双方项目各有一份记录。操作申请记载申请人，审批和处置记载实际审批人，系统状态事件留空主体字段。

可按 `category`、`account`、`subject_id` 和 RFC 3339 时间范围筛选。类别为 `account`、`ownership`、`operation`、`approval`、`resolution`、`observation`、`catalog`、`alert`、`console_access`。时间区间含 `from`，截止于 `until` 之前。列表按发生时间和审计 ID 倒序返回，`limit` 默认 50，范围为 1 至 200。续读使用 `next` 及原筛选条件。操作类记录的 `subject_id` 可直接查询项目操作详情，目录、告警和原生控制台记录分别使用模板 ID、告警 ID 与访问授权 ID。

`GET /v1/tenants/{tenant}/projects/{project}/audit/export` 使用同样的筛选条件，在一个数据库快照内导出最多 2000 条 CSV。超限返回 422，可用时间或类别缩小范围。输出字段为事件 ID、时间、类别、动作、对象、账号、地域、主体、关联项目、结果及适用的修订号。可能被电子表格识别为公式的文本单元格会转成文本。凭据、请求指纹和原始证据引用保留在各自的存储边界。

```sh
go run ./cmd/cloud-manager audit list tenant-id project-id --category approval --limit 50
go run ./cmd/cloud-manager audit export tenant-id project-id --from 2026-01-01T00:00:00Z --until 2026-02-01T00:00:00Z > project-audit.csv
```

迁移 `0025_project_audit` 回填已有记录。之后，新审计行与来源事件在同一事务提交。升级时根据历史数据量评估回填窗口，完成迁移后再恢复写入。

### 项目服务目录

项目管理员可以发布 AWS EC2 或 OpenStack 的实例模板。成员可在控制台「服务目录」查看启用中的模板，具备执行权限的操作者只需提交模板版本和实例名称。管理员可同时查看停用模板、查询历史版本和发布新版本。

以下路径以 `/v1/tenants/{tenant}/projects/{project}/service-catalog` 为前缀。

| 方法与路径 | 返回与权限 |
| --- | --- |
| `GET` 目录 | `200`。项目成员可读。按模板 ID 分页，`limit` 默认 50，最大 100。使用响应中的 `next` 作为下次 `after`。管理员可添加 `include_disabled=true` |
| `POST` 目录 | `201` 和 `Location`。管理员创建模板并发布首版 |
| `GET /{template}` | `200` 和修订号 `ETag`。项目成员读取当前启用版本，管理员也可读取停用模板 |
| `PUT /{template}` | `200` 和新 `ETag`。管理员带当前 `If-Match` 发布新版本，停用模板可借此恢复 |
| `DELETE /{template}` | `200` 和新 `ETag`。管理员带当前 `If-Match` 停用模板，保留已有版本、审计和操作 |
| `GET /{template}/versions/{version}` | `200`。管理员读取指定历史版本 |
| `POST /{template}/launch` | `202` 和操作 `Location`。操作者带 `Idempotency-Key` 提交 `{ "version": 1, "name": "worker-01" }` |

首版发布需要 `id`、`title`、`account`、`region`、`image_id`、`size`、`network_id` 和 `security_group_id`。发布后续版本使用相同规格字段，模板 ID 取自路径。发布与停用都递增修订号，只有发布递增版本号。过期修订号或对非当前启用版本的新申请返回 `412`。模板发布与新申请均重新校验账号写权限、规格和资源依赖。已受理操作保留原模板版本及实例参数，并继续遵守项目配额、独立审批、Worker 发送前校验及结果对账。权限仍有效时，使用原请求和幂等键重试可取回同一操作，即使模板已有新版本或已经停用。复用已用键提交不同名称或另一有权使用的已发布版本返回 `409`。

模板发布和停用写入 `catalog` 类项目审计，操作收据和详情提供 `catalog_template` 与 `catalog_version`。控制台在当前打开的页面内保留暂未确认的申请，可使用原键重试。CLI 使用 `CLOUD_MANAGER_API_URL` 和 `CLOUD_MANAGER_API_TOKEN`，可自行保存原请求和幂等键。

```sh
go run ./cmd/cloud-manager catalog create tenant-id project-id < template.json
go run ./cmd/cloud-manager catalog list tenant-id project-id --include-disabled
go run ./cmd/cloud-manager catalog get tenant-id project-id linux-small
go run ./cmd/cloud-manager catalog publish tenant-id project-id linux-small --if-match 1 < spec.json
go run ./cmd/cloud-manager catalog version tenant-id project-id linux-small 1
go run ./cmd/cloud-manager catalog launch tenant-id project-id linux-small --idempotency-key setup-001 < launch.json
go run ./cmd/cloud-manager catalog retire tenant-id project-id linux-small --if-match 2
```

### 项目告警

运行 `migrate` 后，以 `go run ./cmd/cloud-manager alert-controller` 启动告警评估器。进程使用 `CLOUD_MANAGER_DATABASE_URL` 读取本地盘点和操作状态。多个实例可并行领取不同项目。每个项目成功评估后，最早在 30 秒后再次评估。停止后重启会继续使用 PostgreSQL 中的评估计划和告警状态。

| 规则 | 触发条件 |
| --- | --- |
| `operation_review` | 项目操作进入 `needs_review` |
| `operation_delayed` | 当前 `queued` 状态持续至少 10 分钟，或 `awaiting_approval`、`applying`、`observing` 持续至少 30 分钟 |
| `scan_failures` | 同一启用的盘点计划连续失败或限流至少 3 次 |
| `scan_stale` | 距计划登记时间与最近一次完整盘点时间中的较晚者达到 `max(3 × 扫描间隔, 15 分钟)` |

盘点告警归当前云账号所属项目，只评估启用计划和可读账号。每条规则对同一对象至多保留一条未恢复告警。确认记录管理员及时间，条件解除时评估器自动恢复。恢复后同一问题再次出现时生成新的告警 ID。触发、确认和恢复均写入项目审计。操作超时使用当前状态的最后更新时间，首次受理状态使用创建时间。

项目成员可在控制台「项目告警」、CLI 或 `GET /v1/tenants/{tenant}/projects/{project}/alerts` 查看。列表默认返回未恢复告警，可用 `status` 和 `rule` 筛选。状态筛选支持 `active`、`open`、`acknowledged`、`resolved`、`all`。每页默认 50 条，最多 100 条。将响应的 `next` 用作下一页的 `after`。详情 `GET /alerts/{alert}` 返回当前 `ETag`。项目管理员以 `POST /alerts/{alert}/acknowledgement` 和当前 `If-Match` 确认告警，状态或修订号变更时返回 412。

```sh
go run ./cmd/cloud-manager alerts list tenant-id project-id --status active --rule scan_stale
go run ./cmd/cloud-manager alerts get tenant-id project-id ALERT_ID
go run ./cmd/cloud-manager alerts acknowledge tenant-id project-id ALERT_ID --if-match 1
```

在项目 Webhook 配置中将 `include_alerts` 设为 `true` 后重新执行 `event-register`，即可接收后续的 `project.alert.opened` 和 `project.alert.resolved`。通知使用与操作事件相同的签名、退避、失败查询和人工重投机制。接收端用通知 `id` 去重，用 `alert_id` 和 `revision` 关联告警状态。投递重试期间恢复通知可能先到达，接收端应保留同一告警的最新修订号。确认事件记录在审计中。将 `include_alerts` 改回 `false` 并重新登记可停止新增告警投递，已入队的记录继续处理。

部署时先执行 `0028_project_alerts`，再升级事件发布器。确认所有相关发布器已升级后启用告警订阅。回滚旧版本发布器前，先停用告警订阅并排空告警投递。升级前应按实际发件箱和操作表规模评估迁移锁窗口。

### OpenStack 原生控制台访问

项目操作者从控制台资源清单选择本项目拥有的 OpenStack 实例，填写故障排查、维护或应急处置用途，由独立 `approver` 角色审批。账号须启用写权限。待审批申请有效期为 15 分钟，批准后有 5 分钟开始连接。仅原申请人的同源浏览器会话可使用授权，连接建立最多一次，转发生命周期最长 5 分钟。

申请、审批、连接占用、失败、断开和撤销记录在 `console_access` 类审计。连接前再次检查实例归属、请求人与审批人的成员代次、账号凭据代次和写权限。转发期间每 2 秒重新检查浏览器会话与授权。申请人或项目管理员可撤销，服务会关闭当前转发连接。连接失败后可以重新申请，原授权保持已使用状态。待审批或批准后超时的记录显示 `expired`。

| 端点 | 契约 |
| --- | --- |
| `GET /console-grants` 和 `GET /console-grants/{id}` | 项目成员查询，列表用 `limit` 和 `after` 续读，详情返回 `ETag` |
| `POST /console-grants` | 浏览器会话和 CSRF，`If-Match` 取实例当前归属版本，`Idempotency-Key` 标识申请。正文含 `account`、`region`、`asset_id`、`purpose`，返回 `201` 与 `Location` |
| `POST /console-grants/{id}/approval` | 独立审批人携带授权的 `If-Match`，正文提供 `decision` 为 `approve` 或 `reject` |
| `POST /console-grants/{id}/revoke` | 申请人或管理员携带授权的 `If-Match`，空正文 |
| `GET /console-grants/{id}/stream` | 使用申请人 Cookie 和准确的 `Origin` 发起 HTTP/1.1 WebSocket 升级 |

端点均位于 `/v1/tenants/{tenant}/projects/{project}` 下。CLI 使用已授权的服务凭据查询、审批或撤销。提交的人类身份和交互式连接使用浏览器会话。

```sh
go run ./cmd/cloud-manager console-grants list tenant-id project-id
go run ./cmd/cloud-manager console-grants get tenant-id project-id GRANT_ID
go run ./cmd/cloud-manager console-grants approve tenant-id project-id GRANT_ID --if-match 1
go run ./cmd/cloud-manager console-grants revoke tenant-id project-id GRANT_ID --if-match 3
```

启用步骤依次为应用迁移 `0029_native_console`、配置 OIDC 浏览器入口、设置 `CLOUD_MANAGER_NATIVE_CONSOLE_PROFILES` 并升级网关。该环境变量指向私有 JSON 文件，键为凭据引用，值为具有实例读取和创建远程控制台权限的 OpenStack 配置文件路径。每个指定站点的配置增加 `console_proxy_url`，例如 `https://nova-console.example.com/websockify`。Nova 返回的 `vnc_lite.html` 或 `vnc_auto.html` 须与配置的代理位于同一 HTTPS 源和目录。配置以可信管理网端点为准，并确保 Nova 代理、网关和外部观测系统对上游查询参数脱敏。Nova 令牌用于服务端连接，浏览器只使用同源路径及已有会话。 [Nova 远程控制台 API](https://docs.openstack.org/api-ref/compute/#create-remote-console)

在同一个浏览器 HTTPS 源的 `/console-assets/novnc/` 部署经审查、固定版本的 noVNC 静态发行文件，包括 `vnc.html` 及其依赖目录。网关须转发控制台 WebSocket 的 `Upgrade`、`Connection` 与原始 `Origin`，使用 HTTP/1.1 连接 API，空闲超时覆盖授权连接的 5 分钟上限。产品控制台通过 `vnc.html` 的 `path` 设置指定同源流地址。 [noVNC 部署说明](https://novnc.com/noVNC/docs/EMBEDDING.html)

### AWS EC2 串口控制台访问

AWS 实例使用同一项目授权列表和独立审批。申请者提交本地生成的 OpenSSH RSA 公钥，服务校验 2048 至 4096 位、指数 65537 的公钥并保存 SHA-256 指纹。审批页面显示指纹。原申请人使用批准版本和同一把公钥发放，OpenSSH 公钥注释可任意变更。浏览器使用会话与 CSRF，CLI 和 API 也可使用具有项目执行权限的服务身份。所有访问仍要求实例属于该项目且云账号启用写权限。

先执行迁移 `0030_aws_serial_console` 并升级 API 副本，再向 `CLOUD_MANAGER_NATIVE_CONSOLE_PROFILES` 的凭据映射加入启用串口控制台的 AWS 配置。AWS 私有配置沿用 `credential_ref`、`region`、`role_arn` 和 `external_id`，另加 `serial_console_host`。该字段填厂商表中本地域的 SSH 主机名。站点须先启用 AWS 串口访问，为角色授权读取目标实例，并将 `ec2-instance-connect:SendSerialConsoleSSHPublicKey` 限定到允许的实例 ARN。连接前根据 AWS 公布的主机指纹验证 SSH 主机密钥。 [AWS 串口授权](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/configure-access-to-serial-console.html) · [地域 SSH 主机及指纹](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/connect-to-serial-console.html#serial-console-endpoints)

在资源清单选择 EC2 实例发起申请。批准后由原申请人重新输入同一把公钥，控制台显示 SSH 用户名、主机、端口和建连截止时间。API 使用 `POST /v1/tenants/{tenant}/projects/{project}/console-grants` 申请，在 OpenStack 请求字段基础上填写 `ssh_public_key`。领取使用 `POST /console-grants/{id}/serial-key`，提供授权当前 `If-Match` 及正文 `ssh_public_key`。成功响应的 `grant` 为 `key_sent`，`ssh` 包含 `host`、`username`、`port`、`valid_until` 和厂商请求 ID。

```sh
go run ./cmd/cloud-manager console-grants request-serial tenant-id project-id \
  --account aws-account --region us-east-1 --asset-id i-0123456789abcdef0 \
  --purpose troubleshoot --if-match 1 --idempotency-key serial-ticket-001 --public-key ~/.ssh/serial.pub
go run ./cmd/cloud-manager console-grants approve tenant-id project-id GRANT_ID --if-match 1
go run ./cmd/cloud-manager console-grants activate-serial tenant-id project-id GRANT_ID --if-match 2 --public-key ~/.ssh/serial.pub
ssh -o StrictHostKeyChecking=yes -o IdentitiesOnly=yes -i ~/.ssh/serial -p 22 \
  i-0123456789abcdef0.port0@serial-console.ec2-instance-connect.us-east-1.aws
```

发放前再次验证厂商账号与实例、申请人与审批人权限、凭据代次及资源归属版本。远端发放只提交一次，请求超时或未取得可信回执时记录 `dispatch_unknown`，授权保持已使用状态。AWS 公钥可用于 60 秒内建立连接，响应提供从发放请求开始计算的保守截止时间。撤权可阻止发放前的授权消费，AWS 已发放的密钥和已建立的 SSH 会话按厂商生命周期结束。运维可用退场流程和 CloudTrail 核对 AWS 操作。回退旧版 API 前应停用 AWS 串口申请和发放，等待当前发放结束或超时。 [发放 API 与 60 秒期限](https://docs.aws.amazon.com/ec2-instance-connect/latest/APIReference/API_SendSerialConsoleSSHPublicKey.html) · [既有 SSH 会话语义](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/connect-to-serial-console.html)

## 实例操作受理

`POST /v1/tenants/{tenant}/projects/{project}/instance-operations` 受理对已盘点实例的 `start`、`stop`、`reboot`、`delete`、`attach-volume` 和 `detach-volume`。正文包含 `account`、`region`、`asset_id`、`action`。请求要求实例当前 `ownership_revision` 对应的强 `If-Match`，以及调用方生成的 `Idempotency-Key`。

请求人须具备项目执行权限和账号写权限，实例须属于该项目并处于可操作的观测范围。成功返回 202 和 `Location`。通过 `GET {Location}` 查询项目操作账本。同主体、同项目、同键、同请求重放原操作。键已用于其他请求或实例已有活动操作时返回 409。项目活动操作上限默认为 100，可通过 `cmp_instance_operation_budgets` 配置。达到上限时返回 429。

操作和初始审计事件在同一事务写入。直接受理的操作在同一事务写入 Plumego SQL 队列，返回 `queued`。删除、卸载卷及命中项目审批规则的操作返回 `awaiting_approval`，批准后才入队。Worker 执行已入队操作并观测结果。

### 实例操作 Worker

`CLOUD_MANAGER_WORKER_PROFILES` 指向私有 JSON 文件，映射本地凭据引用到厂商配置文件。配置须与账号已校验的远端身份一致。使用独立的授权身份运行 `go run ./cmd/cloud-manager worker`。每个进程逐个领取队列任务，可通过多个进程并行。

Worker 发送前核对账号写权限、请求人的当前成员代次及实例归属版本，并提交 `applying`。启动、停止、重启和删除各发一次。厂商超时或领取到已进入 `applying` 的任务时，继续只读观测。启动或停止以目标状态确认为准。重启需要独立完成证据。Nova 支持操作详情和 2.51 微版本时，Worker 按厂商请求 ID 轮询完成事件。AWS 重启和缺失可信证据的 Nova 重启进入 `needs_review`。观测耗尽或原授权失效时进入待复核。旧租约无法提交结果。

`GET {Location}` 返回状态、最近转换时间、可用的厂商请求 ID 及结果原因。生产验证需在已授权的厂商沙箱验收。

#### 腾讯云、阿里云和华为云实例电源操作

三家均通过实例操作 API、CLI 和批量操作受理 `start`、`stop` 和 `reboot`。账号须具备本地写授权。Worker 的私有配置通过 `"allow_instance_power": true` 启用开关机，通过 `"allow_instance_reboot": true` 独立启用重启。云端身份须获得点查和所启用动作的权限。Worker 绑定已核验的账号、地域及项目。含审批的操作在点查后重新核对审批人代次及申请人的当前权限，再记录发送。

关机使用正常关机。腾讯云显式选择 `SOFT` 和 `KEEP_CHARGING`，阿里云显式选择 `ForceStop=false` 和 `KeepCharging`，华为云使用 `SOFT`。平台接收华为云任务 ID 作为厂商回执，统一根据目标实例的实时状态确认操作结果。 [腾讯云启动](https://cloud.tencent.com/document/api/213/15735) · [腾讯云关机](https://cloud.tencent.com/document/api/213/15743) · [阿里云关机](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-stopinstance) · [华为云关机](https://support.huaweicloud.com/api-ecs/ecs_02_0303.html)

开关机发送后断连时，Worker 用只读点查恢复。确认目标状态后，资产状态、操作结果和终态事件同事务提交，并使旧盘点领取失效。

重启采用软重启，每项任务只发送一次。腾讯云核对同一实例的最新操作名和操作请求 ID，华为云核对项目内的任务 ID 和任务中的目标实例。阿里云保留请求回执，由独立审批人提供与回执匹配的完成证据并处置。无法确认匹配回执或观测证据的重启进入 `needs_review`。 [腾讯云重启](https://cloud.tencent.com/document/api/213/15742) · [腾讯云实例详情](https://cloud.tencent.com/document/api/213/15728) · [阿里云重启](https://www.alibabacloud.com/help/zh/ecs/developer-reference/api-ecs-2014-05-26-rebootinstance) · [华为云重启](https://support.huaweicloud.com/api-ecs/ecs_02_0302.html) · [华为云任务详情](https://support.huaweicloud.com/api-ecs/ecs_02_0901.html)

TLS 协议夹具和真实 PostgreSQL 已覆盖发送前撤权、断连后只读恢复、旧盘点隔离、重启证据匹配和人工复核。真实账号沙箱验收保持待验证。

### 操作事件与 Webhook 投递

项目成员可通过 `GET {Location}/events?after=0&limit=50` 查询该操作的状态记录。响应包含 `events`、`next_after` 和 `has_more`。首次查询可省略 `after`，后续通读或轮询复用 `next_after`。序号用十进制字符串编码，页大小为 1 至 100。

为项目登记接收端后，`succeeded`、`failed` 和 `needs_review` 会与操作状态、审计和资产变更同事务写入发件箱。Webhook 正文为 UTF-8 JSON，包含 `schema_version`、`id`、`sequence`、`type`、`tenant`、`project`、`operation_id`、`action`、`status`、`occurred_at`，以及适用的 `asset_id`、`volume_id` 和 `reason`。新事件附带 `resource_kind`。事件类型示例包括 `instance.operation.succeeded`、`network.operation.succeeded`、`subnet.operation.succeeded` 和 `security-group.operation.succeeded`。接收端按 `sequence` 核对同一操作的事件顺序，按 `id` 去重。

将接收端列表写入权限为 `0600` 或更严格的私有 JSON 文件，并令 `CLOUD_MANAGER_EVENT_PROFILES` 指向它。每个接收端使用独立的随机签名密钥，长度至少 32 字节。URL 使用 HTTPS，认证走 HMAC。

```json
[
  {
    "id": "project-a-audit",
    "tenant": "tenant-id",
    "project": "project-a",
    "endpoint": "https://audit.example.com/events",
    "secret": "<从密钥管理注入至少 32 字节的独立随机密钥>"
  }
]
```

```sh
go run ./cmd/cloud-manager event-register project-a-audit
go run ./cmd/cloud-manager event-publisher
go run ./cmd/cloud-manager event-failures project-a-audit
go run ./cmd/cloud-manager event-retry project-a-audit EVENT_ID
go run ./cmd/cloud-manager event-disable project-a-audit
```

接收端 ID 持久绑定租户、项目和 URL。登记仅适用于之后产生的事件。暂停后停止新增投递和领取，已领取的事件仍可能发送并完成。以原配置登记可继续处理积压。更换目的地使用新 ID。轮换签名密钥时停止旧发布进程，更新接收端和发布配置后重启。

发布器使用至少一次投递。同一事件的 ID 和正文字节在重试时保持一致。接收端按 `X-Webhook-Timestamp` 与原始正文验证 `X-Webhook-Signature`，签名为 `v1=` 加 HMAC-SHA256 的十六进制编码，签名输入为 `timestamp.body`。接收端应限制时间戳偏差、校验签名正文中的项目，并在去重记录和业务效果持久提交后返回 2xx。`X-Webhook-Delivery` 等于正文 `id`。

连接异常、408、429 和 5xx 进入退避重试。发布器遵守最长 10 分钟的 `Retry-After`，每轮最多尝试 10 次。重定向和其他拒绝转入失败记录。`event-failures` 展示最近 100 条失败的原因及可用的 HTTP 状态。人工重投保留同一事件 ID。发布器按租约并发领取，过期租约无法确认投递。数据库保留事件及每个目的地的最新投递状态。

### 处置待复核操作

`POST {Location}/resolution` 由同项目的独立审批人调用。提交 `decision`（`succeeded` 或 `failed`）、`evidence_ref` 和可用的 `provider_request_id`。厂商请求 ID 已记录时，提交值必须一致。证据字段填写可供人工核查的引用，服务记录审批人的判断和引用。

处置仅接受 `needs_review` 状态。原申请人即使后来取得审批权限，也不能处置自己的操作。同一审批人的完全相同请求可重放。处置结果、证据和审计事件同事务保存。完成后释放实例活动操作限制和项目配额。

Nova 重启回执需同时匹配项目、实例、动作和请求 ID。只有带完成时间的 `compute_reboot_instance` 事件可确认成功或失败。观测只读，过期或证据不足时转人工复核。[Nova 操作详情协议](https://docs.openstack.org/api-ref/compute/#show-server-action-details)

### 发送前审批

运维可在 `cmp_instance_approval_rules` 中为项目指定需要审批的实例动作。示例在某项目要求停止实例前取得批准。

```sql
INSERT INTO cmp_instance_approval_rules (tenant_id, project_id, action)
VALUES ('tenant-id', 'project-id', 'stop');
```

命中规则的操作以 `awaiting_approval` 受理，保留实例互斥与项目配额。独立审批人通过 `POST {Location}/approval` 提交 `{"decision":"approve"}` 或 `{"decision":"reject"}`。批准重新校验申请人的权限以及账号、成员和资源归属版本，并在同一事务保存决定、审计和队列任务。拒绝结束操作并释放配额。同一审批人可重放相同决定。原申请人即使角色变成审批人，也不能批准自己的申请。

### 实例删除

项目操作者可调用 `GET /v1/tenants/{tenant}/projects/{project}/instance-operations/deletion-preview`，传入 `account`、`region`、`asset_id`。响应提供当前归属版本、本地删除保护、盘点已知卷关联数及审批要求。`provider_supported` 表示该厂商有保盘删除路径。凭据开关及实际可删条件由 Worker 在发送前核实。关联数来自最近盘点。

管理员可在数据库按租户、账号、区域和实例登记保护。提交、批准及 Worker 发送前都会检查保护。

```sql
INSERT INTO cmp_protected_instances (scope_id, external_id)
SELECT s.scope_id, o.external_id
FROM cmp_inventory_scopes AS s JOIN cmp_inventory_objects AS o USING (scope_id)
WHERE (s.tenant_id, s.account_id, s.region, s.kind, o.external_id) =
      ('tenant-id', 'account-id', 'region', 'instance', 'instance-id');
```

厂商配置默认禁用删除。已认证的沙箱凭据配置可设置 `"allow_instance_delete": true`，并且云端角色须具备该作用域内的读取和删除权限。删除始终要求独立审批。

- AWS 要求受验证的账号和实例、已识别的 EBS 根卷，以及所有返回的 EBS 映射明确标记保留。Worker 仅根据同账号、同区域、同 ID 的 `terminated` 状态自动确认完成。实例本地存储随实例终止而丢失。[EC2 终止实例](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/terminating-instances.html)
- OpenStack 要求 Nova 2.51 实例详情返回准确的项目、实例和完整挂载卷保留策略。Worker 按厂商请求 ID 关联 `compute_terminate_instance` 完成事件。站点无法提供匹配证据时，操作进入人工复核。实例本地磁盘随删除回收。[Nova 实例详情](https://docs.openstack.org/api-ref/compute/#show-server-details) · [Nova 操作详情](https://docs.openstack.org/api-ref/compute/#show-server-action-details)
- 阿里云仅释放已停止、按量付费且明确无操作锁的 ECS。Worker 校验完整的实例挂载清单、恰好一块系统盘和每块云盘的 `DeleteWithInstance=false`，然后以 `Force=false` 单次发送。厂商返回的请求 ID 供独立审批人核对完成证据。安全锁可能覆盖云盘保留策略。[阿里云 DeleteInstance](https://www.alibabacloud.com/help/zh/ecs/developer-reference/api-ecs-2014-05-26-deleteinstance) · [阿里云 DescribeDisks](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-describedisks)
- 华为云要求实例已关机、已在云端卸下云系统盘且明确未锁定。Worker 核对实例详情与挂载列表，确认余下云数据盘均标记保留，显式保留数据盘和弹性公网 IP。只有目标项目中、以原任务 ID 返回并匹配实例的删除成功任务可自动确认。[华为云卸载云硬盘](https://support.huaweicloud.com/intl/zh-cn/usermanual-evs/evs_01_0003.html) · [华为云 DeleteServers](https://support.huaweicloud.com/api-ecs/ecs_02_0103.html) · [华为云 ShowJob](https://support.huaweicloud.com/api-ecs/ecs_02_0901.html)
- 腾讯云销毁 CVM 时会释放系统盘。当前保盘删除入口返回 `provider_supported=false`，单项和批量受理均拒绝该动作。[腾讯云销毁实例](https://cloud.tencent.com/document/product/213/15723) · [腾讯云实例生命周期](https://cloud.tencent.com/document/product/213/4856)

厂商检查与删除是两个请求。应通过站点权限或变更窗口避免并发修改挂载与销毁策略。厂商断连、404 和本地盘点移除均不足以确认删除。Worker 核实厂商证据后，在同一事务写入操作结果和资产移除记录。后续扫描忽略该实例的迟到结果。人工处置记录账本结论，资产继续由盘点核实。

### 卷挂载和卸载

通过 `instance-operations` 提交 `attach-volume` 或 `detach-volume`，并提供 `volume_id` 和 `volume_revision`。`volume_revision` 取自卷当前的 `ownership_revision`。实例和卷必须在同一账号、区域和归属项目。每个动作同时占用实例与卷，直到确认完成、失败或经人工处置。挂载可按项目策略要求审批。卸载始终要求独立审批，申请人须在来宾系统完成文件系统卸载，再提交 `guest_unmounted: true`。此字段记录申请人的确认。

厂商凭据配置通过 `"allow_volume_changes": true` 开启卷写入。当前支持单挂载数据卷。AWS 挂载还须提供 `/dev/sdf` 至 `/dev/sdp` 之间的 `device`，并要求实例和卷位于同一可用区。Nova 使用 Compute 2.79 指定保留卷，设备由 Nova 分配。挂载完成及卸载发送前均检查 Nova 的保留策略。Worker 保护根卷和无法确认能安全卸载的卷，卸载请求使用厂商的常规模式。[AWS EBS 挂载](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_AttachVolume.html) · [AWS EBS 卸载](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DetachVolume.html) · [Nova 卷挂载](https://docs.openstack.org/api-ref/compute/#attach-a-volume-to-an-instance)

腾讯云、阿里云和华为云均要求实例和数据卷在同一可用区。Worker 读取云端卷和实例，再复核本地账号、资源及审批人代次。腾讯云使用 CBS 的非共享弹性数据盘，沿用经点查确认的卷保留设置。阿里云只挂载可独立卸载的非共享数据盘，显式提交卷保留和非启动属性。华为云使用项目内的 EVS 与 ECS 接口，挂载可提供 `/dev/sdf` 至 `/dev/sdp` 内的设备名，并从实例侧确认盘符及卷保留设置。卸载使用常规模式。 [腾讯云挂载](https://cloud.tencent.com/document/api/362/16313) · [腾讯云卸载](https://cloud.tencent.com/document/product/362/16316) · [阿里云挂载](https://www.alibabacloud.com/help/zh/ecs/developer-reference/api-ecs-2014-05-26-attachdisk) · [阿里云卸载](https://www.alibabacloud.com/help/zh/ecs/developer-reference/api-ecs-2014-05-26-detachdisk) · [华为云挂载](https://support.huaweicloud.com/api-ecs/ecs_02_0605.html) · [华为云卸载](https://support.huaweicloud.com/api-ecs/ecs_02_0606.html)

挂载和卸载各最多发送一次。恢复时 Worker 只读核对厂商报告的挂载关系。卸载成功要求卷仍存在且处于可用状态。双向关联、卷状态及操作审计在同一事务保存，写入完成时会废止两个作用域正在使用的盘点租约。观测失败的操作保留互斥，等待独立审批人经 `/resolution` 处置。

### 创建 EC2 实例

`POST /v1/tenants/{tenant}/projects/{project}/instances` 在项目持有且已启用写入的 AWS 账号中受理单实例创建。请求带 `Idempotency-Key`，正文中 `network_id` 填 AWS 子网 ID。

```json
{
  "account": "account-a",
  "region": "us-east-1",
  "name": "batch-worker",
  "image_id": "ami-0123456789abcdef0",
  "size": "t3.micro",
  "network_id": "subnet-0123456789abcdef0",
  "security_group_id": "sg-0123456789abcdef0"
}
```

AMI、子网和安全组须已在同项目、账号及区域盘点。子网和安全组须属于同一 VPC。API 按账号和区域检查可用规格，Worker 的 AWS 凭据配置使用 `"instance_create_sizes": ["t3.micro"]` 独立限定厂商写权限。空列表禁止创建。提交禁用公网 IPv4 自动分配，要求 IMDSv2，卷保留策略沿用 AMI 配置。通过 `cmp_instance_approval_rules` 为项目配置 `create` 可要求独立批准。

```sql
INSERT INTO cmp_instance_offerings (tenant_id, account_id, region, size)
VALUES ('tenant-id', 'account-a', 'us-east-1', 't3.micro');
```

操作、参数、厂商幂等令牌、配额预留和审计在同一事务保存。`202` 返回操作 `Location`，未核实厂商结果前 `asset_id` 为空。`max_managed_instances` 默认 100，独立于活动操作上限。已创建实例继续占用配额。确认失败或平台核实该实例删除时释放额度。

AWS 受理结果不明时，Worker 按令牌只读查询，匹配账号、区域、AMI、规格、子网和安全组后才接受结果。首次发送后三分钟内，空结果可触发一次同令牌、同参数和同子网的 AWS 幂等重放。其后继续只读观测。观察到 `running` 才原子提交实例、项目归属、操作终态和审计。超出观测预算进入 `needs_review`，额度继续保留。[EC2 `RunInstances` 幂等性](https://docs.aws.amazon.com/ec2/latest/devguide/ec2-api-idempotency.html#run-instances-idempotency)

### 创建 OpenStack 实例

同一个 `/instances` 入口接收 OpenStack 账号下的创建请求。`region` 填 Keystone 目录区域，`image_id`、`network_id` 和 `security_group_id` 分别填 Glance 镜像、Neutron 网络和安全组 UUID。`size` 填 Nova flavor ID。镜像、网络和安全组须已盘点到同一项目。对 OpenStack 账号同样需要登记 `cmp_instance_offerings`，并在 Worker 凭据文件中通过 `instance_create_sizes` 启用允许的 flavor。

Nova 创建需要 Compute 2.52，以一次请求写入专用关联标签。响应中的实例 UUID 会与操作一起保存。响应丢失时，Worker 在原 Keystone 项目和区域按标签查询，要求搜索结果唯一且完整。点查用支持原始 flavor ID 的 Compute 2.46，并核对项目、实例、标签和镜像。实例进入 `ACTIVE` 后，还须通过 Neutron 验证单一端口绑定了请求的网络和安全组。`ERROR`、作用域或属性不符以及观测耗尽均进入人工复核，配额仍保留。[Nova 创建 API](https://docs.openstack.org/api-ref/compute/#create-server) · [Neutron 端口 API](https://docs.openstack.org/api-ref/network/v2/#list-ports)

### 创建腾讯云 CVM 和阿里云 ECS 实例

沿用同一个 `/instances` 入口、`Idempotency-Key`、项目规格表 `cmp_instance_offerings` 和可选的项目独立审批规则。Worker 私有凭据配置须通过 `instance_create_sizes` 明确列出允许创建的机型，空列表关闭创建。镜像、子网或交换机、安全组均须已盘点到当前项目、账号和区域。

| 厂商 | 请求字段与创建行为 |
| --- | --- |
| 腾讯云 CVM | `network_id` 填子网 ID。Worker 点查子网所在 VPC 和可用区，再复核本地授权与适用的审批。创建单台按小时计费 CVM，不分配公网 IP，沿用自有镜像的登录设置。 [腾讯云 RunInstances](https://cloud.tencent.com/document/api/213/15730) · [腾讯云 DescribeSubnets](https://cloud.tencent.com/document/api/215/15784) |
| 阿里云 ECS | `network_id` 填交换机 ID。交换机和安全组须属于同一个 VPC。创建单台按量 ECS，公网带宽为零，禁用镜像预置密码，强制实例元数据使用 IMDSv2。 [阿里云 RunInstances](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-runinstances) |

两家都用持久化的 `ClientToken` 提交单次创建，并在同一请求写入专用关联标签。响应未知时，Worker 在原账号和区域按该标签只读查询。只有结果唯一，标签、名称、镜像、机型、子网和安全组均匹配且实例已运行，才原子确认资产和操作。厂商若返回实例 ID，观测结果还须匹配该 ID。关联标签仅供厂商对账，资产列表和事件均不展示它。证据冲突或观测耗尽转入人工复核，保留配额。[腾讯云 DescribeInstances](https://cloud.tencent.com/document/api/213/15728) · [阿里云 DescribeInstances](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-describeinstances)

### 创建华为云 ECS 实例

在同一个 `/instances` 入口传入当前项目已盘点的自有镜像、VPC 子网 ID 和安全组。名称由英文字母、数字、下划线和连字符组成，须以字母或数字开头。使用 `cmp_instance_offerings` 及 Worker 凭据的 `instance_create_sizes` 启用对应机型。凭据配置还须设置 `huawei_root_volume_type`，支持 `SAS`、`GPSSD`、`SSD` 和 `ESSD`。

Worker 点查子网所属项目、VPC、Neutron 网络和 IPv4 子网，再复核本地授权及适用的审批。在原 IAM 项目中创建单台按需实例，不申请公网 IP，沿用自有镜像登录设置。持久化的创建令牌派生为 UUID 格式的 `X-Client-Token`，并独立派生用于只读恢复的标签。[华为云 CreateServers](https://support.huaweicloud.com/api-ecs/ecs_02_0101.html) · [请求幂等性说明](https://support.huaweicloud.com/api-ecs/zh-cn_topic_0000001105258814.html) · [华为云 ShowSubnet](https://support.huaweicloud.com/api-vpc/vpc_subnet01_0002.html)

收到任务回执时，先核对原任务 ID、创建任务类型和目标实例。响应丢失时，在原 IAM 项目按关联标签查找唯一结果。两条路径均点查运行中的实例和标签，核对镜像、机型、计费方式和安全组，并核对实际网卡的 Neutron 网络及 IPv4 子网。Worker 按同一创建意图只发一次写请求。证据不符时保留配额并进入人工复核。[华为云 ShowJob](https://support.huaweicloud.com/api-ecs/ecs_02_0901.html) · [华为云 ListServersDetails](https://support.huaweicloud.com/api-ecs/zh-cn_topic_0094148850.html) · [华为云 ListServerInterfaces](https://support.huaweicloud.com/api-ecs/ecs_02_0505.html)

### 恢复创建观测

AWS、OpenStack、腾讯云、阿里云和华为云的独立审批人均可向 `{Location}/retry-observation` 提交 `{"evidence_ref":"case/123"}`，对原请求继续只读核实。续查使用同一云账号的当前凭据及审批授权，因此可以在凭据轮换或原申请人离开后继续。Worker 会再次检查该审批人和凭据的代次。相同审批人和证据引用可重放同一次决定。确认没有创建实例后可经 `/resolution` 记录失败并释放额度。

### 创建网络和 IPv4 子网

`POST /v1/tenants/{tenant}/projects/{project}/network-operations` 接受严格 JSON 正文与单个 `Idempotency-Key`，返回 202 和操作 `Location`。`account`、`region`、`kind`、`name` 必填。网络使用 `kind: network`，子网使用 `kind: subnet`。

| 厂商 | 创建网络 | 创建子网 |
| --- | --- | --- |
| AWS EC2 | 提供规范的 IPv4 `cidr`，前缀长度为 16 至 28 | 提供 `parent_id` 与同一 VPC CIDR 内的 IPv4 `cidr`，前缀长度为 16 至 28。EC2 分配可用区 |
| OpenStack Neutron | 创建项目私有网络 | 提供项目私有网络的 `parent_id` 和规范的 IPv4 `cidr` |
| 腾讯云 VPC | 提供 `10.0.0.0/12`、`172.16.0.0/12` 或 `192.168.0.0/16` 内的 IPv4 `cidr`，前缀最长 28 位 | 提供 `parent_id`、`zone` 及 16 至 28 位前缀的 `cidr` |
| 阿里云 VPC | 提供 RFC 1918 私有 IPv4 `cidr`，前缀长度为 16 至 28 | 提供 `parent_id`、`zone` 及 16 至 29 位前缀的 `cidr` 创建交换机 |
| 华为云 VPC | 提供 RFC 1918 私有 IPv4 `cidr`，前缀最长 24 位 | 提供 `parent_id` 和最长 29 位前缀的 `cidr`，可指定 `zone`。网关取网段第一个可用地址，启用 DHCP |

创建子网时，`If-Match` 必须为父网络当前的 `ownership_revision`，格式如 `"2"`。父网络须已盘点并属于同一项目、账号和区域。提交时对同一父网络下已登记的创建意图检查地址段重叠，失败意图释放预留，待复核意图保留预留。厂商也会校验实际网络中的地址约束。

账号须允许写入，Worker 的厂商配置也须开启 `"allow_network_creation": true`。可在 `cmp_instance_approval_rules` 为项目配置 `create-network`、`create-subnet`，命中时由独立审批人通过 `{Location}/approval` 批准。AWS IAM 需允许相应的创建、创建时打标和只读查询。Neutron 站点须支持 `standard-attr-description` 扩展。[EC2 CreateVpc](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateVpc.html) · [EC2 CreateSubnet](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateSubnet.html) · [Neutron 网络 API](https://docs.openstack.org/api-ref/network/v2/#networks)

Worker 最多发送一次写请求。AWS 在创建时写入专用标签，Neutron 在同一请求中写入关联描述。Worker 按厂商返回的 ID 或按关联标记查找唯一结果，核对厂商所有者、名称以及对应的地址段和父网络，再提交资产、项目归属、拓扑关系和终态事件。子网观测期间父网络归属发生变化时，操作进入待复核。通过 `GET {Location}` 与 `GET {Location}/events` 查询。独立审批人可用 `{Location}/retry-observation` 恢复只读核实。

腾讯云、阿里云和华为云的 Worker 配置同样使用 `allow_network_creation`，默认关闭。项目要求审批时，发送前会再次检查审批人的成员代次。腾讯云和阿里云在创建时写入专用标签，阿里云还携带持久化的 `ClientToken`。华为云在创建请求中写入专用描述，在原 IAM 项目按描述查询。三家都要核对归属、名称、CIDR 及关联证据，且查询结果唯一，才确认创建成功。响应丢失后继续只读对账。三家的网络写入已通过本机夹具，真实厂商沙箱仍待验收。[腾讯云 CreateVpc](https://cloud.tencent.com/document/product/215/15774) · [阿里云 CreateVpc](https://www.alibabacloud.com/help/en/vpc/developer-reference/api-vpc-2016-04-28-createvpc) · [华为云 CreateVpc](https://support.huaweicloud.com/api-vpc/vpc_api01_0001.html)

应用 `0033_subnet_zone` 后，新的子网请求将 `zone` 连同父网络、归属版本和 CIDR 写入意图，审批详情可读取这些字段。旧意图保留原参数。创建前，Worker 先点查父 VPC，确认请求 CIDR 完整落在主网段或附加网段内，再复核本地权限和版本。确认成功时记录父 VPC 及明确指定的可用区。腾讯云返回数字可用区 ID 时，Worker 以同地域可用区目录核对。华为云还核对 IPv4 网关及 DHCP。[腾讯云 CreateSubnet](https://cloud.tencent.com/document/api/215/15782) · [阿里云 CreateVSwitch](https://www.alibabacloud.com/help/en/vpc/developer-reference/api-vpc-2016-04-28-createvswitch) · [华为云 CreateSubnet](https://support.huaweicloud.com/api-vpc/vpc_subnet01_0001.html)

### 删除 VPC 和子网

腾讯云、阿里云和华为云可从项目资产列表申请删除单个 VPC 或子网。先执行 `cloud-manager migrate` 应用 `0052_network_deletion`，再升级 API 与 Worker。账号须允许写入，Worker 凭据按需开启 `allow_vpc_deletion` 或 `allow_subnet_deletion`，两项默认关闭。

向 `POST /v1/tenants/{tenant}/projects/{project}/network-operations/delete` 提交 `account`、`region`、`asset_id` 和 `kind`。VPC 使用 `kind=network`，子网使用 `kind=subnet`。提供单个 `Idempotency-Key`，`If-Match` 使用目标资产当前归属版本。返回 202 和操作 `Location` 后，由另一位审批人通过该地址下的 `/approval` 决定。子网的父 VPC 由盘点拓扑确定，父 ID 和归属版本随意图保存，并出现在审批详情中。CLI 使用 `cloud-manager operations delete-network TENANT PROJECT --idempotency-key KEY --if-match REVISION < request.json`。

平台校验资产依赖和活动操作。仍在盘点中的引用以及使用子网的启用目录模板均会阻止删除。删除申请活动期间，目标资源归属转移、新建受影响的实例或子网络会被拦截。Worker 发送前再次核对本地授权、审批及依赖。删除 VPC 还需厂商确认其子网列表完整且为空。腾讯云拒绝默认网络或默认子网，阿里云还拒绝共享交换机并显式关闭强制删除。华为云使用原 IAM 项目中的单资源删除路径，并核对子网所属 VPC。平台预检与厂商写入间存在并发窗口，因此厂商的在用资源约束仍是最后一道保护。[腾讯云 DeleteVpc](https://cloud.tencent.com/document/product/215/15775) · [腾讯云 DeleteSubnet](https://cloud.tencent.com/document/api/215/15783) · [阿里云 DeleteVpc](https://www.alibabacloud.com/help/en/vpc/developer-reference/api-vpc-2016-04-28-deletevpc) · [阿里云 DeleteVSwitch](https://www.alibabacloud.com/help/en/vpc/developer-reference/api-vpc-2016-04-28-deletevswitch) · [华为云删除 VPC](https://support.huaweicloud.com/intl/zh-cn/api-vpc/vpc_api01_0005.html) · [华为云删除子网](https://support.huaweicloud.com/intl/zh-cn/api-vpc/vpc_subnet01_0005.html)

每条申请至多发送一次厂商删除请求。原账号和地域内两次连续、完整的精确查询均返回空列表后，平台原子写入资产删除标记、操作终态及通知事件。确认子网删除后，可以重新申请原 CIDR。404、畸形响应和不完整分页无法作为缺失证据。证据不足时置为 `needs_review`，独立审批人可通过 `/retry-observation` 恢复只读核实。厂商真实沙箱验收仍待完成。

### 创建安全组

在同一个 `network-operations` 入口提交 `kind: security-group`，提供 `account`、`region`、`name` 和 `Idempotency-Key`。AWS 和阿里云需要已盘点到同一项目的 VPC `parent_id`，以及对应的强 `If-Match`。腾讯云按地域创建，华为云按 IAM 项目创建，Neutron 按 Keystone 项目创建。这三类请求省略父网络字段与版本头。项目可通过 `create-security-group` 审批规则要求独立审批。操作详情记录名称、父网络及归属版本，供审批人复核。

Worker 凭据配置使用独立的 `"allow_security_group_creation": true` 启用。AWS 复核 VPC 所属账号，使用创建时标签和安全组 ID 对账。Neutron 使用 `standard-attr-description` 扩展写入关联描述，并核对安全组项目、共享状态和返回的规则列表。两家厂商均要求观测确认无入站规则后才记成功，出站规则由厂商默认策略决定。若站点模板为新安全组加入了入站规则，操作进入待复核。资产和终态事件一并提交，AWS 安全组同时记录所属 VPC 拓扑。[EC2 安全组创建](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateSecurityGroup.html) · [Neutron 安全组](https://docs.openstack.org/api-ref/network/v2/#security-groups) · [Neutron 默认规则](https://docs.openstack.org/neutron/2026.1/admin/intro-os-networking.html)

| 厂商 | 创建与只读确认 |
| --- | --- |
| 腾讯云 | 创建时写入关联标签。确认安全组唯一匹配、且非默认组，并要求入站、出站规则列表均为空。[CreateSecurityGroup](https://cloud.tencent.com/document/product/215/15806) · [DescribeSecurityGroupPolicies](https://cloud.tencent.com/document/api/215/15804) |
| 阿里云 | 先确认父 VPC 属于账号且可用，再使用持久化 `ClientToken` 和关联标签创建高级安全组。确认类型为 `enterprise`、组内策略为 `Drop` 且无显式入站规则。成功后记录父 VPC 拓扑。[CreateSecurityGroup](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-createsecuritygroup) · [DescribeSecurityGroupAttribute](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-describesecuritygroupattribute) |
| 华为云 | 在 IAM 项目调用 V3 API，创建时写入关联描述。读回安全组详情，验明项目以及所有规则归属，入站仅接受厂商同组互通规则，出站沿用厂商默认策略。[CreateSecurityGroup](https://support.huaweicloud.com/api-vpc/vpc_apiv3_0010.html) · [ShowSecurityGroup](https://support.huaweicloud.com/api-vpc/vpc_apiv3_0012.html) |

三家公有云各自最多收到一次创建写请求。响应丢失后，Worker 按关联标记查询唯一候选。厂商回执包含资源 ID 时，Worker 按原 ID 核对。规则超出上述范围或证据冲突时，操作进入 `needs_review`。独立审批人可通过 `retry-observation` 恢复只读对账。

### 删除安全组

腾讯云、阿里云和华为云可申请删除未被外部资源使用的安全组。先执行 `cloud-manager migrate` 应用 `0053_security_group_deletion` 至 `0055_network_deletion_backfill`，升级 API 与 Worker 后在目标账号的 Worker 凭据中启用 `allow_security_group_deletion`。该开关默认关闭。迁移会回填尚在进行的 VPC 与子网删除预留。

沿用 `POST /v1/tenants/{tenant}/projects/{project}/network-operations/delete`，提交 `account`、`region`、`asset_id`、`kind=security-group` 和 `confirm_rule_removal=true`。提交目标资产归属版本 `If-Match` 和 `Idempotency-Key`。控制台要求勾选同时删除组内规则，CLI 使用同一个 `operations delete-network` 命令。由独立审批人批准。阿里云的审批详情同时显示父 VPC 与归属版本。

使用安全组的已盘点资产、待执行的实例或规则操作、启用的目录模板会阻止删除。申请活动期间拦截该组的规则创建、撤销、归属转移，以及依赖该组的实例申请。云端预检也会检查绑定和其他安全组的引用。腾讯云核对组属性和关联计数。阿里云限定已盘点到同一项目 VPC 的非托管高级安全组，并检查 ECS、弹性网卡与其他组的引用。华为云拒绝默认组，核对原 IAM 项目的端口与规则引用，仅允许目标组自己的自引用随组清理。平台发送单资源删除，厂商继续执行自身的在用、引用和托管保护。[腾讯云删除安全组](https://cloud.tencent.com/document/api/215/15803) · [阿里云删除安全组](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-deletesecuritygroup) · [华为云删除安全组](https://support.huaweicloud.com/api-vpc/vpc_apiv3_0014.html)

厂商回执丢失时继续只读核实。连续两轮完整的精确查询均确认安全组缺失后，平台同事务更新该组、组内规则、操作终态和通知事件。迟到的规则盘点保留其他组的结果，并跳过父组已确认删除的规则。真实厂商沙箱验收仍待完成。

### 创建安全组规则

在 `network-operations` 入口提交 `kind: security-group-rule`、`account`、`region`、父安全组 `parent_id` 和 `rule`。`rule` 包含 `direction`（`ingress` 或 `egress`）、`protocol`（`tcp` 或 `udp`）、`from_port`、`to_port` 及 `remote_cidr`。端口范围为 1 至 65535，远端须为规范的 IPv4 CIDR。携带父安全组当前 `ownership_revision` 对应的 `If-Match` 和 `Idempotency-Key`。父安全组须属于同一项目、云账号和区域。相同键重放返回原操作。另一键提交同定义的有效创建意图返回 409，规则经确认撤销后可用新键重新申请。

```json
{
  "account": "aws-account",
  "region": "us-east-1",
  "kind": "security-group-rule",
  "parent_id": "sg-0123456789abcdef0",
  "rule": {"direction": "ingress", "protocol": "tcp", "from_port": 443, "to_port": 443, "remote_cidr": "192.0.2.0/24"}
}
```

CLI 使用 `cloud-manager operations create-network TENANT PROJECT --idempotency-key KEY --if-match REVISION < rule.json` 受理。操作详情的 `network_intent` 保留父安全组、归属版本和准确规则参数，控制台也会在批准前显示这些内容。规则始终需要独立审批。发送前会检查申请人的授权、父安全组归属及审批人的成员代次。默认安全组和 Neutron 共享安全组会被厂商适配器拒绝。

应用 `0031_security_group_rules` 迁移并升级 Worker 和 API 后，在确认可操作的云账号 Worker 凭据中设置 `"allow_security_group_rules": true`。AWS 需要读取组与规则、对应方向的授权操作权限和创建时打标权限。Neutron 需要读取组与规则、创建规则的权限以及 `standard-attr-description` 扩展。Worker 最多发出一次写请求，按远端 ID 或原关联标记查找唯一规则，核对归属、父组和完整参数后提交资产、拓扑和 `security-group-rule.operation.succeeded`。结果未知可由独立审批人恢复只读观测。已确认创建的规则和只读发现的已有规则使用同一父组归属。真实厂商站点和规则撤销分别验收。 [EC2 入站授权](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_AuthorizeSecurityGroupIngress.html) · [EC2 出站授权](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_AuthorizeSecurityGroupEgress.html) · [Neutron 安全组规则](https://docs.openstack.org/api-ref/network/v2/#security-group-rules)

腾讯云、阿里云和华为云共用这个默认关闭的创建开关。升级时先运行 `cloud-manager migrate`，应用 `0034_security_rule_outbox`。该迁移使安全组规则终态与项目告警能写入同一发件箱，并保留已有记录。就绪探针会检查迁移版本。三家创建的都是显式允许规则，使用 IPv4 CIDR、TCP 或 UDP 和连续目的端口。Worker 在写请求中记录派生的关联描述，审批版本变化时拒绝发送。匹配结果须与申请的方向、协议、端口、CIDR 和允许动作相符，且只能有一条。证据冲突进入 `needs_review`。

| 厂商 | 写入与对账 |
| --- | --- |
| 腾讯云 | 点查非默认父组及策略版本，携带版本在指定方向的末尾追加规则。读取组策略完备核对关联描述，使用与盘点相同的规则 ID。[CreateSecurityGroupPolicies](https://cloud.tencent.com/document/product/215/15807) · [DescribeSecurityGroupPolicies](https://cloud.tencent.com/document/api/215/15804) |
| 阿里云 | 限定在 VPC 高级安全组，显式设置优先级 100 并携带持久 `ClientToken`。厂商对重复规则可能返回成功，Worker 仍须分页找到带本次关联描述的规则才确认。[入站规则](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-authorizesecuritygroup) · [出站规则](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-authorizesecuritygroupegress) |
| 华为云 | 在原 IAM 项目的非默认父组创建启用的 V3 规则，显式设置优先级 100。有规则 ID 时点查规则，响应丢失时读取同一父组的规则集合。[CreateSecurityGroupRule](https://support.huaweicloud.com/api-vpc/vpc_apiv3_0016.html) · [ShowSecurityGroup](https://support.huaweicloud.com/api-vpc/vpc_apiv3_0012.html) |

### 撤销安全组规则

可从安全组规则资产行申请，也可向 `POST /v1/tenants/{tenant}/projects/{project}/network-operations/revoke-rule` 提交。正文包含 `account`、`region`、`parent_id` 和 `rule_id`。`If-Match` 使用父安全组当前归属版本，`Idempotency-Key` 用于重放同一申请。受理返回 202 和操作地址。申请保留当时的规则内容，审批人可在操作者独立的会话中查看父组、规则 ID 和内容。父组、规则或审批权限在发送前发生变化时，调用被拒绝。

```sh
cloud-manager operations revoke-security-rule TENANT PROJECT --idempotency-key KEY --if-match REVISION < revoke-rule.json
cloud-manager operations get TENANT PROJECT OPERATION_ID
cloud-manager operations approve TENANT PROJECT OPERATION_ID --decision approve
```

执行 `0032_security_rule_revocation` 迁移并升级 Worker 与 API，再在可操作账户的 Worker 凭据配置中启用 `"allow_security_rule_revocation": true`。AWS 工作角色需要读取父组和规则，并允许相应方向的 `RevokeSecurityGroupIngress` 或 `RevokeSecurityGroupEgress`。Neutron 需要读取父组和规则以及按 ID 删除规则。系统禁止撤销默认安全组中的规则和 Neutron 共享组中的规则。 [EC2 入站撤销](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_RevokeSecurityGroupIngress.html) · [EC2 出站撤销](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_RevokeSecurityGroupEgress.html) · [Neutron 删除规则](https://docs.openstack.org/api-ref/network/v2/#delete-security-group-rule)

腾讯云、阿里云和华为云使用同一独立撤销开关，默认关闭。Worker 根据云端预检选定撤销目标，在发送前复核本地父组版本、规则内容与审批人权限。

| 厂商 | 撤销与确认 |
| --- | --- |
| 腾讯云 | 核对非默认父组、规则内容与唯一索引，使用同一次读取返回的策略版本撤销单方向单条规则。重复指纹或不完整策略会阻止自动确认。[DeleteSecurityGroupPolicies](https://cloud.tencent.com/document/product/215/15809) |
| 阿里云 | 在 VPC 高级安全组内按厂商规则 ID 调用对应方向的撤销接口。读取同一 VPC、同一父组的全部规则页后确认目标缺失。[撤销入站](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-revokesecuritygroup) · [撤销出站](https://www.alibabacloud.com/help/en/ecs/developer-reference/api-ecs-2014-05-26-revokesecuritygroupegress) |
| 华为云 | 核对同一 IAM 项目的非默认父组及目标规则，按 V3 规则 ID 删除。父组列表缺失且目标点查返回 404 才形成一轮缺失证据。[DeleteSecurityGroupRule](https://support.huaweicloud.com/api-vpc/vpc_apiv3_0019.html) |

Worker 对已持久记录的撤销最多发送一次。连续两轮只读观测都确认缺失后，才一并提交资产移除和终态事件。规则仍可见时继续等待，读取范围冲突或证据耗尽时进入待复核。审批人可以恢复只读观测。已确认撤销的本地记录会阻止迟到盘点复活该规则 ID。对在平台外独立删除的规则，普通盘点仍保留待确认缺失状态。
