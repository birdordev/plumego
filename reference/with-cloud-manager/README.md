# 多云资源管理

本目录是独立的 PostgreSQL 产品模块。目前提供项目与云账号授权、可恢复的多云盘点、具备审批和对账的资源操作，以及项目操作查询。

自有 KVM 的宿主机登记、容量上报与预留见[站点与宿主机放置](#自有-kvm-站点与宿主机放置)。

## PostgreSQL 本地验证

首次检出和更新控制台源码后，用 Node.js 22.12 或更新版本在仓库根目录运行 `make cloud-manager-console`，生成 Go 编译所需的控制台嵌入资源。仓库的 `make reference-vet` 和 `make reference-test` 会执行这一步。

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

## VMware vSphere 只读盘点

通过项目云账号 API 登记 `vmware-vsphere`。使用 vCenter 只读服务主体，在 `CLOUD_MANAGER_VSPHERE_PROFILE` 指定的私有 JSON 文件写入 `credential_ref`、`endpoint`、`username`、`password`、`vcenter_uuid` 和 `datacenters`。`endpoint` 是受部署方管理的 HTTPS 源，`datacenters` 是允许读取的数据中心托管对象 ID 数组。私有 CA 可通过 `ca_pem` 提供。凭据文件权限为 `0600` 或更严格。

适配器先验证 TLS 证书和 vCenter UUID，再登录。每次连接复核配置中的所有数据中心可见。会话及容器视图随任务退出关闭，服务端会话 Cookie 与分页令牌只存在于当前任务内存。[vSphere ServiceInstance](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.ServiceInstance.html) · [ContainerView](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.view.ContainerView.html)

```bash
cloud-manager verify-vsphere tenant-id vcenter-account
cloud-manager scan-vsphere tenant-id vcenter-account - region
cloud-manager scan-vsphere tenant-id vcenter-account datacenter-7 instance
cloud-manager scan-vsphere tenant-id vcenter-account datacenter-7 image
cloud-manager scan-vsphere tenant-id vcenter-account datacenter-7 network
```

数据中心映射为地域，虚拟机映射为实例，VM 模板映射为镜像。VM 和模板以 `config.instanceUuid` 标识，网络使用已核验数据中心内的托管对象 ID。虚拟机关联的标准网络、分布式端口组及不透明网络可通过现有项目拓扑查询。平台控制台支持按类型和账号查看这些资产。定时控制器可使用同一凭据引用和数据中心范围。[VirtualMachine 属性](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vim.VirtualMachine.html)

适配器先完整消费 PropertyCollector 分页，再将排序后的本地盘点数据分批提交。跨进程使用内容指纹和偏移续跑，结果发生变化时使旧代次重启。权限收缩后未看到的资源保留为缺失候选。项目仍按原有账号和资产归属授权；实例写入由账号授权层拒绝。单次采集上限为 100,000 个远端对象，另有响应体与内存预算，超限使采集失败。[PropertyCollector](https://developer.broadcom.com/xapis/vsphere-web-services-api/latest/vmodl.query.PropertyCollector.html)

## Redfish 裸金属只读盘点

执行 `cloud-manager migrate` 应用 `0117_redfish_schedules`。通过项目云账号 API 登记 `redfish`，用 `CLOUD_MANAGER_REDFISH_PROFILE` 指向私有 JSON 文件。站点运维在受控管理网配置 BMC 的 HTTPS 地址、专用只读账号、已知服务 UUID 和站点标识。凭据文件使用 `0600` 或更严格的权限，私有证书链通过 `ca_pem` 配置。

```json
{
  "credential_ref": "redfish/site-a/reader",
  "endpoint": "https://bmc-site-a.example.net",
  "service_uuid": "6dfa9bd0-98bf-49a1-ad93-a98567937e16",
  "site": "site-a",
  "username": "inventory-reader",
  "password": "<BMC 只读账号密码>"
}
```

```bash
cloud-manager verify-redfish tenant-id bmc-account
cloud-manager scan-redfish tenant-id bmc-account - region
cloud-manager scan-redfish tenant-id bmc-account site-a bare-metal
```

先用 TLS 读取服务根并核对 UUID，再发送 HTTP Basic 凭据给同源资源。校验账号时还核对是否能完整读取 Systems 集合。已校验的账号同时绑定服务 UUID 与站点，变更其中任一字段会被拒绝。适配器只跟随同源的 Redfish 路径，拒绝重定向。资产只纳入 `SystemType=Physical`，以系统 UUID 标识，显示电源状态，使用资产的地域字段记录站点。[DMTF Redfish 协议](https://www.dmtf.org/sites/default/files/standards/documents/DSP0266_1.21.0.pdf)

单个 BMC 每轮至多接受 64 页、1,000 个 Systems 集合成员。所有页和成员都通过检查后，才向资产盘点提交一批结果。响应失败、重复成员或集合计数不一致时保留原资产。可在持久控制器配置同一凭据引用，并为账号及站点安排 `bare-metal` 类型的扫描。项目资产 API 和控制台可按裸金属类型筛选。真实设备兼容性与管理网验收仍待完成。

### 裸金属电源操作

先应用迁移 `0118_bare_metal_power` 并更新 API 和 Worker。管理员使用账号当前 ETag 对 `/cloud-accounts/{account}` 提交 `{"action":"enable-bare-metal-power"}`，再对该站点执行完整盘点。账号初次校验、凭据轮换和恢复后进入待校验或只读状态，需重新启用。可用 `disable-bare-metal-power` 关闭派发。两种操作均递增账号代次并记录审计。

将同一凭据引用配置到 `CLOUD_MANAGER_WORKER_PROFILES`，指向启用了 `"allow_power":true` 的私有配置。Worker 配置使用 BMC 上具备相应电源权限的专用主体，盘点配置可继续使用只读主体。控制台资产行或 `POST /v1/tenants/{tenant}/projects/{project}/bare-metal/power` 受理申请。使用资产归属版本作为 `If-Match`，并提供 `Idempotency-Key`。

```bash
cloud-manager operations bare-metal-power TENANT PROJECT --idempotency-key KEY --if-match REVISION < power.json
```

```json
{"account":"bmc-account","region":"site-a","asset_id":"11112222-3333-4444-8666-777788889999","action":"power-on-bare-metal"}
```

动作包括 `power-on-bare-metal` 开机和 `shutdown-bare-metal` 优雅关机。须由独立审批人批准。队列派发前复核账号和资产代次、审批与工单，并检查同一物理系统 UUID、动作地址与允许的 ResetType。已经处于目标电源状态时只读确认。相同物理 UUID 同时只保留一项活动电源操作。

每个申请至多提交一次 BMC 写入。连接中断或异步受理后继续只读核对，确认同一系统达到目标状态时原子更新资产、操作状态和发件箱。观测耗尽则保留待复核状态。审批人可用现有 `/operations/{id}/retry-observation` 带证据引用恢复只读对账。真实 BMC 执行和访客关机仍待现场验收。[DMTF ComputerSystem Reset](https://redfish.dmtf.org/schemas/v1/DSP2046_2025.2.html)

### 裸金属一次性 PXE 启动配置

应用 `0119_bare_metal_pxe` 并更新 API 和 Worker。沿用裸金属账号写入开关，在 Worker 的私有 BMC 配置中设置 `"allow_pxe":true`。控制台可从关机资产提交申请，API 为 `POST /v1/tenants/{tenant}/projects/{project}/bare-metal/pxe`。提交使用资产归属版本、幂等键和独立审批。

```bash
cloud-manager operations bare-metal-pxe TENANT PROJECT --idempotency-key KEY --if-match REVISION < pxe.json
```

```json
{"account":"bmc-account","region":"site-a","asset_id":"11112222-3333-4444-8666-777788889999","action":"stage-bare-metal-pxe"}
```

`stage-bare-metal-pxe` 设置下一次启动为 PXE，`cancel-bare-metal-pxe` 取消当前的一次性 PXE。Worker 仅在同一物理系统处于关机状态时处理。设置前须确认 BMC 声明支持 PXE，已有其他启动覆盖时拒绝变更。BMC 须提供强 ETag，更新携带 `If-Match`。目标状态已经满足时直接进入只读确认。PATCH 回执丢失时只观察，不重发。结果以关机状态下读回的启动配置为准。

该操作的终态表示启动配置已核对。开机仍走独立的电源审批。站点部署方负责 PXE 服务、隔离网络和安装内容，系统安装结果与清理回收另行验收。[DMTF BootSourceOverride](https://redfish.dmtf.org/schemas/v1/DSP2046_2025.2.html)

### 裸金属部署与安装收据

应用迁移 `0120_bare_metal_deployments`。先确认一次性 PXE 配置成功，再提交开机申请。在独立开机审批前，由项目管理员关联这两项操作、已选安装镜像的 SHA-256，以及本项目具有只读成员身份的专用安装器服务主体。两个操作必须属于同一账号、站点、物理 UUID 和资产代次。

项目管理员从操作记录打开待审批的裸金属开机，在「裸金属部署」核对系统与预填的 PXE 操作，输入安装器服务主体 ID 和镜像摘要。项目成员可在同一详情刷新部署状态，独立审批人在批准前核对系统 UUID、PXE 操作、安装器和镜像摘要。安装器回报后，详情显示证据摘要和收到回报的时间。

```bash
cloud-manager operations register-deployment TENANT PROJECT < deployment.json
cloud-manager operations approve TENANT PROJECT BOOT_OPERATION --decision approve --deployment-id DEPLOYMENT
cloud-manager operations get-deployment TENANT PROJECT DEPLOYMENT
cloud-manager operations report-deployment TENANT PROJECT DEPLOYMENT < receipt.json
```

登记字段为 `stage_operation`、`boot_operation`、`image_sha256`、`installer_id`。安装器使用自己的服务令牌提交 `system_id`、`image_sha256`、`result` 和 `evidence_sha256`。结果为 `installed` 或 `failed`。API 集合路径为 `/v1/tenants/{tenant}/projects/{project}/bare-metal/deployments`，收据写入路径为 `/{deployment}/receipt`。

项目操作详情的可选字段 `bare_metal_deployment` 包含管理员可登记的 `stage_operation` 或已关联的 `deployment`。批准已关联部署的开机操作时，在 `POST /operations/{operation}/approval` 的正文提供已核对的 `deployment_id`。遗漏、ID 不匹配或待审批部署已过期会返回 409。控制台会随批准发送 ID。

登记保留两小时。Worker 开机前再次校验安装器代次和一次性 PXE 设置，确认同机上电且一次性覆盖已消费才完成开机操作。部署等待安装回报期间，其他平台电源或 PXE 变更及资产归属转移被拒绝。开机明确失败时展示 `boot_failed` 并解除部署保留；待回报任务到期显示 `expired`。开机确认后，指定安装器可登记终态。重复提交相同结果与证据摘要返回原回执，冲突结果被拒绝。账号、资产或安装器代次变化会拦截回报。

站点部署方负责 PXE 网络、镜像供给和安装器。安装收据记录由已绑定安装器提交的证据摘要，现场验收还需核对实际介质、网络隔离、系统启动和清理回收。

### 裸金属清理与回收

应用迁移 `0121_bare_metal_reclaims` 后更新 API 和 Worker。平台开机操作一旦进入派发或确认成功，即使派发之后超时或结果未知，设备的每次跨项目转移都要取得本次清理凭证。管理员先关机并确认当前站点盘点显示关机，随后提交账号、地域、物理 UUID 和站点清理服务主体。后者须是项目内启用的只读服务主体。申请需要当前资产归属版本和幂等键。

项目独立审批人批准后，指定服务在 30 分钟内领取一次授权。领取请求与证据回报均携带同一物理 UUID，并使用该服务主体的 Bearer 凭据。站点执行全盘清理及网络隔离，保存可供核验的证据原件，再触发一次同账号、同站点的完整关机盘点。服务端校验领取后的关机观测和当前授权代次，登记证据 SHA-256。重复提交相同证据返回原记录。

申请至归属转移完成期间，平台阻止该物理 UUID 的电源和 PXE 变更。独立审批人在领取前可拒绝。授权被领取后，失败或回报缺失会继续保留操作限制，不能通过再次领取解除。管理员完成归属转移时，系统复核本次凭证、归属版本和最近 10 分钟内的关机观测，并原子消费凭证。转移失败保留原凭证和归属。

在控制台的资产列表打开裸金属行的「回收与归属」。项目成员可查看当前回收和证据摘要，管理员可发起回收或登记恢复申请，独立审批人可提交相应决定。凭证验证后，管理员可选择自己同样有管理权限的目标项目并确认转移。转移成功后刷新原项目资产列表。

```bash
cloud-manager operations request-reclaim TENANT PROJECT --idempotency-key KEY --if-match REVISION < request.json
cloud-manager operations current-reclaim TENANT PROJECT --account ACCOUNT --region SITE --system-id UUID
cloud-manager operations review-reclaim TENANT PROJECT RECLAIM < decision.json
cloud-manager operations claim-reclaim TENANT PROJECT RECLAIM < claim.json
cloud-manager operations report-reclaim TENANT PROJECT RECLAIM < receipt.json
cloud-manager operations get-reclaim TENANT PROJECT RECLAIM
```

API 集合路径为 `/v1/tenants/{tenant}/projects/{project}/bare-metal/reclaims`。用 `POST` 申请并用 `GET /{reclaim}` 查询。审批、领取和回报依次用 `POST /{reclaim}/approval`、`POST /{reclaim}/claim`、`POST /{reclaim}/receipt`。申请正文包含 `account`、`region`、`system_id`、`sanitizer_id`。审批正文包含 `decision`，值为 `approve` 或 `reject`。领取正文包含 `system_id`，回报正文包含 `system_id` 和 `evidence_sha256`。平台记录站点服务的清理回报与关机观测。站点须保管证据并验证实际擦除和网络隔离。

用 `GET /current?account=ACCOUNT&region=SITE&system_id=UUID` 查询设备当前的回收。仅当前归属项目可查询，设备没有活动记录时返回 `{"reclaim":null}`。已消费的回收可继续用原 ID 在来源项目追溯。

#### 恢复已领取的回收任务

应用迁移 `0122_bare_metal_reclaim_recoveries`。若站点服务领取后失联，管理员须先使原服务授权代次失效，并取得现场证据，确认旧服务已隔离且对应物理设备已清理。管理员随后通过 `POST /{reclaim}/recovery` 登记 `recovery_id`、`system_id`、`isolation_sha256` 和 `evidence_sha256`。恢复 ID 由调用方生成。同一申请人以相同 ID 和正文重试会返回原记录。拒绝后可用新 ID 提交新证据。

在申请后完成一次显示关机的站点盘点。独立审批人核对原始证据，通过 `POST /{reclaim}/recovery/approval` 提交该申请的 `recovery_id` 与 `decision`。批准时平台复核当前账号和申请人代次、原清理授权及关机盘点，并记录审批与证据摘要。查询回收时可读到最新恢复申请。恢复过程保持设备操作限制，经过验证的凭证由原归属转移流程消费。

```bash
cloud-manager operations recover-reclaim TENANT PROJECT RECLAIM < recovery.json
cloud-manager operations review-reclaim-recovery TENANT PROJECT RECLAIM < decision.json
```

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

#### 对象存储桶盘点

执行 `cloud-manager migrate` 后，使用已核验账号扫描 `global` 范围。定时计划同样使用 `region=global` 和 `kind=object-bucket`，一次列举覆盖已授权账号的桶。返回的实际地域位于 `object_bucket.location`。

```bash
cloud-manager scan-tencent tenant-id account-id global object-bucket
cloud-manager scan-alibaba tenant-id account-id global object-bucket
cloud-manager scan-huawei tenant-id account-id global object-bucket
```

| 厂商 | 读取权限和认证 |
|---|---|
| 腾讯云 COS | 使用配置的访问密钥为 `GetService` 请求签名，授权 `name/cos:GetService`。[API 与分页](https://intl.cloud.tencent.com/document/product/436/8291) · [签名](https://cloud.tencent.com/document/api/436/7778) |
| 阿里云 OSS | 使用配置的首个地域构造 OSS 接入点，向 `ListBuckets` 发送 V4 签名，授权 `oss:ListBuckets`。[API 与分页](https://www.alibabacloud.com/help/en/oss/developer-reference/listbuckets) · [签名](https://www.alibabacloud.com/help/en/oss/developer-reference/recommend-to-use-signature-version-4) |
| 华为云 OBS | 通过已验证的 IAM Token 获取 15 分钟临时签名凭据，临时策略只授予 `obs:bucket:ListAllMyBuckets`。通过配置的地域接入点列举对象桶。[IAM 临时凭据](https://support.huaweicloud.com/api-iam/iam_04_0002.html) · [OBS API](https://support.huaweicloud.com/api-obs/obs_04_0020.html) |

在控制台选择“对象存储桶”，或通过资产、差异与导出接口筛选 `kind=object-bucket`。库存保存桶名、产品、实际地域、创建时间和可用的存储类型。资产 ID 采用 `桶名@UTC创建时间`，同名重建且创建时间变化时生成独立资产和归属记录。CSV 追加 `product`、`location`、`storage_class` 和 `created_at`。

腾讯云与阿里云按厂商标记续页，华为云使用有界账号列表。每页核对远端归属。完整盘点后，缺失的旧资产进入待确认状态。可在盘点命令后追加完整资产 ID，重新读取同一账号的桶清单。启用缺失确认前，需确认服务身份具备整个账号的列桶权限，且访问策略不会按标签或其他条件裁剪结果，再在私有厂商配置中设置 `"bucket_list_account_wide": true`。只有具备该配置且完整清单中找不到原创建批次时，才移除库存记录。

```bash
cloud-manager scan-alibaba tenant-id account-id global object-bucket \
  'example-bucket@2025-01-01T10:00:00Z'
```

#### 托管数据库盘点

执行 `cloud-manager migrate` 后，用已核验的云账号按地域扫描 `database-instance`。定时控制器使用同一资源类型和账号配置。

```bash
cloud-manager scan-tencent tenant-id account-id ap-guangzhou database-instance
cloud-manager scan-alibaba tenant-id account-id cn-hangzhou database-instance
cloud-manager scan-huawei tenant-id account-id cn-north-4 database-instance
```

| 厂商 | 盘点范围与权限 |
|---|---|
| 腾讯云 | 云数据库 MySQL、PostgreSQL、SQL Server。一个地域的盘点顺序读取三个产品，完整盘点需要相应的三项 `DescribeDBInstances` 权限。[MySQL](https://cloud.tencent.com/document/api/236/15872) · [PostgreSQL](https://cloud.tencent.com/document/product/409/16773) · [SQL Server](https://cloud.tencent.com/document/api/238/19969) |
| 阿里云 | ApsaraDB RDS 实例，使用地域接入点和 `rds:DescribeDBInstances`。[API](https://www.alibabacloud.com/help/en/rds/developer-reference/api-rds-2014-08-15-describedbinstances) · [接入点](https://www.alibabacloud.com/help/zh/rds/developer-reference/api-rds-2014-08-15-endpoint) |
| 华为云 | IAM 项目内的云数据库 RDS v3 实例，使用 `rds:instance:listAll` 或相应角色策略的列表权限。[API](https://support.huaweicloud.com/api-rds/rds_01_0004.html) |

在控制台选择“托管数据库”，或在项目资产、差异、拓扑和 CSV 导出接口传 `kind=database-instance`。资产响应的数据库字段为 `database_instance`，包含产品、引擎和已知版本。资产和历史支持按这些字段搜索，已纳管且可见的 VPC 在拓扑中显示。连接地址、登录账号和厂商响应中的连接配置不进入库存。CSV 增加 `product`、`engine` 和 `engine_version` 三列。

限流或临时请求失败时可从已提交的检查点续跑。续页总数变化会使游标失效，下次扫描从头开始。已完成全量盘点中的缺失实例会显示为待确认。追加精确实例 ID 进行缺失复核，只有同作用域的完整空结果才会移除本地资产。

```bash
cloud-manager scan-alibaba tenant-id account-id cn-hangzhou database-instance rm-example
```

使用签名 TLS 夹具及 PostgreSQL 验证了三家盘点链路。真实厂商地域支持、IAM 和限流仍需沙箱验证。

#### 托管数据库实例改名

腾讯云 MySQL、PostgreSQL、SQL Server，阿里云 ApsaraDB RDS 和华为云 RDS v3 共用项目操作账本。改名修改实例显示名。Worker 账号配置的 `allow_database_rename` 默认关闭，沙箱验收后按账号启用。迁移至 `0105_database_rename_events` 后升级 API、Worker 和事件发布器。

| 厂商 | 执行与观测权限 |
|---|---|
| 腾讯云 | 按产品使用 `ModifyDBInstanceName` 和对应的 `DescribeDBInstances`。[MySQL](https://www.tencentcloud.com/document/product/236/15877) · [PostgreSQL](https://www.tencentcloud.com/document/product/409/18101) · [SQL Server](https://intl.cloud.tencent.com/pt/document/product/238/32104) |
| 阿里云 | 使用 `rds:ModifyDBInstanceDescription` 和 `rds:DescribeDBInstances`。[改名接口](https://www.alibabacloud.com/help/en/rds/developer-reference/api-rds-2014-08-15-modifydbinstancedescription) |
| 华为云 | 使用 IAM 项目内的 `UpdateInstanceName` 和实例列表权限。[改名接口](https://support.huaweicloud.com/api-rds/rds_05_0005.html) |

从资产行选择「申请改名」，或调用 `POST /v1/tenants/{tenant}/projects/{project}/databases/rename`。请求体必填 `account`、`region`、`asset_id`、`current_name` 和 `name`。`If-Match` 使用盘点中的归属版本，`Idempotency-Key` 标识本次申请。新名称采用五种产品的统一平台规则，长 4 至 59 个字符且最多 64 个 UTF-8 字节，以英文字母或汉字开头，其余字符可为英文字母、汉字、数字或下划线。

```bash
cloud-manager operations rename-database-instance tenant-id project-id \
  --if-match 1 --idempotency-key rename-example-1 <<'JSON'
{"account":"prod-db","region":"cn-hangzhou","asset_id":"rm-example","current_name":"Original_db","name":"New_db"}
JSON
```

`202` 表示已登记待审批操作，`Location` 指向项目操作详情。`database_rename_intent` 保留厂商产品、原名称、新名称和归属版本。独立审批及适用的外部工单通过后，Worker 点查同一 ID 并核对当前名称和可操作状态，然后登记发送，至多提交一次。厂商 API 受理、回执丢失和目标返回异常都通过只读点查收敛。观察到同一实例在可操作状态下具有期望名称后，事务同时更新资产、操作终态、审计与发件箱。对账未确认时保留资产变更预约，由独立审批人通过 `operations retry-observation` 恢复观测。

#### 托管缓存盘点

执行 `cloud-manager migrate` 后，通过已核验的账号按地域扫描 `cache-instance`。定时控制器可以使用同一地域和资源类型。

```bash
cloud-manager scan-tencent tenant-id account-id ap-guangzhou cache-instance
cloud-manager scan-alibaba tenant-id account-id cn-hangzhou cache-instance
cloud-manager scan-huawei tenant-id account-id cn-north-4 cache-instance
```

| 厂商 | 读取权限 |
|---|---|
| 腾讯云 Redis | 使用 TC3 签名调用 `DescribeInstances`，授权 `redis:DescribeInstances`。[API](https://cloud.tencent.com/document/product/239/20018) |
| 阿里云 Tair | 使用 ACS3 签名调用 `DescribeInstances`，授权 `kvstore:DescribeInstances`。[API](https://www.alibabacloud.com/help/zh/redis/developer-reference/api-r-kvstore-2015-01-01-describeinstances-redis) · [接入点](https://www.alibabacloud.com/help/zh/redis/developer-reference/api-r-kvstore-2015-01-01-endpoint-redis) |
| 华为云 DCS | 使用已验证的 IAM 项目令牌调用 DCS v2 `ListInstances`，授权 `dcs:instance:list`。[API](https://support.huaweicloud.com/api-dcs/ListInstances.html) |

控制台可筛选“托管缓存”。项目资产、差异、拓扑与导出接口使用 `kind=cache-instance`。库存的 `cache_instance` 保存产品、引擎、已知版本和以 MiB 表示的容量，拓扑显示项目可见的已纳管 VPC。列表和历史可按产品、引擎或版本搜索，CSV 追加 `product`、`engine`、`engine_version` 和 `capacity_mib`。连接地址、登录账号和连接配置不进入库存。

厂商限流或请求失败后，扫描可以从已提交的检查点续跑。完整盘点后可在扫描命令末尾追加实例 ID，复核待确认缺失的资产。精确点查找到原实例时恢复观测状态。仅在确认服务身份具备同地域账号的完整查询权限，或华为云 IAM 项目内无企业项目裁剪的完整查询权限后，才在私有厂商配置中设置 `"cache_list_scope_wide": true`。此时同作用域的精确空页可移除库存记录。HTTP 404、授权失败和未启用完整权限声明的空页会保留待确认状态。

```bash
cloud-manager scan-alibaba tenant-id account-id cn-hangzhou cache-instance r-example
```

#### 托管缓存改名

腾讯云 Redis、阿里云 Tair 和华为云 DCS 的 Redis 兼容实例支持单资源显示名变更。执行 `cloud-manager migrate` 后部署 API、Worker 和事件发布器。Worker 的私有账号配置项 `"allow_cache_rename": true` 默认关闭，按账号完成沙箱验收后启用。

| 厂商 | 改名调用 |
| --- | --- |
| 腾讯云 Redis | `redis:ModifyInstance`，`Operation=rename`，固定发送单实例 ID 与名称。[API](https://cloud.tencent.com/document/api/239/31785) |
| 阿里云 Tair | `kvstore:ModifyInstanceAttribute`，发送 `InstanceId` 与 `InstanceName`。[API](https://help.aliyun.com/zh/redis/developer-reference/api-r-kvstore-2015-01-01-modifyinstanceattribute-redis) |
| 华为云 DCS | 在已绑定 IAM 项目内调用 `UpdateInstance`，请求体仅含 `name`。[API](https://support.huaweicloud.com/api-dcs/UpdateInstance.html) |

在资产清单选择“申请改名”，或调用 `POST /v1/tenants/{tenant}/projects/{project}/caches/rename`。请求使用 `If-Match` 的资产归属版本和 `Idempotency-Key`。新名称以英文字母开头，长度 4–60 位，可包含字母、数字、下划线和连字符。以下示例中的账号、地域、实例 ID、当前名称和归属版本取自项目资产。

```bash
cloud-manager operations rename-cache-instance tenant-id project-id \
  --idempotency-key cache-rename-2026-01 --if-match 1 < request.json
```

```json
{"account":"account-id","region":"cn-hangzhou","asset_id":"r-example","current_name":"old-cache","name":"new-cache"}
```

`202` 返回待审批操作，`Location` 指向操作详情，详情的 `cache_rename_intent` 保存变更范围和预期。独立审批及适用的外部工单通过后，Worker 核对同一实例的当前名称和可操作状态，再登记发送。发送后只读对账。回执不明时，独立审批人可以恢复观测。确认期望名称后，事务写入本地库存、操作终态、审计和 `cache-instance.operation.succeeded`，并使已启动的旧盘点领取失效。

#### Kubernetes 集群盘点

执行 `cloud-manager migrate` 后，使用已核验的云账号按地域扫描 `kubernetes-cluster`。定时计划使用同一资源类型和地域。

```bash
cloud-manager scan-tencent tenant-id account-id ap-guangzhou kubernetes-cluster
cloud-manager scan-alibaba tenant-id account-id cn-beijing kubernetes-cluster
cloud-manager scan-huawei tenant-id account-id cn-north-4 kubernetes-cluster
```

| 厂商 | 读取范围与权限 |
|---|---|
| 腾讯云 TKE | 托管与独立集群，使用 TC3 签名调用区域 `DescribeClusters`，授权 `tke:DescribeClusters`。[API](https://cloud.tencent.com/document/product/457/31862) |
| 阿里云 ACK | 使用 ACS3 签名调用地域接入点的 `GET /api/v1/clusters`，通过地域和可选集群 ID 过滤，授权 `cs:GetClusters`。[API](https://www.alibabacloud.com/help/zh/ack/ack-managed-and-ack-dedicated/developer-reference/api-cs-2015-12-15-describeclustersv1) · [签名](https://www.alibabacloud.com/help/en/sdk/product-overview/v3-request-structure-and-signature) |
| 华为云 CCE | 通过已核验的 IAM 项目令牌读取单个项目的 CCE v3 集群列表，授权 `cce:cluster:list`。单次响应最多接收 1000 个集群。[API](https://support.huaweicloud.com/api-cce/cce_02_0239.html) |

控制台提供“Kubernetes 集群”筛选，展示厂商产品、集群类型、已知版本和厂商报告的节点数。项目资产、差异、拓扑与导出支持 `kind=kubernetes-cluster`，响应以 `kubernetes_cluster` 返回集群字段。节点数采用各厂商定义，缺省表示未知。项目可见的已纳管 VPC 会出现在拓扑中。CSV 追加 `product`、`cluster_type`、`kubernetes_version` 和 `node_count`。该能力只读取云管理面元数据，库存不保存 API Server 地址、认证配置或任意注解。

TKE 和 ACK 支持分页检查点，CCE 项目列表作为一个完整页提交。追加集群 ID 可复核待确认缺失的资产。TKE、ACK 按 ID 过滤，CCE 重新读取同一 IAM 项目的完整列表后核对 ID。确认服务身份具备整个盘点作用域的读取权限后，可在私有厂商配置中设置 `"kubernetes_list_scope_wide": true`，允许经确认缺失时移除本地库存。权限错误、404、无效响应及未声明完整可见性的缺失结果均保留待确认状态。

```bash
cloud-manager scan-tencent tenant-id account-id ap-guangzhou kubernetes-cluster cls-example
```

##### 节点池快照

先执行迁移并盘点父集群，再按账号、地域和集群 ID 刷新。命令读取厂商管理面数据并原子发布这次刷新取得的全部节点池，失败时保留最近的完整快照。

```bash
cloud-manager refresh-tencent-cluster-node-pools tenant-id account-id ap-guangzhou cls-example
cloud-manager refresh-alibaba-cluster-node-pools tenant-id account-id cn-beijing c-example
cloud-manager refresh-huawei-cluster-node-pools tenant-id account-id cn-north-4 cluster-uuid
```

| 厂商 | 读取 API 与权限 |
|---|---|
| 腾讯云 TKE | 2022-05-01 `DescribeNodePools`，需要 `tke:DescribeNodePools`。按页核对集群 ID、总量和节点池唯一性。[API](https://cloud.tencent.com/document/api/457/103205) |
| 阿里云 ACK | `GET /clusters/{ClusterId}/nodepools`，需要 `cs:DescribeClusterNodePools`。使用父集群地域的 ACS3 接入点。[API](https://www.alibabacloud.com/help/zh/ack/ack-managed-and-ack-dedicated/developer-reference/api-cs-2015-12-15-describeclusternodepools) |
| 华为云 CCE | v3 `ListNodePools`，需要 `cce:nodepool:list`。同一 IAM 项目内读取父集群的节点池，并请求包含默认节点池。[API](https://support.huaweicloud.com/api-cce/cce_02_0269.html) |

定时刷新复用 `controller` 进程和 `CLOUD_MANAGER_CONTROLLER_PROFILES`。为每个需要纳管的父集群登记 `kind=kubernetes-node-pools`，并填入地域和 `target_id`。新字段在普通盘点计划中默认留空。使用 `ON CONFLICT` 更新计划的运维脚本应在冲突目标中包含 `target_id`。父集群需要在同一个账号和地域完成盘点，节点池计划可以与父集群的 `kubernetes-cluster` 盘点计划并存。

```sql
INSERT INTO cmp_inventory_schedules (tenant_id, account_id, region, kind, target_id, interval_seconds)
VALUES ('tenant-id', 'account-id', 'ap-guangzhou', 'kubernetes-node-pools', 'cls-example', 600);
```

计划按成功间隔重复执行，失败使用现有退避和厂商 `Retry-After`。节点池快照与本次计划结果在同一事务提交。计划停用、租约被接管、账号凭据轮换或父集群观测已变化时，旧执行者无法发布快照。控制器停机让出计划后，其他副本可以接续。

项目 API 为 `GET /v1/tenants/{tenant}/projects/{project}/assets/kubernetes-clusters/{asset}/node-pools?account=…&region=…`。第一页返回 `snapshot`、`observed_at` 和可选的 `next_offset`。续页使用同一 `snapshot` 和 `offset`。快照已替换时返回 409，调用方从第一页重读。父集群不可见、凭据已轮换或暂未刷新时返回 404。控制台在父集群行提供“查看节点池”。

只公开节点池 ID、名称、已知类型和状态、节点数和自动伸缩配置。数值采用各厂商定义，已知零与未知值分别显示。用户脚本、认证配置、标签和任意注解不进入快照。单个集群最多接收 1000 个节点池。

#### Kubernetes 工作负载

腾讯云 TKE、阿里云 ACK 和华为云 CCE 共用 Kubernetes API 采集工作负载。先运行 `migrate` 应用 `0086_cluster_workloads` 和 `0087_cluster_workload_schedules`，完成云账号核验和父集群盘点，再为需要采集的集群增加只读凭据。

在对应的已有云账号私有配置中添加 `kubernetes_workloads`，以 `地域/集群 ID` 显式绑定工作负载凭据文件。例如在腾讯云配置中添加以下字段。

```json
{
  "kubernetes_workloads": {
    "ap-guangzhou/cls-example": "/etc/cloud-manager/credentials/tke-cls-example.json"
  }
}
```

工作负载凭据文件使用绝对路径，只有文件所有者可读。字段如下。

| 字段 | 用途 |
| --- | --- |
| `cloud_identity`、`credential_revision` | 核验后的云账号远端身份、当前凭据版本，须与父集群库存对应 |
| `endpoint` | 从控制器所在网络可达的 Kubernetes HTTPS API 源，例如 `https://k8s.example.internal:6443` |
| `ca_pem`、`bearer_token` | 校验 API Server 的 CA 和仅供采集的 Bearer 凭据 |
| `identity_namespace`、`identity_uid` | 通过受信渠道核对的锚点命名空间名称和 UID，采集前后均确认身份 |
| `namespaces` | 允许采集的命名空间名称，最多 32 个 |

采集角色需要对锚点命名空间的 `namespaces` 单资源 `get`，ClusterRole 可用 `resourceNames` 限定名字。每个允许采集的命名空间授权 `apps` 组中 `deployments`、`statefulsets`、`daemonsets` 的 `list`，以及 `batch` 组中 `jobs`、`cronjobs` 的 `list`。控制器直连配置的 HTTPS 端点，验证 CA 和主机名，拒绝重定向。注册新的采集范围或替换集群身份时，先轮换平台云账号凭据版本并重新盘点父集群，再启用新配置。

```sh
cloud-manager refresh-tencent-cluster-workloads tenant-id account-id ap-guangzhou cls-example
cloud-manager refresh-alibaba-cluster-workloads tenant-id account-id cn-beijing c-example
cloud-manager refresh-huawei-cluster-workloads tenant-id account-id cn-north-4 cluster-uuid
```

定时采集由 `controller` 执行。在滚动升级中先部署支持工作负载的新控制器，再启用 `kind=kubernetes-workloads`、`target_id=集群 ID` 的计划。它和父集群盘点及节点池计划各自独立。

```sql
INSERT INTO cmp_inventory_schedules (tenant_id, account_id, region, kind, target_id, interval_seconds)
VALUES ('tenant-id', 'account-id', 'ap-guangzhou', 'kubernetes-workloads', 'cls-example', 600);
```

一次刷新最多采集 5000 条工作负载。同一命名空间与类型的列表逐页核对 `resourceVersion`，所有列表成功且身份复核通过后，在同一事务发布快照与计划成功状态。跨资源类型和命名空间的列表各有独立观测时点。采集失败、权限不足、游标失效或旧租约尝试提交时保留上一份完整快照。游标失效后下一轮从第一页开始。[Kubernetes 分页语义](https://kubernetes.io/docs/reference/using-api/api-concepts/#retrieving-large-results-sets-in-chunks)

项目接口为 `GET /v1/tenants/{tenant}/projects/{project}/assets/kubernetes-clusters/{asset}/workloads?account=…&region=…`。返回 `workloads`、`observed_at`、`snapshot` 和分页信息。续页携带同一个 `snapshot`，遇到 409 从第一页读取。父集群不可见、凭据代次过期或尚无完整快照时返回 404。控制台在集群资产行提供“查看工作负载”。

快照公开工作负载 UID、名称、命名空间、类型和已知计数或暂停状态。采集凭据、Pod 模板、环境变量、日志、容器镜像、任意标签及注解不入库。

##### 持续订阅

执行 `migrate` 应用 `0088_cluster_workload_checkpoints` 和 `0089_cluster_workload_watch_schedules`。将普通控制器更新到支持订阅计划的版本，再启用下列计划。订阅复用云账号私有配置和命名空间白名单，每个白名单命名空间还需对上述五类资源授予 `watch`。使用与控制器相同的 `CLOUD_MANAGER_CONTROLLER_PROFILES` 启动独立进程。

```sh
cloud-manager workload-controller
```

```sql
INSERT INTO cmp_inventory_schedules (tenant_id, account_id, region, kind, target_id, interval_seconds)
VALUES ('tenant-id', 'account-id', 'ap-guangzhou', 'kubernetes-workload-watch', 'cls-example', 60);
```

每个订阅控制器并行处理最多 4 个集群，每个集群最多保持 8 个集合连接。每次订阅请求向 API Server 请求 5 秒窗口，单轮上限为 3 分钟，租约为 4 分钟。成功后间隔 1 秒继续领取，`interval_seconds` 参与现有新鲜度告警阈值。失联、限流和连续失败使用各自计划的退避，普通盘点由独立进程领取。

首次订阅逐集合读取完整列表，然后从各自的 `resourceVersion` 续订。支持 `ADDED`、`MODIFIED`、`DELETED` 和 `BOOKMARK`。连接截断时以最后一条完整事件为界处理。遇到 HTTP 410 或流内过期事件时，丢弃该集合本轮增量，重读该集合并从新版本订阅。需要恢复的集合和其他集合的结果一起通过校验后才提交。[Kubernetes 订阅语义](https://kubernetes.io/docs/reference/using-api/api-concepts/#efficient-detection-of-changes)

订阅检查点、公开快照和计划完成状态在同一事务中写入。提交时核对父集群、凭据版本、租约和读取时的快照基线。只有书签或游标变化时，保留公开快照标识和内容的 `observed_at`，避免在翻页中途无故换代。手动全量刷新会生成新快照，下一轮订阅重新建立检查点。持续订阅按端点、信任根、集群锚点和白名单识别检查点，同一采集身份的 Token 更新可继续使用原版本。

##### 调整工作负载副本

应用 `0090_workload_change_reservations` 至 `0092_workload_scale_outbox` 并升级 Worker 后，写入已启用的云账号可以为 Deployment 或 StatefulSet 申请调整期望副本数。申请需要独立审批。同一父集群的活动申请占用变更名额，并阻止归属转移。

在 Worker 使用的云账号配置中添加 `kubernetes_workload_writers`。键与只读配置相同，值指向另一份私有凭据文件。写入文件沿用只读文件的字段，并增加 `max_replicas`，范围为 1 至 100。两份文件使用相同的端点、CA、集群锚点与云账号凭据版本，写入凭据采用独立 Token，命名空间为读取白名单的子集。给写入身份授予锚点命名空间的 `get`，以及目标命名空间中 `deployments/scale` 和 `statefulsets/scale` 的 `get`、`update`。可用 `resourceNames` 进一步限定部署名称。缺少写入配置时，已获批的申请在发送前失败。

```json
{
  "kubernetes_workload_writers": {
    "ap-guangzhou/cls-example": "/etc/cloud-manager/credentials/tke-cls-example-writer.json"
  }
}
```

项目接口为 `POST /v1/tenants/{tenant}/projects/{project}/kubernetes-workloads/scale`。请求使用 `Idempotency-Key` 和包含当前父集群归属版本的 `If-Match`。正文填写快照中的工作负载 UID、命名空间、类型与名称，以及目标副本数。

```json
{
  "account": "account-id",
  "region": "ap-guangzhou",
  "asset_id": "cls-example",
  "snapshot": "00000000-0000-4000-8000-000000000001",
  "uid": "workload-uid",
  "namespace": "checkout",
  "kind": "Deployment",
  "name": "payments",
  "replicas": 4
}
```

```sh
cloud-manager operations scale-workload tenant-id project-id --idempotency-key scale-payments-1 --if-match 2 < scale.json
```

也可以从控制台的工作负载详情提交。Worker 在发送前核对云端 UID 与当前期望副本数，通过 `PUT /scale` 携带 UID 和当前 `resourceVersion` 写入。冲突返回失败，回执丢失进入只读观测。观察到同一 UID 的期望副本数达到目标后，同一事务更新库存、结束操作并生成终态事件。若出现其他期望值或目标重建，交由独立审批人核实证据后重启只读观测。前置条件、审批和只读恢复均通过现有操作接口完成。[Kubernetes Scale 前置条件](https://kubernetes.io/docs/reference/generated/kubectl/kubectl-commands.html#scale)

成功表示观测到期望副本数已调整。Pod 就绪度继续由采集更新。HPA 或 GitOps 控制器后续变更会作为新的观测进入库存。

#### 公网 DNS 托管区与解析记录

执行 `cloud-manager migrate` 应用 `0067_dns_zone_records` 和 `0068_dns_zone_schedules`。使用已核验的只读云账号扫描 `global` 范围，定时盘点可使用相同的 `region=global`、`kind=dns-zone`。

```bash
cloud-manager scan-tencent tenant-id account-id global dns-zone
cloud-manager scan-alibaba tenant-id account-id global dns-zone
cloud-manager scan-huawei tenant-id account-id global dns-zone
```

| 厂商 | 范围 | 读取权限 |
|---|---|---|
| 腾讯云 DNSPod | 当前账号自有公网托管区 | `dnspod:DescribeDomainList`、`dnspod:DescribeRecordList` |
| 阿里云云解析 DNS | 当前账号可读的公网托管区 | `alidns:DescribeDomains`、`alidns:DescribeDomainInfo`、`alidns:DescribeDomainRecords` |
| 华为云 DNS | 中国站公网托管区，通过华北-北京四 `cn-north-4` 访问，只接受已核验 IAM 项目的结果 | `dns:zone:list`、`dns:recordset:list` |

资产列表、历史和 CSV 支持 `kind=dns-zone`。托管区名称使用 ASCII 或厂商提供的 Punycode。完整盘点中未出现的托管区标为待确认缺失，控制台隐藏其记录快照。厂商列表和记录集在多个 API 调用间可发生变更，采集会拒绝可检测的分页总数变化和记录 ID 重复。

扫描发现托管区后，以资产 ID 刷新单区记录。阿里云按域名查询记录，会在采集前后分别核对域名和 ID。

```bash
cloud-manager refresh-tencent-dns-zone tenant-id account-id zone-id
cloud-manager refresh-alibaba-dns-zone tenant-id account-id zone-id
cloud-manager refresh-huawei-dns-zone tenant-id account-id zone-id
```

在资产详情中选择“查看解析记录”，或调用 `GET /v1/tenants/{tenant}/projects/{project}/assets/dns-zones/{asset}/records?account={account}`。结果按记录名、类型及 ID 排序，`limit` 默认 50、最大 100，后续读取使用 `next_offset`。如果 `observed_at` 变化，从第一页重新读取。

快照最多包含 5,000 条记录和 4 MiB 投影。记录包括名称、类型、TTL、厂商状态和可用的解析线路。A、AAAA、CNAME、NS、PTR 和 MX 返回通过类型校验的值。TXT、SRV、CAA、SOA 等类型只保存值数量。权限失败、分页错误和重叠刷新均保留上一份有效快照。读取受项目资产可见性及当前云凭据版本约束。该查询提供公网 DNS 盘点与记录快照。

#### 创建公网 DNS 记录

运行 `cloud-manager migrate` 后，从已刷新的托管区快照选择“申请添加记录”。入口创建包含一个地址的 A 或 AAAA 记录集，使用默认解析线路。同名同类型记录、CNAME 冲突及子域委派会阻止提交或云端写入。TTL 接受 1 至 604800 秒，厂商套餐可能进一步限制取值。

```bash
cloud-manager operations create-dns-record tenant-id project-id \
  --idempotency-key dns-create-example --if-match 3 < request.json
```

```json
{
  "account": "account-id",
  "asset_id": "zone-id",
  "observed_at": "2026-11-26T00:00:00Z",
  "name": "www.example.test",
  "type": "A",
  "value": "192.0.2.42",
  "ttl": 600
}
```

API 为 `POST /v1/tenants/{tenant}/projects/{project}/dns-zones/records`，使用 `Idempotency-Key` 和带托管区归属版本的 `If-Match`。须由独立审批人批准。账号必须启用写入，且 Worker 私有凭据配置需要显式设置 `"allow_dns_record_creation": true`。

| 厂商 | 创建权限 | 回执丢失后的证据 |
|---|---|---|
| 腾讯云 DNSPod | `dnspod:CreateRecord` | 从记录列表核实内部对账标记与记录属性 |
| 阿里云云解析 DNS | `alidns:AddDomainRecord` | 独立审批人查阅厂商审计后提交证据引用和准确的厂商记录 ID |
| 华为云 DNS 中国站 | `dns:recordset:create` | 从记录集列表核实内部对账标记与记录属性 |

回执丢失后 Worker 只进行读取，不会重复创建。阿里云审批人使用 `operations retry-observation TENANT PROJECT OPERATION --evidence-ref REF --provider-resource-id RECORD_ID` 或相应 API 提交 `evidence_ref` 和 `provider_resource_id`，Worker 会在同一托管区复核 ID 和完整属性。创建确认后原快照失效。刷新托管区后可查看新增记录。

#### 启用或停用公网 DNS 解析

执行 `cloud-manager migrate` 后，项目操作者可从 DNS 快照选择一条稳定状态的 A 或 AAAA 记录，提交启用或停用申请。请求固定托管区归属版本、快照时间及记录内容。申请必须经另一位项目成员审批，且同一托管区一次只允许一条进行中的变更。控制台在记录旁显示申请入口。

```bash
cloud-manager operations set-dns-record-status tenant-id project-id \
  --idempotency-key dns-status-example --if-match 3 < request.json
```

```json
{
  "account": "account-id",
  "asset_id": "zone-id",
  "record_id": "record-id",
  "observed_at": "2026-11-26T00:00:00Z",
  "enabled": false
}
```

API 为 `POST /v1/tenants/{tenant}/projects/{project}/dns-zones/record-status`，使用 `Idempotency-Key` 和携带托管区归属版本的 `If-Match`。操作的 `202` 响应提供详情地址。云端写入默认关闭，执行需要账号开启写入，且 Worker 私有凭据配置 `"allow_dns_record_status": true`。

| 厂商 | 精确复核权限 | 状态变更权限 |
|---|---|---|
| 腾讯云 DNSPod | `dnspod:DescribeRecord` | `dnspod:ModifyRecordStatus` |
| 阿里云云解析 DNS | `alidns:DescribeDomainRecordInfo` | `alidns:SetDomainRecordStatus` |
| 华为云 DNS 中国站 | `dns:recordset:get` | `dns:recordset:setStatus` |

Worker 在发送前核对厂商记录 ID、托管区归属、名称、类型、已投影线路、TTL、记录值和原状态。三家均使用专用状态接口。发送后仅用精确读取确认目标状态和其余字段。审批人可为待复核操作提交证据引用并恢复只读观测。确认成功会使写入前的快照失效，刷新后才能据新快照继续申请。DNS 客户端的缓存按各自 TTL 到期。

#### 删除停用的公网 DNS 记录

执行 `cloud-manager migrate` 应用 `0075_dns_record_deletion`。项目操作者可从托管区快照中为一条已停用的 A 或 AAAA 记录申请删除，审批人可查看该记录的厂商 ID、类型、地址、TTL 和已归档线路。必须经独立审批，同区创建、启停及删除共用一份活动操作预约。执行要求云账号已启用写入，且 Worker 私有凭据配置显式设置 `"allow_dns_record_deletion": true`。

```bash
cloud-manager operations delete-dns-record tenant-id project-id \
  --idempotency-key dns-delete-example --if-match 3 < request.json
```

```json
{
  "account": "account-id",
  "asset_id": "zone-id",
  "record_id": "record-id",
  "observed_at": "2026-11-26T00:00:00Z"
}
```

API 为 `POST /v1/tenants/{tenant}/projects/{project}/dns-zones/record-deletion`，要求 `Idempotency-Key` 和托管区归属版本 `If-Match`。除了盘点所需读权限，Worker 需要精确读取记录及删除权限。腾讯云还需通过自有域名列表确认托管区，华为云还需查询公网托管区详情并通过 v2.1 读取记录关联。

| 厂商 | 删除 API | 删除权限 |
|---|---|---|
| 腾讯云 DNSPod | [DeleteRecord](https://cloud.tencent.com/document/product/1427/56176) | `dnspod:DeleteRecord` |
| 阿里云云解析 DNS | [DeleteDomainRecord](https://www.alibabacloud.com/help/en/dns/api-alidns-2015-01-09-deletedomainrecord) | `alidns:DeleteDomainRecord` |
| 华为云 DNS 中国站 | [DeleteRecordSet](https://support.huaweicloud.com/api-dns/DeleteRecordSet.html) | `dns:recordset:delete` |

Worker 发送前核对托管区身份、目标记录 ID、停用状态、名称、类型、已投影线路、TTL 和地址。华为云仅接受默认线路的非系统记录，并拦截别名及关联了健康检查的记录集。删除只发送一次。Worker 在两轮连续的完整分页读取中确认原记录 ID 已消失后才将操作标记成功。同名的新记录有独立 ID，保留在托管区中。404、分页中断及仍存在的原记录会清空连续缺失计数。

证据不足时，审批人可使用 `operations retry-observation TENANT PROJECT OPERATION --evidence-ref REF` 恢复只读核查。成功确认、旧快照失效及终态事件在同一事务内提交，刷新托管区可读取新快照。上线前需在对应厂商沙箱验证账号角色及记录类型。

#### 负载均衡盘点

执行 `cloud-manager migrate` 应用 `0056_load_balancer_inventory` 与 `0057_load_balancer_schedules`。使用 `scan-tencent`、`scan-alibaba` 或 `scan-huawei`，在租户、账号和地域之后传入 `load-balancer`。定时盘点使用同一种类。

| 厂商 | 资源与分页 |
| --- | --- |
| 腾讯云 | CLB 通用型与传统型使用 TC3 签名和偏移分页。记录 VPC、子网、可用区及安全组等可验证关系。 [DescribeLoadBalancers](https://cloud.tencent.com/document/product/214/30685) |
| 阿里云 | 同一地域盘点 ALB、NLB 与传统型 CLB。先后使用 ALB 和 NLB 的厂商令牌及 CLB 的页号。检查点保存当前产品，三个产品都取得完整结果时才结束该轮。部署地域及凭据需支持这三个读取 API。 [ALB](https://www.alibabacloud.com/help/en/slb/application-load-balancer/developer-reference/api-alb-2020-06-16-listloadbalancers) · [NLB](https://www.alibabacloud.com/help/en/slb/network-load-balancer/developer-reference/api-nlb-2022-04-30-listloadbalancers) · [传统型 CLB](https://www.alibabacloud.com/help/en/slb/classic-load-balancer/developer-reference/api-slb-2014-05-15-describeloadbalancers) |
| 华为云 | ELB 通过原 IAM 项目读取，核对每条资源的项目 ID，使用 V3 标记续页。通过 VPC、虚拟子网、可用区和弹性公网 IP 的稳定标识建立关系。 [ListLoadBalancers](https://support.huaweicloud.com/intl/en-us/api-elb/ListLoadBalancers.html) |

项目可按名称、ID、域名或已采集地址查询 `kind=load-balancer`。响应中的 `load_balancer` 包含产品、地址类型、已知域名和私网地址，`public_addresses` 返回已知公网地址。地址类型 `dual` 表示前端同时涉及公网与私网。阿里云 ALB 列表提供 DNS 名称，地址和可用区按厂商列表实际提供的字段登记。资产历史保留变更前后的属性。筛选同一种类的 CSV 追加公网地址、产品、地址类型、域名和私网地址。拓扑只解析同账号、地域且项目可见的目标。

完整盘点后未出现的资产先进入 `missing_candidate`。在三家扫描命令的 `load-balancer` 之后追加该资产 ID，可重新点查缺失候选。完整的精确空结果才确认移除。404、限流、权限错误和不完整分页仍保留候选。真实厂商沙箱验收仍待完成。

#### 监听器和后端组

应用 `0058_load_balancer_configuration` 后，用已核验账号的读取凭据刷新单个已盘点实例。参数依次为租户、云账号、地域和负载均衡 ID。

```bash
cloud-manager refresh-tencent-load-balancer TENANT ACCOUNT REGION ASSET_ID
cloud-manager refresh-alibaba-load-balancer TENANT ACCOUNT REGION ASSET_ID
cloud-manager refresh-huawei-load-balancer TENANT ACCOUNT REGION ASSET_ID
```

| 厂商 | 快照内容与只读权限 |
| --- | --- |
| 腾讯云 | 通用 CLB 需要 `DescribeLoadBalancers`、`DescribeListeners`、`DescribeTargetGroups`，读取监听器和直接或经转发规则绑定的目标组。传统 CLB 使用 `DescribeClassicalLBListeners` 读取监听器。 [通用监听器](https://cloud.tencent.com/document/api/214/30686) · [目标组](https://cloud.tencent.com/document/api/214/40554) · [传统监听器](https://cloud.tencent.com/document/api/214/31791) |
| 阿里云 | ALB 需要 `ListLoadBalancers`、`ListListeners`、`ListRules`、`ListServerGroups`，读取监听器及默认动作、转发和镜像引用的服务器组。NLB 使用 `ListLoadBalancers`、`ListListeners`、`ListServerGroups`。CLB 使用 `DescribeLoadBalancers`、`DescribeLoadBalancerAttribute`、`DescribeVServerGroups`，读取监听器和虚拟服务器组，监听器标识由实例 ID、协议和端口组成。 [ALB 转发规则](https://www.alibabacloud.com/help/en/slb/application-load-balancer/developer-reference/api-alb-2020-06-16-listrules) · [NLB 服务器组](https://www.alibabacloud.com/help/en/slb/network-load-balancer/developer-reference/api-nlb-2022-04-30-listservergroups) · [CLB 虚拟服务器组](https://www.alibabacloud.com/help/en/slb/classic-load-balancer/developer-reference/api-slb-2014-05-15-describevservergroups) |
| 华为云 | ELB V3 需要 `elb:loadbalancers:list`、`elb:listeners:list` 和 `elb:pools:list`，按父实例查询监听器和后端服务器组。 [监听器](https://support.huaweicloud.com/intl/en-us/api-elb/ListListeners.html) · [后端服务器组](https://support.huaweicloud.com/intl/en-us/api-elb/ListPools.html) |

刷新读取成功后一次提交。单个分页序列最多 20 页，单个实例最多保存 1,000 个监听器和后端组。超限、跨父实例响应、分页失败和结构错误保留上一份完整快照。凭据版本变更、账号停用或父实例待确认缺失时，查询隐藏已保存的快照。并发刷新中，较早启动的请求无法覆盖更新的快照。

有父实例查看权限的项目可以使用控制台的“查看配置”，或调用 `GET /v1/tenants/{tenant}/projects/{project}/assets/load-balancers/{asset}/configuration?account=ACCOUNT&region=REGION`。返回采集时间、监听器及后端组，以及厂商提供的监听器到组的关联。快照没有可访问结果时返回 404，共享组的其他实例引用不进入响应。此入口使用数据库保存的快照。

#### 后端成员和健康检查

在刷新命令末尾添加 `--include-backends`，同时采集关联组的成员和厂商报告的健康结果。常规刷新保存基础配置快照，带该参数的刷新保存同一采集批次的配置和后端状态。对应的附加读取权限如下。

| 厂商 | 成员和健康检查来源 |
| --- | --- |
| 腾讯云 | 通用 CLB 使用 `DescribeTargetGroupInstances` 枚举目标组成员，按需使用 `DescribeTargets` 读取监听器及转发规则的直接绑定，用 `DescribeTargetHealth` 读取健康结果。传统 CLB 使用 `DescribeClassicalLBTargets` 枚举实例级默认成员，用 `DescribeClassicalLBHealthStatus` 读取监听器健康结果。 [目标组成员](https://cloud.tencent.com/document/product/214/40556) · [直接绑定](https://cloud.tencent.com/document/product/214/30684) · [传统 CLB 成员](https://cloud.tencent.com/document/api/214/31790) |
| 阿里云 | ALB 和 NLB 使用 `ListServerGroupServers` 和 `GetListenerHealthStatus`。健康结果保留厂商报告的异常、初始化等状态，缺少报告的成员健康状态未知。传统 CLB 从 `DescribeLoadBalancerAttribute` 读取默认组成员，从 `DescribeVServerGroupAttribute` 读取虚拟服务器组成员，用 `DescribeMasterSlaveServerGroups` 和 `DescribeMasterSlaveServerGroupAttribute` 查询主备组，用 `DescribeHealthStatus` 读取监听器健康结果。 [传统 CLB 实例属性](https://www.alibabacloud.com/help/zh/slb/classic-load-balancer/developer-reference/api-slb-2014-05-15-describeloadbalancerattribute) · [主备组列表](https://www.alibabacloud.com/help/zh/slb/classic-load-balancer/developer-reference/api-slb-2014-05-15-describemasterslaveservergroups) · [传统 CLB 健康检查](https://www.alibabacloud.com/help/en/slb/classic-load-balancer/developer-reference/api-slb-2014-05-15-describehealthstatus) |
| 华为云 | 使用 `elb:members:list` 逐组查询成员，只保留当前实例的监听器健康结果。厂商只提供组级状态时，在确认组与单一当前监听器关联的条件下采用。 [ELB 后端成员](https://support.huaweicloud.com/intl/en-us/api-elb/ListMembers.html) |

扩展快照包含 `backends.members` 与 `backends.checks`。成员包含组、端口和厂商提供的权重。检查包含监听器、已知组和规则、端口及状态。两类记录用匿名标识 `key` 关联。应用保留每个父实例的独立随机密钥，同一父实例中，云端端点标识和端口相同时刷新可复用标识。旧快照在迁移后首次扩展刷新时生成新标识。后端地址、厂商实例 ID 和共享组的其他监听器不会落库或进入响应。

`target_groups[].kind` 标识默认组 `default`、主备组 `primary-secondary`、传统虚拟组 `virtual` 和直接绑定 `direct`。传统默认成员由实例登记，`port` 为 `0` 表示后端端口按监听器配置。阿里云默认组的监听器绑定需云端证据，快照继续保留实例级成员。主备成员的 `role` 分为 `primary` 和 `secondary`。腾讯云通用 CLB 的直接成员只纳入未绑定目标组的监听器或转发规则，厂商在关联目标组下重复报告的成员仍归原组。

每个实例的扩展读取最多额外发出 128 次厂商请求，保存至多 4,000 条成员和检查记录。超限、越界响应、限流或不完整分页均保留上一份完整快照。阿里云 NLB 健康接口若返回续页令牌，会将该轮判定为不完整，因为公开的请求合同没有续页参数。未绑定至实例的通用目标组和腾讯云后端云函数仍待纳管。

#### 负载均衡改名

执行迁移 `0059_load_balancer_rename` 和 `0060_load_balancer_rename_outbox` 后，项目操作者可在控制台选中已观测负载均衡并申请改名，或通过 CLI 提交同一申请。请求发送前须由另一位有权限的成员审批。默认关闭云端写入，启用时需将 Worker 对应的私有凭据配置设为 `"allow_load_balancer_rename": true`，并启用账号写入模式。

```sh
cloud-manager operations rename-load-balancer TENANT PROJECT --idempotency-key REQUEST_KEY --if-match 7 < rename.json
```

请求体示例以 `account` 表示本平台登记的云账号 ID。`current_name` 填写资产最近盘点的名称，原名称为空时传空字符串。`If-Match` 是归属版本。新名称由 2 至 60 个 ASCII 字母、数字、下划线或短划线组成，以字母开头。

```json
{"account":"prod-cloud","region":"ap-guangzhou","asset_id":"lb-example","current_name":"edge-old","name":"edge-new"}
```

API 使用 `POST /v1/tenants/{tenant}/projects/{project}/load-balancers/rename`，持久受理后返回 `202` 和操作地址。Worker 在发送前复核审批、账号代次和云端旧名称。发送后仅进行观测。腾讯云 CLB 和阿里云 NLB 在有回执时还核对异步任务状态。观察到同一实例以新名称处于可用状态后，同一事务更新资产名称、拦截此前启动的盘点页并写入成功事件。结果不明时可由独立审批人恢复只读观测。厂商原生控制台等外部入口在点查与写入之间仍有并发修改窗口。

| 厂商 | 写入与回执 |
| --- | --- |
| 腾讯云 CLB | `ModifyLoadBalancerAttributes` 与 `DescribeTaskStatus`。 [改名](https://cloud.tencent.com/document/api/214/30680) · [任务状态](https://cloud.tencent.com/document/product/214/30683) |
| 阿里云 | ALB 和 NLB 使用 `UpdateLoadBalancerAttribute`，NLB 使用 `GetJobStatus`。传统 CLB 使用 `SetLoadBalancerName`。 [ALB](https://www.alibabacloud.com/help/zh/slb/application-load-balancer/developer-reference/api-alb-2020-06-16-updateloadbalancerattribute) · [NLB](https://www.alibabacloud.com/help/zh/slb/network-load-balancer/developer-reference/api-nlb-2022-04-30-updateloadbalancerattribute) |
| 华为云 ELB V3 | `UpdateLoadBalancer` 的请求只提交名称，并核对响应的项目及资源标识。 [改名](https://support.huaweicloud.com/api-elb/UpdateLoadBalancer.html) |

真实厂商沙箱验收仍待完成。

#### 负载均衡监听器改名

执行迁移 `0061_load_balancer_listener_rename` 后，项目操作者可在负载均衡配置详情中选择监听器并申请改名。API 为 `POST /v1/tenants/{tenant}/projects/{project}/load-balancers/rename-listener`，持久受理后返回 `202` 和操作地址。写入默认关闭。启用时需要账号处于写入模式，且 Worker 私有凭据配置为 `"allow_load_balancer_listener_rename": true`。

```sh
cloud-manager operations rename-load-balancer-listener TENANT PROJECT --idempotency-key REQUEST_KEY --if-match 7 < rename-listener.json
```

```json
{"account":"prod-cloud","region":"cn-hangzhou","asset_id":"nlb-example","listener_id":"lsn-example@443","current_name":"edge-old","name":"edge-new"}
```

`asset_id` 与 `If-Match` 分别绑定父负载均衡的标识及归属版本。`listener_id` 与 `current_name` 来自当前凭据版本的配置快照，未命名时传空字符串。名称限制和独立审批与实例改名相同。同一父实例同时只能有一条进行中的操作申请。

| 产品 | 点查、更新与回执 |
| --- | --- |
| 腾讯云通用 CLB | `DescribeLoadBalancers`、`DescribeListeners`、`ModifyListener`、`DescribeTaskStatus`。 [监听器点查](https://cloud.tencent.com/document/api/214/30686) · [监听器改名](https://cloud.tencent.com/document/product/214/30681) |
| 阿里云 ALB | `GetListenerAttribute`、`UpdateListenerAttribute`、`ListAsynJobs`。 [监听器改名](https://www.alibabacloud.com/help/zh/slb/application-load-balancer/developer-reference/api-alb-2020-06-16-updatelistenerattribute) · [任务状态](https://www.alibabacloud.com/help/en/slb/application-load-balancer/developer-reference/api-alb-2020-06-16-listasynjobs) |
| 阿里云 NLB | `GetListenerAttribute`、`UpdateListenerAttribute`、`GetJobStatus`。 [监听器改名](https://www.alibabacloud.com/help/en/slb/network-load-balancer/developer-reference/api-nlb-2022-04-30-updatelistenerattribute) |
| 华为云 ELB V3 | `ListLoadBalancers`、`ShowListener`、`UpdateListener`。 [监听器点查](https://support.huaweicloud.com/api-elb/ShowListener.html) · [监听器改名](https://support.huaweicloud.com/api-elb/UpdateListener.html) |

Worker 在写入前校验批准记录、账号代次、父实例归属和云端旧名称。请求只修改名称。结果不明时走只读对账。云端名称确认后，平台在同一事务更新指定监听器的快照字段并写入成功事件。此前启动的刷新无法覆盖新名称。腾讯云传统型 CLB 和阿里云传统型 CLB 的监听器写入暂未开放。真实厂商沙箱仍待验收。

#### 负载均衡后端组改名

迁移 `0062_load_balancer_target_group_rename` 后重新刷新目标负载均衡的配置快照。云端关联清单确认后端组仅属于该实例时，快照返回 `exclusive: true`，控制台为该组提供“申请后端组改名”。API 为 `POST /v1/tenants/{tenant}/projects/{project}/load-balancers/rename-target-group`，受理后返回 `202` 和操作地址。需要账号写入模式、Worker 私有凭据配置 `"allow_load_balancer_target_group_rename": true` 及独立审批。

```sh
cloud-manager operations rename-load-balancer-target-group TENANT PROJECT --idempotency-key REQUEST_KEY --if-match 7 < rename-target-group.json
```

```json
{"account":"prod-cloud","region":"cn-hangzhou","asset_id":"nlb-example","target_group_id":"sgp-example","current_name":"group-old","name":"group-new"}
```

`If-Match` 使用父负载均衡归属版本。`current_name` 使用最近一次有效配置快照中的组名，名称规则与实例改名相同。共享组、独占证据缺失的组和传统负载均衡的默认、主备及虚拟组暂不可通过此入口修改。同一账号和地域下，同一个后端组只允许一条活动改名申请。

| 产品 | 点查、修改及任务证据 |
| --- | --- |
| 腾讯云通用 CLB | `DescribeLoadBalancers`、`DescribeTargetGroups`、`ModifyTargetGroupAttribute`。以组名、完整关联及父实例状态核对结果。 [目标组属性](https://cloud.tencent.com/document/product/214/40552) |
| 阿里云 ALB | `ListServerGroups`、`UpdateServerGroupAttribute`、`ListAsynJobs`。点查关联清单和组状态，核对任务关联的后端组。 [更新后端组](https://www.alibabacloud.com/help/en/slb/application-load-balancer/developer-reference/api-alb-2020-06-16-updateservergroupattribute) |
| 阿里云 NLB | `ListServerGroups`、`UpdateServerGroupAttribute`、`GetJobStatus`。点查关联清单和组状态，核对回执及任务状态。 [更新后端组](https://www.alibabacloud.com/help/en/slb/network-load-balancer/developer-reference/api-nlb-2022-04-30-updateservergroupattribute) |
| 华为云 ELB V3 | `ListLoadBalancers`、`ShowPool`、`UpdatePool`。核对所用云项目、后端组和父实例。 [查询后端组](https://support.huaweicloud.com/api-elb/ShowPool.html) · [更新后端组](https://support.huaweicloud.com/api-elb/UpdatePool.html) |

Worker 在发送前复核审批、归属版本及云端关联。只发送名称更新，回执丢失后进行只读对账。只有同一后端组仍独占关联且名称已确认时，平台才原子更新快照和成功事件，并阻止此前开始的刷新覆盖变更。厂商接口没有按父实例做条件更新，平台外创建关联仍可能发生在点查和发送之间。真实厂商沙箱仍待验收。

#### 调整后端成员权重

执行迁移 `0063_load_balancer_backend_keys` 和 `0064_load_balancer_backend_weight` 后，使用 `--include-backends` 刷新父负载均衡。独占后端组内具有固定端口、已知权重及可唯一定位的成员可提交申请。控制台显示“申请修改权重”，CLI 和 REST API 使用相同审批流程。云端写入默认关闭，启用需要账号处于写入模式，且 Worker 私有凭据配置为 `"allow_load_balancer_backend_weight": true`。

```sh
cloud-manager operations set-load-balancer-backend-weight TENANT PROJECT --idempotency-key REQUEST_KEY --if-match 7 < backend-weight.json
```

```json
{"account":"prod-cloud","region":"cn-hangzhou","asset_id":"nlb-example","target_group_id":"sgp-example","member_key":"19273a49ad02a19123e8f38429acbd80","current_weight":20,"weight":0}
```

API 为 `POST /v1/tenants/{tenant}/projects/{project}/load-balancers/backend-weight`。使用父负载均衡归属版本作为 `If-Match`。原权重和新权重都须填写，范围为 0 至 100 的整数。权重设为 0 可停止向该成员分发新的请求，存量连接由厂商负载均衡策略决定。共享组、传统默认或主备组、直接绑定成员和全端口绑定不走此入口。

| 产品 | 读取成员与执行 |
| --- | --- |
| 腾讯云通用 CLB | `DescribeTargetGroupInstances`、`ModifyTargetGroupInstancesWeight`、`DescribeTaskStatus`。在组内用地址和端口核对唯一目标。 [更新权重](https://cloud.tencent.com/document/api/214/40550) |
| 阿里云 ALB 与 NLB | `ListServerGroupServers`、`UpdateServerGroupServersAttribute`，ALB 使用 `ListAsynJobs`，NLB 使用 `GetJobStatus`。支持固定端口的 ECS、ENI、ECI 或 IP 成员。 [ALB 更新](https://www.alibabacloud.com/help/en/slb/application-load-balancer/developer-reference/api-alb-2020-06-16-updateservergroupserversattribute) · [NLB 更新](https://www.alibabacloud.com/help/en/slb/network-load-balancer/developer-reference/api-nlb-2022-04-30-updateservergroupserversattribute) |
| 华为云 ELB V3 | `ListMembers`、`UpdateMember`。目前为轮询和最少连接算法的独占组开放。 [更新成员](https://support.huaweicloud.com/api-elb/UpdateMember.html) |

Worker 根据组内点查结果用匿名标识确认成员，平台无需保存云端成员地址或原始 ID。发送前复核审批、归属代次、组的独占关联和当前权重。发送后用厂商状态和同一成员的实际权重确认结果。回执丢失后只读对账。确认成功与发件箱事件同事务保存，晚到的旧快照无法覆盖该权重。厂商的并发外部变更仍可能发生在预检与发送之间。真实厂商沙箱仍待验收。

#### 添加实例到后端组

执行 `cloud-manager migrate` 后，使用 `--include-backends` 刷新父负载均衡的配置快照。独占组支持从同项目、同账号和同地域选择一台已盘点实例。当前入口要求实例运行中、仅有一个可定位的私网 IPv4 地址，且与负载均衡和后端组在同一个 VPC。新成员初始权重固定为 0，可在验证后通过权重调整流程引流。写入默认关闭，启用需要账号写入模式、Worker 私有凭据中的 `"allow_load_balancer_backend_attach": true` 以及独立审批。

```sh
cloud-manager operations attach-load-balancer-backend TENANT PROJECT --idempotency-key REQUEST_KEY --if-match 7 < attach-backend.json
```

```json
{"account":"prod-cloud","region":"cn-hangzhou","asset_id":"nlb-example","target_group_id":"sgp-example","instance_id":"i-example","instance_revision":4,"port":8080}
```

API 为 `POST /v1/tenants/{tenant}/projects/{project}/load-balancers/attach-backend`。提交时绑定负载均衡的 `If-Match` 和源实例的 `instance_revision`。端口取 1–65535。控制台按项目、账号和地域核对实例后提交版本。活动绑定申请会阻止同组并行变更以及源实例删除或归属转移。升级时，`0069_load_balancer_change_reservations` 和 `0070_load_balancer_change_backfill` 保留进行中操作的资源预约，操作终结后释放预约。确认后，操作详情提供用于后续改权重或解绑的匿名成员标识。

| 产品 | 实例查证及绑定 |
| --- | --- |
| 腾讯云通用 CLB | CVM `DescribeInstances` 读取私网地址，CLB `RegisterTargetGroupInstances` 添加固定端口成员，`DescribeTaskStatus` 检查异步任务。 [注册目标组服务器](https://cloud.tencent.com/document/api/214/40549) |
| 阿里云 ALB/NLB | ECS `DescribeInstances` 核对实例 VPC 和私网地址，`AddServersToServerGroup` 添加 ECS 成员。ALB 使用 `ListAsynJobs`，NLB 使用 `GetJobStatus`。 [ALB 添加](https://www.alibabacloud.com/help/en/slb/application-load-balancer/developer-reference/api-alb-2020-06-16-addserverstoservergroup) · [NLB 添加](https://www.alibabacloud.com/help/en/slb/network-load-balancer/developer-reference/api-nlb-2022-04-30-addserverstoservergroup) |
| 华为云 ELB V3 | 点查 ECS 及网卡固定 IPv4 地址和子网，使用 `CreateMember` 添加成员。支持轮询或最少连接的同 VPC 后端组。 [创建后端服务器](https://support.huaweicloud.com/api-elb/CreateMember.html) |

Worker 复核独占关系、实例归属、VPC、私网地址和候选绑定是否已存在。发送前保存源端点摘要，平台无需持久化私网 IP 或原始成员 ID。一次发送后只读核查实际出现的单一目标及零权重，地址变化等不匹配情况转入复核。确认终态、快照及事件在同一事务提交，晚到的旧快照结果无法覆盖确认结果。真实厂商沙箱仍待验收。

#### 解绑已排空的后端成员

迁移 `0065_load_balancer_backend_detach` 后，可通过控制台、CLI 或 API 申请解绑单个成员。当前入口适用于腾讯云通用 CLB、阿里云 ALB/NLB 和华为云 ELB V3 的独占组固定端口成员，要求云端权重已为 0。请确认存量连接已按业务要求排空。解绑移除负载均衡成员的绑定，云服务器仍归所属项目管理。云端写入默认关闭，启用需要账号处于写入模式、Worker 配置 `"allow_load_balancer_backend_detach": true`，每个申请仍须独立审批。

```sh
cloud-manager operations detach-load-balancer-backend TENANT PROJECT --idempotency-key REQUEST_KEY --if-match 7 < detach-backend.json
```

```json
{"account":"prod-cloud","region":"cn-hangzhou","asset_id":"nlb-example","target_group_id":"sgp-example","member_key":"19273a49ad02a19123e8f38429acbd80","confirm_detach":true}
```

API 为 `POST /v1/tenants/{tenant}/projects/{project}/load-balancers/detach-backend`，持久受理返回 `202`。使用父负载均衡归属版本作为 `If-Match`，并使用带后端数据的配置快照中的成员标识。同一组的改名、改权重及解绑申请互斥。

| 产品 | 解绑及任务确认 |
| --- | --- |
| 腾讯云通用 CLB | `DeregisterTargetGroupInstances`，异步任务用 `DescribeTaskStatus` 查询。 [解绑目标组服务器](https://cloud.tencent.com/document/product/214/40557) |
| 阿里云 ALB 与 NLB | `RemoveServersFromServerGroup`，ALB 核对 `ListAsynJobs` 返回的任务，NLB 核对 `GetJobStatus`。 [ALB 解绑](https://www.alibabacloud.com/help/en/slb/application-load-balancer/developer-reference/api-alb-2020-06-16-removeserversfromservergroup) · [NLB 解绑](https://www.alibabacloud.com/help/en/slb/network-load-balancer/developer-reference/api-nlb-2022-04-30-removeserversfromservergroup) |
| 华为云 ELB V3 | 按 IAM 项目、组和成员调用 `DeleteMember`，通过同组完整成员列表确认解绑。 [删除后端服务器](https://support.huaweicloud.com/api-elb/DeleteMember.html) |

Worker 发送前复核审批、归属、独占关联、成员当前权重和唯一身份，并保存厂商实际绑定标识的摘要。结果未知时只读核对。错误、成员仍存在或异步任务未结束都会中断连续缺失证据。连续两次完整观测确认同一绑定已消失后，平台在同一事务清理该成员及能归属到它的健康记录、拦截此前启动的刷新并写入成功事件。真实厂商沙箱仍待验收。

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

完成迁移后，运维可在 `cmp_inventory_schedules` 中按云账号、地域和资源类型登记扫描计划。间隔范围为 60 秒至 7 天。全局 `region` 盘点使用空地域。普通盘点使用默认的空 `target_id`，节点池快照计划使用父集群 ID。

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

#### 前端开发

控制台使用 React、Vite、严格模式 TypeScript 和 Zod，源码位于 `cmd/cloud-manager/console/src`。

| 目录 | 职责 |
| --- | --- |
| `main.tsx` | 挂载 React 应用和载入基础样式 |
| `app/routes/` | `resources.ts`、`operations.ts`、`governance.ts` 和 `billing.ts` 分别声明业务页面、导航权限及列表加载器。`registry.ts` 负责页面标识与侧栏顺序，`project-page.tsx` 封装按需渲染 |
| `app/workspace/` | `AuthenticatedWorkspace.tsx` 装配工作区。会话与项目切换、列表导航及批次草稿的挂载分别由各自的模块管理 |
| `features/<业务>/` | 页面、表格、筛选、`api.ts`、`schema.ts` 和该业务的 `collection.ts`。复杂详情和表单按工作流分目录，业务专用样式随模块维护 |
| `features/session/` | 会话接口、当前项目和请求客户端。`login/` 管理登录页和专用样式。客户端向共用传输层提供 CSRF 信息 |
| `features/assets/` | 共用资产契约与资源专用操作。`inventory/` 管理清单页面、筛选和表格，`instances/` 管理实例选择、核对及实例行操作 |
| `features/catalog/` | 服务目录列表。`templates/` 管理模板发布、停用和历史版本，`launch/` 管理实例申请、收据及幂等重试 |
| `features/native-console/` | `grants/` 管理授权列表，`request/` 管理访问申请，`access/` 管理授权详情、审批、撤销及串口公钥发放 |
| `features/operations/` | `history/` 管理操作列表和筛选，`forms/` 管理变更请求与幂等键。`detail/review/` 管理审批、工单和人工复核，`detail/events/` 管理状态事件与分页，`detail/intents/` 按资源领域存放变更意图契约 |
| `features/batches/` | 页面和编辑流程装配。 `request/` 管理草稿表单、实例选择转换和输入契约，`preview/` 管理预览请求、结果契约和视图，`receipt/` 管理幂等提交、收据查询和结果视图 |
| `features/billing/reports/` | 账期与版本筛选、原币种费用、对账差额和归档摘要。账单、预算与分摊共用的查询字段在 `billing/schema.ts` |
| `features/billing/budgets/` | 月度预算查询、配置、版本校验和费用口径状态 |
| `features/billing/allocation/` | 历史分摊查询、按币种查看成本中心及版本追溯 |
| `shared/` | 同源传输、通用 Zod 契约、`collection/` 分页与筛选组件、`feedback/` 状态提示，以及 `detail/` 弹窗和表单生命周期。`display/` 提供日期格式和跨业务状态文案 |

新增列表时，在业务目录用 `schema.ts` 定义响应结构，由 `api.ts` 负责请求和校验，再在 `collection.ts` 用读取器创建类型化列表。页面通过 `useCollection` 订阅。页面、导航权限和列表加载器在 `app/routes/` 对应的业务模块声明，菜单位置在 `registry.ts` 登记。路由只向页面传递实际所需的属性。会话、批次和详情也在请求边界通过 Zod 校验，组件从对应契约推导类型。

应用层负责装配业务与共用层。页面、交互 Hook、请求和 Zod 契约放在对应工作流目录，页面组装视图，Hook 管理交互及请求生命周期，`api.ts` 调用共用客户端，`schema.ts` 定义运行时契约并推导类型。同一业务内多条工作流共用的契约放在业务根目录。负载均衡和快照摘要由资源目录定义契约并推导组件属性，清单组合这些契约。资源类型、操作名称、授权用途等文案由各自业务的 `labels.ts` 维护。

跨业务复用时，从能力所属模块直接导入 `scripts/feature-entrypoints.mjs` 登记的入口。应用装配可额外使用同一文件列出的应用入口。项目路由用显式字符串路径按需导入 `*Page.tsx` 和 `collection.ts`，批次页面在工作区按需装配。使用 `import type` 和 `export type` 声明纯类型依赖。`npm run check` 分别检查静态依赖、动态加载和类型引用，并报告静态运行时循环依赖。

新增资产行操作时，在资源目录定义 `row-actions.ts`，通过资产清单的 `inventory/asset-actions.ts` 按资源类型接入。业务组件负责字段和校验，通过 `OperationForm` 提交带资源版本和幂等键的变更。共用弹窗通过 React 渲染内容，通过浏览器原生对话框管理焦点。

页面和列表读取器按访问加载，操作、负载均衡、DNS 和 Kubernetes 详情按打开加载。离开列表时会取消进行中的列表请求，迟到的分包也不会为旧页面发起请求。批量操作在同一项目内保留草稿。项目切换和会话退出会清空当前选择和草稿，关闭旧详情并取消旧项目的列表、批次、详情和审计导出读请求。已经发出的写请求仍可能在服务端完成，结果仅在发起请求的弹窗仍打开时展示。留在同一项目重试结果不明确的批次时，控制台对同一表单内容复用幂等键。分页和审计导出按已提交的筛选条件请求。Kubernetes 分页中途发现快照更新时要求重新加载。

在 `cmd/cloud-manager/console` 目录使用 Node.js 22.12 或更新版本运行配置命令。

```bash
npm ci
npm run check
npm run build
```

`npm run build` 会生成供 Go 嵌入的 `_embedded` 产物及第三方许可证。Git 跟踪前端源码、构建配置和锁文件，`_embedded` 由本地或 CI 构建生成。生产访问 `/console`，使用同源会话和现有 CSP。入口脚本和样式禁用缓存。React 运行时、Zod 和业务代码按分包输出，带内容散列的分包使用不可变缓存。

使用 `npm run dev` 启动本地开发服务，访问 Vite 的 `/console/` 路径。可用 `CLOUD_MANAGER_DEV_API` 指向受控测试后端，代理仅转发项目 API 和会话接口。真实登录仍由受信 OIDC 网关完成，后端按配置验证请求源和 CSRF。

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

### 云账单归档与对账

财务运维在受信任的导入进程运行 `billing-import`。该命令使用 `CLOUD_MANAGER_DATABASE_URL`，一次发布一个归属项目、已验证账号、账期和来源的完整报表。随账单更新提交新版本时传入当前版本号，首次导入传 `0`。同一清单和分卷内容重复导入会返回原收据，分卷顺序可变，历史重放保留当前发布版本。一次写入最多 32 个 UTF-8 CSV 分卷、每卷 16 MiB、合计 64 MiB 和 250,000 条记录。分卷应来自同一账期的完整导出，且覆盖互不重叠的记录。

`native` 使用 JSON 清单把文件实际表头映射到账号、账期、币种和账单金额。下列示例使用假定表头。每行账号必须匹配已验证的远端账号 ID，账期列使用 `YYYY-MM`，费用列使用有符号十进制。没有币种列时，通过清单的 `currency` 明确指定已核实的原币种。可选列有 `effective`、`list`、`resource`、`service`、`region`、`category` 和 `tags`。华为云还必须通过 `columns.project` 提供可核验的 IAM 项目 ID，与已验证的 `账号 ID:项目 ID` 绑定一致。

```json
{
  "provider": "tencent-cvm",
  "columns": {
    "account": "计费账号ID",
    "period": "账期",
    "currency": "币种",
    "billed": "应付金额",
    "resource": "资源ID"
  },
  "expected_billed": { "CNY": "1234.56789012" }
}
```

`focus-1.0` 读取 `BillingAccountId`、`BillingCurrency`、`BillingPeriodStart`、`BillingPeriodEnd`、`ProviderName`、`BilledCost` 和 `EffectiveCost`。华为云项目范围还要求可核验的 `SubAccountId`。清单使用 `provider`、可选的 `offset_minutes` 与 `expected_billed`，其中 UTC+08:00 使用 `480`。开始和结束时间必须对应选定时区的完整自然月。阿里云 FOCUS 导出可用于成本分析，结算控制总额仍取对应账单。 [FOCUS 账期规范](https://focus.finops.org/docs/specification/v1-0/columns/cost-and-usage/billing-period-start/) · [阿里云 FOCUS 导出](https://help.aliyun.com/en/user-center/exporting-alibaba-cloud-focus1-0-preview) · [腾讯云账单字段](https://cloud.tencent.com/document/product/555/44144) · [华为云对账示例](https://support.huaweicloud.com/usermanual-billing/bills-topic_800000123.html)

```bash
cloud-manager billing-import tenant finance cloud-account 2026-11 native 0 manifest.json part-01.csv part-02.csv
cloud-manager billing-replay tenant finance cloud-account 2026-11 native 1
cloud-manager billing-source tenant finance cloud-account 2026-11 native 1 1 > verified-part.csv
cloud-manager billing get tenant finance cloud-account 2026-11 native
```

收据包括报表版本、原件编号、SHA-256 和记录数。重放读取原件，以归档清单重新解析并核对明细与汇总。查询通过 `GET /v1/tenants/:tenant/projects/:project/billing/reports`、`billing get` 或控制台「云账单」读取。归属项目管理员可按账号、账期、来源和可选的历史版本查询，账号共享仅授予已有资源访问权限。费用和差额以十进制字符串返回，按原币种汇总。摊销成本或目录价在某币种的任一记录缺失该口径时显示为未提供。提供 `expected_billed` 时，差额为账单金额减该币种控制总额。两种账单来源各自独立发布和查询。

#### 订阅账单自动拉取

先在费用中心将目标账单订阅到对应对象存储，再应用迁移 `0096_billing_pull`。腾讯云读 COS，阿里云读 OSS，华为云读 OBS。通过 `CLOUD_MANAGER_BILLING_PULL_PROFILES` 指定权限为 `0600` 的私有 JSON 配置文件，内容是一个任务数组。每项明确租户、归属项目、已校验账号、账期、账单来源、对象键和解析清单。拉取进程使用独立的对象读取凭据。厂商凭据授权限定到目标对象或前缀。 [腾讯云账单存储](https://cloud.tencent.com/document/product/555/61275) · [阿里云 FOCUS 订阅](https://help.aliyun.com/en/user-center/exporting-alibaba-cloud-focus1-0-preview) · [华为云消费数据存储](https://support.huaweicloud.com/intl/zh-cn/usermanual-billing/bills_topic_1000015.html)

```json
[
  {
    "tenant": "tenant",
    "project": "finance",
    "account": "cloud-account",
    "period": "2026-11",
    "source": "native",
    "generation": 1,
    "interval_seconds": 900,
    "region": "ap-guangzhou",
    "bucket": "finance-1250000000",
    "credentials": {
      "access_key_id": "<只读密钥 ID>",
      "access_key_secret": "<只读密钥>"
    },
    "objects": [
      {"key":"billing/2026-11/export.zip","format":"zip","entries":["detail-1.csv","detail-2.csv"]}
    ],
    "manifest": {
      "provider": "tencent-cvm",
      "currency": "CNY",
      "columns": {"account":"计费账号ID","period":"账期","billed":"应付金额"}
    }
  }
]
```

`objects` 支持 `csv`、单成员 `gzip` 和显式 CSV 成员列表的 `zip`，可选 `sha256` 固定整个对象的摘要。多对象配置应引用同一个已完成批次的不可变键。对象键和 ZIP 内部路径采用原始名称。任务直接读取配置中的对象键，供应商生成新路径或换月时更新配置后重启拉取进程。改变同一任务的对象、解析规则或调度间隔时递增 `generation`，凭据轮换可以沿用任务代次。每次最多读取 64 MiB 厂商原件，展开后的 CSV 延用账本分卷、大小和行数上限。

```bash
CLOUD_MANAGER_BILLING_PULL_PROFILES=/run/secrets/cloud-manager/billing.json cloud-manager billing-pull-controller
cloud-manager billing-pull tenant finance cloud-account 2026-11 native
cloud-manager billing-pull-status tenant finance cloud-account 2026-11 native
cloud-manager billing-pull-source tenant finance cloud-account 2026-11 native OBSERVATION_SHA256 1 > original.zip
cloud-manager billing-pull-replay tenant finance cloud-account 2026-11 native OBSERVATION_SHA256
```

独立控制器逐个领取到期任务，租约、失败状态和下次执行时间保存在 PostgreSQL。一次拉取先读对象元数据，下载使用对应 ETag 条件，发布前复核所有对象。重复观察到同一批次时只读元数据。获取失败、对象改版或分卷不完整会留下失败状态，已发布账本保持原版本。新账单、原件及拉取状态在同一事务发布。状态命令返回观察摘要及关联账单版本，原件按对象键排序编号。重放重新解压归档原件、校验摘要，并检查选中分卷生成的账本明细与汇总。

### 账单费用分摊

财务运维使用 `billing-allocate` 发布某个账单版本的分摊，首次发布的预期分摊版本为 `0`。后续规则变更使用当前分摊版本提交。相同规则重复发布时返回原收据，历史收据保留原版本。归属项目管理员可通过 `GET /v1/tenants/:tenant/projects/:project/billing/allocations`、`billing allocation-get` 和控制台「费用分摊」读取结果。默认查询当前账单的分摊。若当前账单已有修订，查询历史结果时显式传入对应账单版本。

```json
{
  "rules": [
    {
      "service": "compute",
      "tags": { "environment": "production" },
      "shares": [
        { "center": "product", "weight": 7500 },
        { "center": "platform", "weight": 2500 }
      ]
    }
  ]
}
```

规则按声明顺序匹配，首条匹配生效。可组合 `resource`、`service`、`region`、`category` 和 `tags` 做精确匹配。同一规则的各条件须同时满足。标签列应为 JSON 对象，匹配区分字符串、布尔值、有值为 `null` 的键和缺失键。标签格式不可识别时继续尝试其他规则，并记录已尝试解析且失败的行数。未匹配记录按 `default` 分配，省略时归入未归属成本中心。每组权重合计 10,000 基点。单次最多 64 条规则、32 个成本中心，每组最多 8 个分摊对象。 [FOCUS 1.0 标签定义](https://focus.finops.org/docs/specification/v1-0/columns/cost-and-usage/tags/)

```bash
cloud-manager billing-allocate tenant finance cloud-account 2026-11 native 1 0 allocation.json
cloud-manager billing allocation-get tenant finance cloud-account 2026-11 native 1 1
cloud-manager billing-allocation-replay tenant finance cloud-account 2026-11 native 1 1
```

按匹配规则和原币种先汇总，再按权重分摊。金额保留 12 位小数。余数按比例余量从大到小分配，余量相同时按成本中心名排序。退款保留负号，各币种合计与原账单一致。某成本中心涉及的记录缺少摊销成本或目录价时，对应总额显示为未提供。目录价可用时同时显示目录价减账单金额。参与行数可在多个成本中心重复出现，账单总行数单独显示。

收据保存账单 SHA-256、规则 SHA-256 和算法版本。重放使用归档明细与规则核对所有成本中心和金额，并把保存的规则随复核结果输出给本地运维。可用 `billing-replay` 独立核对厂商原件和账本投影。

### 月度云预算

账号归属项目的管理员通过「云预算」、CLI 或 `/v1/tenants/:tenant/projects/:project/billing/budget` 管理预算。用账号 `account`、月份 `period`、来源 `source`、原币种 `currency` 和成本口径 `basis` 定位预算。分别用 `billed` 和 `effective` 对账单金额与摊销成本设额度。GET 读配置，POST 创建，PUT 用当前预算 `If-Match` 更新。额度使用正十进制字符串，预警百分比为 1 至 99，`enabled` 明确控制启用或停用。

```json
{"limit":"10000.00","warning_percent":80,"enabled":true}
```

```bash
cloud-manager billing budget-create tenant finance cloud-account 2026-11 native CNY billed < budget.json
cloud-manager billing budget-get tenant finance cloud-account 2026-11 native CNY billed
cloud-manager billing budget-update tenant finance cloud-account 2026-11 native CNY billed 1 < budget.json
```

系统使用该作用域的最新已发布账单，以十进制计算累计费用。达到预警比例或额度时生成当前最高级别告警。重复发布同一报表保留已有告警。账单修正使费用回落时恢复旧告警，费用口径暂不可用时保留已有告警。停用预算、停用账号或转移账号归属会恢复原作用域的预算告警。账单发布与对应告警、审计及待投递事件在同一事务提交。

预算告警在「项目告警」中仅向该项目管理员显示，详情列出账期、原币种、来源和成本口径，确认操作沿用告警版本。订阅项目告警的 Webhook 会收到开启和恢复事件，载荷包含告警级别、账号与预算标识。预算金额和累计费用通过预算查询读取。配置变更可在「项目审计」使用「云预算」类型检索。

从旧版本滚动升级时，先完成数据库迁移和所有 API 副本的更新，再创建预算。预算告警的角色过滤由新版 API 提供，预算恢复判定也在数据库中执行，旧告警控制器仍可处理原有规则。

### 资源标签合规

应用 `0097_tag_observations` 和 `0098_tag_policies` 后，重新盘点对应资产。项目成员可以在标签合规页查看规则和结果，管理员可以发布规则。规则按项目保存不可变历史。每类资源配置一组必填标签键，键区分大小写，对应值须含非空白字符。每类最多 24 个键，规则请求体最多 128 KiB。证据有效期为 60 至 604800 秒。发布空规则数组可停用当前项目的评估。

盘点事务只保存标签键、观测时间及凭据版本。报告区分合规、缺少标签和证据不足。厂商未返回标签、资产待确认缺失、账号停用、凭据轮换或观测过期都可能产生证据不足。明确返回的空标签集可以判定缺少标签。报告复用资产归属与账号共享规则，只显示当前项目可见资源。标签值不进入报告及治理存储。

使用现有的 `CLOUD_MANAGER_API_URL` 和 `CLOUD_MANAGER_API_TOKEN` 配置。示例规则要求实例具备 `team` 和 `environment` 标签。

```sh
cloud-manager governance policy-create TENANT PROJECT <<'JSON'
{"max_age_seconds":3600,"rules":[{"kind":"instance","required_keys":["team","environment"]}]}
JSON
cloud-manager governance policy-get TENANT PROJECT
cloud-manager governance policy-get TENANT PROJECT 1
cloud-manager governance policy-update TENANT PROJECT 1 < policy.json
cloud-manager governance compliance TENANT PROJECT instance --state noncompliant --limit 50
```

在项目 API 下通过 `GET /governance/tag-policy` 读取，使用 `POST` 发布首版，使用带强 `If-Match` 的 `PUT` 更新。版本和审计事件同事务提交。通过 `GET /governance/tag-compliance?kind=instance` 查询，支持 `account`、`region`、`state`、`limit` 和 `cursor`。分页游标绑定筛选项及当前规则版本，换版后从第一页重新查询。

### 实例闲置分析

应用 `0099_idle_schedules` 和 `0100_instance_idle` 并升级所有盘点、工作负载和告警控制器。完成腾讯云 CVM、阿里云 ECS 或华为云 ECS 的初次盘点后，可给指定实例登记监控采集计划。独立的 `idle-controller` 进程读取 `CLOUD_MANAGER_IDLE_PROFILES` 指定的私有 JSON 文件。文件格式为凭据引用到私有账号配置文件路径的映射，文件权限与盘点控制器相同。凭据需要能校验账号身份并读取目标地域的实例监控数据。

```sh
cloud-manager idle-schedule TENANT ACCOUNT REGION INSTANCE 24h
CLOUD_MANAGER_IDLE_PROFILES=/run/secrets/cloud-manager/idle-profiles.json cloud-manager idle-controller
cloud-manager governance idle TENANT PROJECT --state candidate --limit 50
cloud-manager idle-schedule TENANT ACCOUNT REGION INSTANCE off
```

采集间隔为 1 至 24 小时。每个采集进程同一时间处理一项计划，采集超时为 90 秒，租约为 2 分钟。限流按厂商提供的重试时间退避。进程停机释放尚未完成的租约。停用计划阻止旧领取发布；已有证据仍按新鲜度规则展示。

分析窗口为结束于上一完整 UTC 小时边界的七天时段，预留至少一小时给厂商上报。腾讯云采用主机基础 CPU 与内外网双向小时最大值。阿里云通过 ECS 实例监控读取十分钟样本，将双向内外网流量换算为字节每秒。华为云使用 CES 的 ECS 小时最大值。窗口中任何已知 CPU 样本超过 5%，或任何已知网络样本超过 64 KiB/s，结果为「观察到活动」。只有 CPU 和网络样本全部存在且均未超过阈值，才列为「低活动候选」。未采样、过期盘点、归属或凭据变化以及数据有缺口时显示「证据不足」。本地确认未运行的实例单独显示。

结果用于人工核查。腾讯云公网指标覆盖厂商提供的 IPv4 统计，阿里云的网络速率是十分钟区间平均值。查询同时返回厂商、窗口、采样间隔、样本覆盖、峰值和规范采样摘要。只保存每个实例最新采集窗口的摘要及其校验指纹。采集失败保留上一份证据，窗口结束超过 30 小时后停止用于判断。

项目成员可使用「实例闲置分析」控制台、CLI，或调用 `GET /v1/tenants/{tenant}/projects/{project}/governance/idle-instances`。列表复用当前资产可见性，支持账号、地域、结果状态和游标筛选。结果不会触发云端停机或回收。

腾讯云与阿里云均限制监控接口调用量，腾讯云超出免费额度时需要开通计费。各厂商的真实凭据、地域覆盖、指标口径和调用预算仍需针对目标环境验收。[腾讯云监控 API](https://cloud.tencent.com/document/api/248/31014) · [腾讯云 CVM 指标](https://cloud.tencent.com/document/product/248/6843//) · [阿里云实例监控](https://help.aliyun.com/en/ecs/developer-reference/api-ecs-2014-05-26-describeinstancemonitordata) · [华为云 CES API](https://support.huaweicloud.com/intl/zh-cn/api-ces/ces_03_0034.html) · [华为云 ECS 基础指标](https://support.huaweicloud.com/intl/zh-cn/usermanual-ecs/ecs_03_1002.html)

### 外部变更工单

应用 `0101_change_tickets` 和 `0102_change_ticket_events`，升级 API、Worker 和事件发布器后再启用项目策略。为工单桥接器分配项目内仅有 `viewer` 角色的独立服务主体。桥接器持有该主体的服务凭据，用于向平台报告外部工单结果。

项目管理员可在控制台的「工单策略」页发布、修改或停用规则，也可调用 `/v1/tenants/{tenant}/projects/{project}/governance/ticket-policy`。首版使用 POST，后续使用 PUT 并携带当前强 `If-Match`。GET 可选 `revision` 读历史版本。策略的 `actions` 使用操作记录中的动作名，例如 `start`、`create` 和 `scale-workload`。单独的 `*` 覆盖全部操作，更新为 `{"actions":[]}` 停用新操作的工单要求，历史操作始终使用受理时固定的策略。

```sh
printf '%s\n' '{"actions":["start","scale-workload"],"verifier":"itsm-bridge"}' \
  | go run ./cmd/cloud-manager governance ticket-policy-create TENANT PROJECT
go run ./cmd/cloud-manager governance ticket-policy-get TENANT PROJECT
go run ./cmd/cloud-manager governance ticket-get TENANT PROJECT OPERATION
```

在项目 Webhook 接收端 JSON 配置中设置 `"include_change_tickets": true`，重新执行 `event-register` 后即可接收新申请的签名事件 `change.ticket.requested`。订阅登记从后续事件生效。桥接器使用 `operation_id` 关联外部工单，按事件 `id` 去重，并校验签名、时间戳及正文的租户和项目。事件包含动作、资源类型、账号、地域、固定策略版本及可用的资源 ID。投递使用现有事务发件箱、退避、失败查询和人工重投。

外部系统核验工单后，桥接器以指定服务主体向 `POST /v1/tenants/{tenant}/projects/{project}/governance/change-tickets/{operation}` 登记结果。也可运行 `go run ./cmd/cloud-manager governance ticket-record TENANT PROJECT OPERATION < evidence.json`。正文包含不透明的 `ticket_id`、递增的十进制字符串 `source_revision`，以及 `decision`。有效的 `approved` 必须携带 24 小时内的 RFC 3339 `valid_until`，`revoked` 省略该字段。同一项目的工单 ID 只绑定一次操作。当前回执完全相同的重放返回已有状态，旧版本、同版本变更及更换工单 ID 返回 409。

项目成员可用 GET 查询固定策略版本、最新证据和当前有效性。操作详情在外部批准有效后开放「批准」，独立审批人可在等待期间拒绝申请。外部批准失效时尝试平台批准返回 409，错误详情的 `reason` 为 `change_ticket_required`。Worker 在首次发送及 AWS 创建同键重放前复核有效期、最新回执和核验主体代次。批准撤回或失效后，已发送的请求继续只读对账，仍未发送的排队操作以 `dispatch_unavailable` 终结。恢复核验主体的权限后需要新的回执版本才能重新满足闸门。

工单策略发布和工单回执与项目审计同事务提交。可用 `category=change_ticket` 筛选审计。企业特定工单系统的工作流映射、凭据轮换和生产投递由目标环境验收。

### CMDB 资产同步

CMDB 同步使用来源 API 的项目快照和独立 PostgreSQL 镜像。准备具有目标项目 `viewer` 权限的服务主体，将 `CLOUD_MANAGER_API_URL` 设为来源 API 的 HTTPS 地址，通过 `CLOUD_MANAGER_API_TOKEN` 提供服务凭据，并用 `CLOUD_MANAGER_CMDB_DATABASE_URL` 指向镜像库。

```sh
go run ./cmd/cloud-manager cmdb-migrate
go run ./cmd/cloud-manager cmdb-sync TENANT PROJECT
go run ./cmd/cloud-manager cmdb-status TENANT PROJECT
```

`cmdb-sync` 可以作为调度器中的单次任务运行。每次从 `GET /v1/tenants/{tenant}/projects/{project}/governance/cmdb-snapshot` 读取当前项目的完整清单。接口只接受服务凭据，每个 API 副本最多同时读取两个快照，读取预算为 25 秒。繁忙时返回 429 和 `Retry-After`。CLI 单次运行的预算为 45 秒。

响应媒体类型为 `application/x-ndjson`。首行 `manifest` 含租户、项目、`schema_version=1` 和数据库快照时间。之后每行 `asset` 含资产稳定坐标、厂商、名称、状态、归属版本及项目可见的拓扑边。凭据、网络地址和原始标签留在来源系统。末行 `complete` 含资产条数和此前所有行原始字节的 SHA-256，哈希包含每行的换行符。单次快照上限为 25 万条资产、256 MiB，每行上限为 256 KiB。服务在写末行前复核服务凭据和成员代次。HTTP 200 后断开或缺少完整末行时，接收端丢弃该轮快照。

镜像以来源 URL、租户、项目隔离，资产以账号、地域、类型和资产 ID 去重。接收端校验边界、条数和哈希，再在同一事务中更新变化行、移除已不可见或确认移除的行，并保存发布回执。`missing_candidate` 继续在镜像中保留。截断、校验失败、超时、来源授权失败和发布事务失败时，最近一次已发布数据仍可用。旧领取代次或早于当前发布的来源快照无法覆盖已有版本。

消费端从 `cmp_cmdb_assets` 按 `source_url`、`tenant_id` 和 `project_id` 读取 JSONB 资产投影，也可以读取 `cmdb-status` 返回的发布代次、来源时间、变更和移除数。镜像库由接收端管理，消费进程应使用与其来源、租户和项目范围对应的数据库授权。更换来源 URL 建立新的镜像范围，现有镜像可继续追溯。

### 项目审计与导出

项目管理员可通过浏览器的「项目审计」、CLI 或 `GET /v1/tenants/{tenant}/projects/{project}/audit` 查询审计账本。记录涵盖云账号变更、资产归属、操作状态、审批、人工处置、创建续查和服务目录变更。归属转移或账号共享的双方项目各有一份记录。操作申请记载申请人，审批和处置记载实际审批人，系统状态事件留空主体字段。

可按 `category`、`account`、`subject_id` 和 RFC 3339 时间范围筛选。类别为 `account`、`ownership`、`operation`、`approval`、`resolution`、`observation`、`catalog`、`alert`、`console_access`、`billing_budget`、`tag_policy` 和 `change_ticket`。时间区间含 `from`，截止于 `until` 之前。列表按发生时间和审计 ID 倒序返回，`limit` 默认 50，范围为 1 至 200。续读使用 `next` 及原筛选条件。操作和工单回执记录的 `subject_id` 可直接查询项目操作详情，目录、告警和原生控制台记录分别使用模板 ID、告警 ID 与访问授权 ID。标签规则和工单策略发布记录使用项目 ID 作为对象，并包含发布版本。

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

### 腾讯云 CVM 单实例退役

退役释放按量付费 CVM 及其系统盘。先备份需保留的数据、停止实例并卸载数据盘。执行迁移 `0106_instance_retirement_reservations` 和 `0107_instance_retirement` 后升级 API 与 Worker。账号须处于可写状态，目标账号的 Worker 凭据配置需单独开启 `allow_instance_retirement`，默认关闭。保盘删除与批量操作沿用各自的现有合同。[腾讯云退还实例](https://cloud.tencent.com/document/api/213/15723) · [腾讯云实例销毁影响](https://cloud.tencent.com/document/product/213/4930)

控制台在符合本地关机条件的腾讯云实例行提供「申请退役」。预览读取当前实例版本及唯一已盘点的关联盘，Worker 在发送前确认它是系统盘。申请人填写完整实例 ID 确认影响。HTTP 预览路径为 `GET /v1/tenants/{tenant}/projects/{project}/instance-operations/retirement-preview`，查询参数为 `account`、`region` 和 `asset_id`。提交将末尾的 `retirement-preview` 改为 `retire` 并使用 `POST`，`If-Match` 使用预览中的实例版本。

```bash
cloud-manager operations preview-retire tenant-id project-id \
  --account tencent-account --region ap-guangzhou --asset-id ins-example
cloud-manager operations retire-instance tenant-id project-id \
  --if-match 1 --idempotency-key retire-example <<'JSON'
{"account":"tencent-account","region":"ap-guangzhou","asset_id":"ins-example","system_disk_id":"disk-example","confirm_asset_id":"ins-example"}
JSON
```

受理固定实例、盘点系统盘及各自的归属版本。活动操作及待复核状态预约实例和系统盘，拦截归属转移以及冲突的挂盘和快照写入。单次发送前复核平台保护、当前授权、独立审批及适用的外部工单。Worker 使用同账号同地域的 CVM 和 CBS 精确查询确认关机、按小时计费、销毁保护明确关闭且只挂载同一块云系统盘。确认后发送单实例 `TerminateInstances`，并显式保留包年包月数据盘。[实例点查](https://cloud.tencent.com/document/api/213/15728) · [CBS 磁盘点查](https://cloud.tencent.com/document/product/362/16315)

接收厂商回执后，连续两轮查询的 `DescribeInstances` 与 `DescribeInstancesStatus` 都须返回完整空集，才确认实例释放。回收态、销毁中、点查错误及部分结果会打断连续观测。确认事务记录实例移除、实例配额释放、操作终态、审计和发件箱。系统盘仍通过云盘盘点确认。若厂商回执丢失，操作进入待复核，审批人通过厂商记录核验后登记处置；已有回执时可恢复只读观测。[实例状态查询](https://cloud.tencent.com/document/api/213/15738) · [实例生命周期](https://intl.cloud.tencent.com/zh/document/product/213/4856)

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

### 自有 KVM 控制台

项目操作者和管理员可从虚拟机清单进入“创建 KVM 虚拟机”。选择宿主机、网络、容量与启动来源，预留后核对到期时间和站点分配的地址，再提交独立审批。未提交的预留可释放。浏览器在同一标签页按租户、项目及身份保留当前申请，刷新后可重试原预留或找回已提交的 VM。

从控制台左侧打开“KVM 运维”。虚拟机列表显示站点、当前宿主机、状态和已分配的 IPv4。宿主机列表显示故障域、隔离或排空状态及最近一次容量上报时间。两个列表都可用站点 ID 筛选，点击资源 ID 可以查看详细配置和最近观测。

项目操作者和管理员可为运行中的虚拟机申请优雅关机，管理员可为已关机的虚拟机申请退役。审批角色可处理待审批的创建、关机和退役申请，也可在宿主机代理领取前拒绝已经排队的任务。提交使用当前虚拟机版本和控制台会话的 CSRF 凭据。遇到版本变化时，在详情中刷新并重新确认。关机和退役的实际状态随宿主机代理的观测更新。

在虚拟机详情打开“实时迁移”可查看按申请时间排序的历史与各阶段回执。管理员可从同站点候选宿主机中选择目标，独立审批人可批准，或在派发前拒绝。派发后需要终止时，回执会核对两端宿主机的最新隔离状态和代次，再由审批人提交现场证据摘要。详见下文的实时迁移前提和现场证据要求。

虚拟机详情的“冷迁移与单机疏散”按源状态提供申请入口。项目成员可分页查历史和目标预留。管理员核对备份与现场证据后提交，独立审批人可批准或拒绝。回到虚拟机详情可核对新的宿主机和目标任务状态。

在宿主机详情打开“宿主机隔离与重新接入”可查看隔离事件和分代次的阻塞清单。管理员可以停止或恢复新放置、申请隔离及重新接入。独立审批人可确认带外隔离、登记疏散源端清理证据，以及批准或拒绝重新接入。清单可打开相关虚拟机、现场恢复记录或实时迁移回执。

虚拟机详情中的“保留卷与清理历史”按原放置显示退役卷、迁移源卷及清理回执。宿主机详情和隔离工作台可进入进行中的保留卷清理列表。

API 提供 `GET /v1/tenants/{tenant}/projects/{project}/kvm/hosts` 和 `GET /v1/tenants/{tenant}/projects/{project}/kvm/vms`。两者需要项目查看权限，按站点和资源 ID 排序，默认 50 条且最多 100 条。可传 `site` 限定站点。存在后续记录时，将响应 `data.next` 原样传给同一列表的 `after`，并保留原站点筛选。虚拟机清单使用稳定的 VM ID，放置迁移后仍显示该 VM 的当前宿主机。

### 自有 KVM 站点与宿主机放置

执行 `cloud-manager migrate`，待 `0111_kvm_placement` 完成后启动 API。宿主机按租户、项目和站点绑定。每台物理机的 libvirt UUID 全局唯一，每个代理服务主体在租户内只绑定一台宿主机。代理在该项目中使用独立的 `viewer` 服务身份，使用短期 Bearer 凭据通过 HTTPS 上报。

```bash
cloud-manager kvm register-host TENANT PROJECT SITE < host.json
cloud-manager kvm get-host TENANT PROJECT SITE HOST
cloud-manager kvm change-host TENANT PROJECT SITE HOST REVISION < change.json
```

```json
{"id":"host-01","uuid":"feaa8a19-4765-4bb4-a383-3a1011ba0001","agent":"host-01-agent","failure_domain":"rack-a","networks":["tenant-net"],"storage_pool":"vm-local"}
```

登记和排空需要项目管理员。变更主体接受 `{"action":"drain"}`、`{"action":"resume"}` 或 `{"action":"rebind","agent":"host-01-next"}`。CLI 将当前管理版本送入 `If-Match`。换绑提升代理代次并进入排空，恢复后须有新上报才能再次放置。换绑后撤销旧服务身份或令牌。已撤销项目成员权限的代理立即失去上报和放置资格。

在受控的 Linux KVM 宿主机上使用 `observe-host`。为运行账号授予 libvirt 只读权限，并配置 `CLOUD_MANAGER_API_URL` 与代理自己的 `CLOUD_MANAGER_API_TOKEN`。预先核对宿主机 UUID、活动 libvirt 网络名称、专用本地存储目录和分配上限。代理仅读取本地 `qemu:///system`，以 NUMA 内存、在线逻辑 CPU 及目录卷的实时可用空间限制上报值。目录池支持 ext 系列、XFS 和 Btrfs。磁盘池须归宿主机独占，系统与其他项目的预留由运维留在配置上限之外。[virsh](https://www.libvirt.org/manpages/virsh.html) · [libvirt 能力 XML](https://libvirt.org/formatcaps.html)

非空宿主机通过 `POST /v1/tenants/{tenant}/projects/{project}/kvm/sites/{site}/hosts/{host}/vm-inventory` 一次核对最多 256 个域 UUID。只有当前宿主机绑定的服务身份可查到本站、同项目且仍有持久预留的域。代理最多并行核验四个域，核对持久配置和运行中配置中的归属、vCPU、内存、启动卷及网络。域清单前后必须一致。发现未知域、配置漂移、对账失败或所需网络未激活时，本轮上报零可放置容量。采样开始时无法验证宿主机身份或无法提交报告时，平台依照最近一次报告的有效期判定。站点应维持对 libvirt 和卷池的独占管理。

域全部核验通过后，仍可使用宿主机剩余容量。平台在宿主机锁下扣除所有代次的未释放有效预留，已绑定虚拟机也继续占用。磁盘以配置上限和文件系统实时可用量中较小者作为预算，再扣除预留，避免在稀疏卷或共享区块上提前计入空间。

```bash
cloud-manager kvm observe-host TENANT PROJECT SITE HOST 1 < limits.json
```

```json
{"vcpu":24,"memory_mib":65536,"disk_mib":512000}
```

先升级 API 副本，再更新内置站点代理。按 30 秒采样并串行运行同一宿主机的采样进程。90 秒内没有新报告会失去放置资格。重复提交同代次、同序号、同容量且证据有效的报告返回原观测时间。内置采样器会上报 `inventory.host_revision` 和 `inventory.domains`，平台在宿主机锁下复核版本及已核验域是否仍有有效记录。代次、序号、版本或域证据过期返回 412。受信外部站点代理可以通过 `report-capacity HOST < report.json` 提交 `uuid`、`generation`、`sequence` 和 `capacity`，也可附带 `inventory`。内置采样器提供 CPU、内存和本地磁盘。使用 GPU 放置需登记 `gpu_model`，并由受信外部代理上报 GPU 数量。

```bash
cloud-manager kvm reserve TENANT PROJECT SITE request-042 < placement.json
cloud-manager kvm get TENANT PROJECT SITE PLACEMENT
cloud-manager kvm release TENANT PROJECT SITE PLACEMENT
```

```json
{"vcpu":2,"memory_mib":4096,"disk_mib":20480,"network":"tenant-net","storage_pool":"vm-local","excluded_failure_domain":"rack-b","ttl_seconds":300}
```

放置需要项目执行权限，匹配网络、存储池、剩余容量，以及可选的 `required_host`、`failure_domain`、`excluded_failure_domain` 和 GPU 型号。预留有效期为 1–900 秒，无合适容量返回 409。同一请求人、项目、站点用相同幂等键和输入重试时返回原记录，包括过期或已释放的状态。原请求人和项目管理员可以主动释放。登记、主机管理变更、预留和释放与 `cmp_kvm_events` 在同一事务提交。严格 OpenAPI 见 `/openapi.json` 下的 `/kvm/sites/` 路径。

#### 宿主机故障隔离与重新接入

应用迁移 `0123_kvm_host_fence`，升级处理 KVM 请求的 API 副本及宿主机代理。宿主机疑似失联或已有命令结果未知时，管理员向宿主机路径下的 `/fence` 提交 `action=request` 和物理宿主机 UUID。所有隔离变更需要当前宿主机版本 `If-Match`。申请会进入 `pending`，推进代理代次、清空容量样本并阻止放置、代理上报、DHCP 配置和 VM 派发。现有预留和未决 VM 状态继续保留。

站点人员通过带外手段隔离原宿主机，保存可核验的证据原件。已经发到宿主机的命令仍可能完成。独立审批人核对物理身份和现场证据后提交 `action=confirm`、`uuid` 与 `evidence_sha256`，将状态推进为 `fenced`，供后续故障处置核对。项目成员可从 `GET /fence` 查询当前状态及最近 20 条事件。

项目成员可用 `GET /fence/work` 查看阻塞重新接入的虚拟机。清单包含宿主机 UUID、版本和隔离代次，以及各任务的 ID、版本、状态和适用的待审批恢复 ID。按虚拟机 ID 分页，`limit` 默认 50 条、最多 100 条。用 `next` 续读同一宿主机的当前代次，游标所属宿主机或代次不符时返回 412。分页读取实时状态，任务处理完毕后从首页复查。对仍在 `awaiting_approval` 或 `queued` 的创建任务，独立审批人可沿用原虚拟机审批入口拒绝并释放预留。

对已有写入可能的任务，执行迁移 `0124_kvm_fenced_vm_recovery`。宿主机处于 `fenced` 时，项目管理员向站点内的 `/vms/{vm}/fence-recovery` 提交 `recovery_id`、`host_uuid`、`fence_generation`、`observed`、`evidence_sha256`，并通过强 `If-Match` 固定该任务版本。现场确认域缺失时还需提供另一份独立证据的 `second_evidence_sha256`。独立审批人用同一版本向 `/fence-recovery/review` 提交恢复 ID 和 `approve` 或 `reject`。成员可用 GET 查看最新申请与审批结果。同一申请人用原版本、原 ID 和相同正文重试时，返回原记录。

| 任务阶段 | 可接受的现场观测与审批结果 |
| --- | --- |
| `staging`、`staged` | `absent` → `rejected` |
| `defining`、`defined` | `defined` → `stopped`，`absent` → `rejected` |
| `starting` | `running` → `running`，`defined` → `stopped`，`absent` → `rejected` |
| `stopping` | `defined` → `stopped`，`running` → `running` |
| `undefining` | `defined` → `stopped`，`absent` → `retired` |

恢复审批记录现场观测及对应结果。确认缺失进入 `rejected` 或 `retired` 时，事务归还容量和 IPv4 地址预留，继续保留启动卷占用。仍有持久域时保留原预留。若已进入 `rejoin_pending`，需先拒绝该次重新接入申请，回到 `fenced` 再批准虚拟机恢复。

在阻塞项旁打开“查看恢复与现场证据”。控制台依据当前任务阶段列出可接受的观测，确认缺失时要求填写两份不同的证据摘要。同一表单以相同观测和摘要重试时沿用原恢复 ID。独立审批人可核对摘要和申请版本后审批，随后返回宿主机清单确认阻塞项是否移除。

宿主机准备重新接入时，管理员提交 `action=request-rejoin`、`uuid`、`replacement_agent` 与新的 `evidence_sha256`。独立审批人以 `approve-rejoin` 或 `reject-rejoin` 审批该版本。仍有未收敛的创建、写入或观测任务时，平台保留隔离。批准后绑定新的代理服务主体并推进代次，宿主机保持排空。管理员执行原来的 `resume` 并完成新容量上报后，宿主机才能接收新放置。

控制台核对隔离记录和阻塞清单的宿主机 UUID、版本和代次。虚拟机阻塞项、未结束的实时迁移及已派发的保留卷清理仍在时，批准重新接入的入口不可用。处理阻塞项后，在工作台刷新并从首页重新核对。批准后的“恢复新放置”需要管理员另行确认。

```bash
cloud-manager kvm change-host-fence TENANT PROJECT SITE HOST REVISION < fence.json
cloud-manager kvm get-host-fence TENANT PROJECT SITE HOST
cloud-manager kvm list-host-fence-work TENANT PROJECT SITE HOST
cloud-manager kvm get-vm-recovery TENANT PROJECT SITE VM
cloud-manager kvm request-vm-recovery TENANT PROJECT SITE VM REVISION < recovery.json
cloud-manager kvm review-vm-recovery TENANT PROJECT SITE VM REVISION < review.json
```

### 自有 KVM NFSv4 共享存储

执行 `cloud-manager migrate` 应用 `0131_kvm_shared_storage`。站点先准备项目专用的 NFSv4 导出、访问控制及实际容量上限。该导出用于平台管理卷，镜像缓存另外配置。管理员登记不可变的共享池 ID、规范 IPv4、导出路径和 MiB 预算。同一服务地址上的相同导出或包含关系会被拒绝。站点需为每个登记的导出提供独立容量预算，避免用别名重复登记同一后端。

```json
{"id":"shared-data","server":"10.24.0.9","export":"/kvm/compute","capacity_mib":524288}
```

```bash
cloud-manager kvm register-shared-storage TENANT PROJECT SITE < storage.json
cloud-manager kvm get-shared-storage TENANT PROJECT SITE POOL
```

每台候选宿主机先建立可写的持久 libvirt `netfs` 池。配置 `format type='nfs'`、NFSv4/TCP 和服务端锁，源地址和导出与登记一致。在宿主机登记里填写本机 libvirt 池名 `storage_pool` 及 `shared_storage_pool_id`。候选主机可使用不同的本机池名和挂载目录。`observe-host` 校验 libvirt XML、Linux 挂载身份和实际文件系统，失败时上报零容量。绑定的代理还会在启动卷暂存和后续任务执行前检查挂载。 [libvirt 存储池 XML](https://libvirt.org/formatstorage.html) [Linux NFS 锁语义](https://man7.org/linux/man-pages/man2/flock.2.html)

在预留中填写 `shared_storage_pool_id`。省略 `storage_pool` 时允许选择该共享池任意合格的宿主机。CPU、内存及 GPU 按宿主机计费，磁盘预留由候选宿主机共用同一预算。未绑定的放置在成功回收后释放预算，退役后保留的卷继续计为 `retained_mib`。查询回执包含仍在记账的 `reserved_mib` 和 `retained_mib`，代理用 `usage=false` 读取身份与预算。共享池上的新 VM 使用登记镜像及平台生成的独占卷名。冷迁移可显式指定共享池上的目标预留，并在审批时复核目标容量。

### 自有 KVM 网络与 IPv4 地址池

执行 `cloud-manager migrate` 应用 `0115_kvm_ipam`，更新 API 副本和站点代理。管理员在 `POST /v1/tenants/{tenant}/projects/{project}/kvm/sites/{site}/networks` 登记项目站点网络。在宿主机登记的 `networks` 中使用相同网络名。地址池不可变，规范 IPv4 子网为 /16 至 /30，连续地址段包含 1 至 4096 个可用地址，排除网关、网络地址和广播地址。同一项目站点的地址池子网不得重叠。启用前现有网络的有效预留和已绑定虚拟机须先结算。

```json
{"network":"app","subnet":"10.77.0.0/29","gateway":"10.77.0.1","first_ip":"10.77.0.2","last_ip":"10.77.0.4"}
```

```bash
cloud-manager kvm register-network TENANT PROJECT SITE < ipv4-pool.json
cloud-manager kvm get-network TENANT PROJECT SITE app
```

在已登记网络放置时，IPv4 和宿主机资源在同一事务中预留。默认取最低可用 IPv4，也可在原放置请求中指定 `ipv4`。已占用、越界或地址池耗尽返回 409。收据与 VM 记录包含 `ipv4` 和 `mac`，同键重放返回原地址和状态。未绑定的预留到期或主动释放后可跨宿主机重分配。已绑定地址在关机时保留，在退役确认时随放置预留一起释放。无地址池的网络继续支持原放置流程。

站点管理员在每个候选宿主机预建同名、已激活的持久 libvirt 网络，配置地址池的网关与 IPv4 子网，并从动态 DHCP 范围中排除整段池地址。MAC 由前缀 `02:00` 和 IPv4 的四个十六进制字节组成。例如 `10.77.0.2` 对应 `02:00:0a:4d:00:02`。现场网络须按项目及站点隔离，避免同一二层域的外部地址分配重叠。

```xml
<network>
  <name>app</name>
  <ip address="10.77.0.1" prefix="29">
    <dhcp>
      <host mac="02:00:0a:4d:00:02" name="app-2" ip="10.77.0.2"/>
      <host mac="02:00:0a:4d:00:03" name="app-3" ip="10.77.0.3"/>
      <host mac="02:00:0a:4d:00:04" name="app-4" ip="10.77.0.4"/>
    </dhcp>
  </ip>
</network>
```

#### 宿主机静态 DHCP 预览与配置

升级 API 副本后，在绑定的 Linux 宿主机使用当前代理的短期服务凭据，并明确传入站点核实过的本地网络 UUID。预览查询两个配置视图、动态范围和活动 DHCP 租约。仅绑定该宿主机的服务主体可以从 `POST /v1/tenants/{tenant}/projects/{project}/kvm/sites/{site}/hosts/{host}/dhcp-plan/{network}` 取得对应地址池，旧代次返回 412。

```bash
cloud-manager kvm plan-network-dhcp TENANT PROJECT SITE HOST GENERATION NETWORK NETWORK_UUID
cloud-manager kvm apply-network-dhcp TENANT PROJECT SITE HOST GENERATION NETWORK NETWORK_UUID
```

应用命令需要本地 libvirt 网络写权限，每次最多补齐 16 个地址。只向缺失的生效或持久视图增加静态映射，保留已匹配的条目及现场其他配置。申请作用域、网络 UUID、网关或子网不符，池地址与动态范围重叠，或者既有条目、租约与预期归属冲突时停止。代理在每次调用 `net-update` 前重新校验绑定和代次。站点须独占管理该 libvirt 网络，确保写入期间仅有受控的配置变更。[libvirt net-update](https://www.libvirt.org/manpages/virsh.html#net-update)

输出的 `remaining` 是仍需补齐的地址数，`next` 列出下一批及其缺失视图，`verified_updates` 按已核实的配置视图计数。回执丢失后通过只读核对确认效果。部分写入或中断时，重新执行会从两个视图读取当前状态，仅补齐剩余条目。对于池中已完全配置的网络，预览和应用都返回 `remaining: 0`。

代理为 VM 网卡写入预留 MAC，在定义、启动和启动后确认时都检查本地 libvirt 生效与持久配置，阻止漂移的地址映射进入运行态。现场还需验证真实 DHCP 发放、访客网络可达性和网络隔离。[libvirt 网络 XML](https://libvirt.org/formatnetwork.html#ip-addresses)

### 自有 KVM 虚拟机创建

执行 `cloud-manager migrate` 应用 `0112_kvm_vms`。从项目里已有的有效预留提交创建。首条创建路径使用专用本地目录池里的已预置 raw 启动卷，以及预留中指定的 libvirt 网络。启动卷须供该实例独占，由站点管理员核对操作系统镜像和数据归属。内置创建代理支持 CPU、内存和本地磁盘。

```bash
cloud-manager kvm submit-vm TENANT PROJECT SITE < vm.json
cloud-manager kvm get-vm TENANT PROJECT SITE VM
cloud-manager kvm review-vm TENANT PROJECT SITE VM REVISION < review.json
```

```json
{"placement_id":"PLACEMENT_ID","boot_volume":"boot-ready"}
```

预留原申请人提交创建。同一预留和启动卷重放返回同一 VM，改用其他卷返回 409。受理把原预留转为 `bound`，原 TTL 到期后仍计入宿主机用量。独立审批人发出 `{"decision":"approve"}` 或 `{"decision":"reject"}`。未登记外部写入的任务可拒绝并释放预留。代理领取写入前，API 复核宿主机代次、排空状态以及申请人和审批人的成员版本。

控制台按当前选择预留 5 分钟容量，支持站点登记的 raw 镜像，以及本地池的专用预置卷。镜像字节数须能装入所填磁盘容量。预留请求结果未知时，控制台沿用原幂等键。提交创建结果未知时，从原预留查询 VM ID，并打开审批回执。同一标签页每个项目和身份保留一个待处理的创建申请。

`GET /v1/tenants/{tenant}/projects/{project}/kvm/provisioning-hosts` 为项目执行者提供分页候选，支持 `site`、`limit` 和 `after`。候选要求可用代理授权、近期上报和未排空的宿主机，实际剩余量在预留时重新核对。站点下的 `GET /images` 支持 `limit` 和 `after`。原预留人可通过站点下的 `GET /placements/{placement}/provisioning` 查询预留及已提交的 VM ID。该查询在拒绝和冷迁移之后也保留原 VM 关联。

在绑定的 Linux 宿主机上，运行账号须具备实际定义和启动本地 libvirt 域的权限。站点管理员在本地配置可用启动卷，并使用本项目代理的短期 Bearer 凭据。读取仍走本地 `qemu:///system` 的只读连接。每次运行至多执行一步，按固定间隔重复运行。

```bash
cloud-manager kvm run-vm TENANT PROJECT SITE HOST GENERATION < agent.json
```

```json
{"boot_volumes":["boot-ready"]}
```

代理从任务生成域定义，核对宿主机 UUID、网络、启动卷大小和独占关系。仅在站点有独占 libvirt 管理权且磁盘由受控流程准备时启用。定义与启动各自先登记一次性授权。断连后轮询只点查同一个 UUID、域归属、vCPU、内存、磁盘和网络。已定义的任务可领取启动授权。缺失或属性不符时继续保留容量和启动卷，禁止换绑正在执行的宿主机代理。操作记录的 `running` 表示已观测到 libvirt 域运行，访客操作系统启动仍由站点验证。[libvirt 域 XML](https://libvirt.org/formatdomain.html) · [virsh](https://www.libvirt.org/manpages/virsh.html)

### 自有 KVM 镜像与启动卷

执行 `cloud-manager migrate` 应用 `0116_kvm_boot_images`，更新 API 副本和代理。项目管理员在 `POST /v1/tenants/{tenant}/projects/{project}/kvm/sites/{site}/images` 登记不可变 raw 镜像。登记包含镜像 ID、小写 SHA-256 和精确字节数，字节数的上限为 32 GiB。镜像 ID 按项目与站点隔离，重复登记返回 409。

```bash
cloud-manager kvm register-image TENANT PROJECT SITE < image.json
cloud-manager kvm get-image TENANT PROJECT SITE IMAGE
```

```json
{"image_id":"base-linux","image_sha256":"0000000000000000000000000000000000000000000000000000000000000000","image_size_bytes":1073741824}
```

站点管理员维护镜像目录，在目录中以 `SHA256.raw` 存放对应文件。也可在 Linux 或 macOS 上用已具备该项目读权限的凭据，从站点维护的 HTTPS 镜像源拉取。

```bash
cloud-manager kvm mirror-image TENANT PROJECT SITE IMAGE < mirror.json
```

```json
{"mirror_url":"https://images.example.net/kvm","image_root":"/srv/kvm-images"}
```

镜像源按文件名提供 `/kvm/SHA256.raw` 原始字节，使用宿主机信任的 TLS 证书。命令拒绝重定向和内容编码，控制面令牌仅用于读取登记合同。数据流到磁盘，校验长度和摘要后重新查询登记与读权限，再原子发布。相同摘要的有效文件可就地复用，损坏目标保留供运维检查。要启动使用此镜像的 VM，各候选宿主机的 `image_root` 都应能读到该文件。

#### 目标宿主机备份回执

应用 `0130_kvm_host_images`。在候选宿主机上，用该宿主机绑定的代理凭据执行 `mirror-host-image`。使用与 `run-vm` 相同的本地 `image_root` 和站点维护的 HTTPS 镜像源。代理先验证本机 libvirt UUID、服务绑定和代次，校验尺寸与 SHA-256，并在文件原子发布后上报回执。已存在的文件会重新校验，成功时刷新回执，无需重新下载。

```bash
cloud-manager kvm mirror-host-image TENANT PROJECT SITE HOST GENERATION IMAGE < mirror.json
cloud-manager kvm get-host-image TENANT PROJECT SITE HOST IMAGE
cloud-manager kvm report-host-image TENANT PROJECT SITE HOST IMAGE < report.json
```

回执有效期为 30 分钟，换绑或隔离代次变化使旧回执失效。项目成员可查询版本、状态和有效期。已绑定的代理可向宿主机的 `/images/{image}` 回报 `missing`，提交当前 `revision`、宿主机 `uuid` 与 `generation`、`image_sha256` 和 `image_size_bytes`。探测到本地镜像损坏时，镜像命令也会尝试撤销之前的校验回执。重放相同上报返回原回执，有效期只有通过新版本校验才能刷新。

#### 从镜像创建启动卷

提交 VM 时可以传入 `{"placement_id":"PLACEMENT_ID","image_id":"base-linux"}`，或者使用原来的 `boot_volume`，两者择一。预留磁盘容量须覆盖镜像字节数。系统按预留生成独占启动卷名，`cmp-image-` 前缀用于管理生成的卷。提交回执包含镜像摘要、大小和目标卷，同预留同镜像重试返回原 VM。

需要准备镜像的宿主机在 `run-vm` 的本地配置中设置规范绝对路径 `{"image_root":"/srv/kvm-images"}`。也可同时提供 `boot_volumes`。目录池和镜像目录由站点独占管理，程序校验路径和文件类型。镜像准备在临时文件中写入，经精确长度和 SHA-256 校验、授权复核和持久化后，原子发布目标卷。已有目标仅在内容与镜像完全一致且链接状态可以核验时采用。

独立审批后的镜像任务先进入 `staging`。只有发布目标卷、刷新 libvirt 池且核验卷后才成为 `staged`。刷新回执丢失时，下一轮核对已有目标再刷新。准备阶段只有长度或摘要不符才报告 `mismatch`，锁竞争保留原观测供后续重试。代理在定义和首次启动前复查启动卷内容，虚拟机运行后的写入属于该实例。实际宿主机还需验收 raw 镜像启动、存储目录权限与 QEMU 访问。[libvirt 池刷新](https://libvirt.org/formatstorage.html)

### 自有 KVM 虚拟机优雅关机

执行 `cloud-manager migrate` 应用 `0113_kvm_vm_shutdown`，更新 API 副本和宿主机代理。项目操作者对运行中的 VM 提交关机申请。API 为 `POST /v1/tenants/{tenant}/projects/{project}/kvm/sites/{site}/vms/{vm}/shutdown`，使用当前 VM 版本作为 `If-Match`，持久受理返回 202 和 VM 地址。相同版本并发提交只有一次受理，旧版本返回 412。请求结果不明确时可读取 VM 当前状态。

```bash
cloud-manager kvm shutdown-vm TENANT PROJECT SITE VM REVISION
cloud-manager kvm review-vm TENANT PROJECT SITE VM REVISION < review.json
cloud-manager kvm get-vm TENANT PROJECT SITE VM
```

独立审批人批准后，已绑定的代理通过现有 `run-vm` 领取任务。代理可在宿主机排空期间执行关机，并可处理同一台宿主机上早期代次创建的域。领取关机授权前，API 复核请求者、审批者和代理的有效身份及代次。审批人可在写入授权登记前拒绝申请，VM 恢复为 `running`。

代理核对宿主机 UUID、持久域、所有权和运行配置。运行中的域需要启用 ACPI，代理仅发送一次 `virsh shutdown VM --mode acpi`。新版代理为在 x86 宿主机上新定义的域启用 ACPI。既有运行域缺少 ACPI 时，申请保留在 `shutdown_queued` 并记录 `mismatch`。管理员可在受控维护窗口核对既有域配置和访客支持。已经匹配且关机的持久域可直接完成观测。[libvirt ACPI 配置](https://libvirt.org/formatdomain.html#hypervisor-features) · [virsh shutdown](https://www.libvirt.org/manpages/virsh.html#shutdown)

代理认领后进入 `stopping`。命令回执丢失或访客异步处理关机时，后续轮询只点查。只有确认同一个持久域处于 `shut off` 才更新为 `stopped` 并同事务登记审计。缺失、不符或仍运行时继续保留占用，待观测的关机也会阻止代理换绑。关机后保留域、启动卷和原容量预留，供后续恢复或回收。

### 自有 KVM 虚拟机退役

执行 `cloud-manager migrate` 应用 `0114_kvm_vm_retirement`，更新 API 副本和宿主机代理。项目管理员可对已确认关机的 VM 提交退役。API 为 `POST /v1/tenants/{tenant}/projects/{project}/kvm/sites/{site}/vms/{vm}/retirement`，使用当前 VM 版本作为 `If-Match`，持久受理返回 202 和 VM 地址。独立审批人批准后，现有 `run-vm` 代理才可领取任务。登记写入授权前可拒绝申请并返回 `stopped`。

```bash
cloud-manager kvm retire-vm TENANT PROJECT SITE VM REVISION
cloud-manager kvm review-vm TENANT PROJECT SITE VM REVISION < review.json
cloud-manager kvm get-vm TENANT PROJECT SITE VM
```

代理在授权前后均核对宿主机、持久域归属和关机状态，检查自动启动、托管保存、快照、检查点、NVRAM、varstore 和 TPM。已有依赖、配置不符或身份无法核验时保留域和预留。授权后最多执行一次 `virsh undefine VM`。调用保留 libvirt 默认依赖保护和存储。站点必须维持对 libvirt 与卷池的独占管理。[libvirt undefine 合同](https://libvirt.org/html/libvirt-libvirt-domain.html#virDomainUndefineFlags)

收到回执或断连后，代理都通过完整域清单继续只读核对。第一轮确认缺失后推进观测版本，第二轮必须基于新版本重新读取。期间若再次看到该域，先前的缺失证据失效。平台在同一事务标记 `retired`、释放原放置预留并登记审计。预留显示 `boot_volume_retained: true`，原启动卷保留且继续禁止绑定任何新 VM。文件系统容量采样仍会计入该卷实际占用。退役请求在写入授权登记前发现域已缺失时保持待核对。

### 自有 KVM 保留卷清理

应用 `0133_kvm_volume_cleanups`，更新 API 和宿主机代理。项目管理员按原 `placement_id` 申请清理退役卷或迁移后的历史卷。历史卷仍属于原宿主机。故障疏散产生的旧卷需先确认源域和网络已清理。进行中的冷迁移申请与保留卷清理互斥。

申请正文包含 `cleanup_id`、`backup_image_id` 和站点保留策略审核的 `evidence_sha256`，通过强 `If-Match` 提交当前宿主机版本。独立审批人使用清理回执的 `revision` 批准或拒绝。源代理从本地 `image_root` 读取已登记备份的 `SHA256.raw`，核对其和原卷的准确字节数与 SHA-256。备份沿用镜像目录的 32 GiB 上限。镜像目录需与启动卷池分离，并由站点在独立故障域保管备份。预置旧卷还需出现在代理配置的 `boot_volumes` 中。

```bash
cloud-manager kvm request-volume-cleanup TENANT PROJECT SITE PLACEMENT HOST_REVISION < cleanup.json
cloud-manager kvm review-volume-cleanup TENANT PROJECT SITE PLACEMENT CLEANUP REVISION < review.json
cloud-manager kvm get-volume-cleanup TENANT PROJECT SITE PLACEMENT CLEANUP
cloud-manager kvm list-volume-cleanups TENANT PROJECT SITE HOST [AFTER]
cloud-manager kvm run-volume-cleanup TENANT PROJECT SITE HOST GENERATION PLACEMENT CLEANUP < agent.json
```

绑定代理核对池 UUID、挂载、文件身份、域及 backing chain 引用后，领取一次逻辑删除授权。站点需保持池路径和挂载受控，共享池仅由受管宿主机使用。代理后续刷新卷清单，分别核对文件、libvirt 目录和域引用。两次独立确认缺失后，移除预算中的保留卷占用并释放卷名。卷重新出现会重置观察记录。删除回执丢失后，使用同一代理命令观察结果。 [libvirt 卷删除](https://www.libvirt.org/manpages/virsh.html#vol-delete)

已派发的任务阻塞重新绑定和隔离后重新接入。可按宿主机分页查看当前任务。物理隔离已确认且旧删除进程已停止时，独立审批人可使用清理版本和现场证据结算。确认卷仍在时提交 `observed=present` 和证据摘要，可保留磁盘后重新申请。确认缺失时提交 `observed=absent` 和两份独立证据摘要，结算保留占用。

控制台在申请前核对同站点登记的备份 ID 和字节数。管理员确认独立备份及保留策略后提交，同一表单重试沿用清理 ID。审批使用清理版本。任务已派发且原宿主机进入新的已确认隔离代次后，独立审批人可从隔离工作台登记现场结算证据。结算完成后，活动清单移除该任务，虚拟机的保留卷历史保留回执。

项目成员可在站点路径下通过 `GET /vms/{vm}/retained-volumes` 发现原放置、原宿主机、备份尺寸要求及清理状态，并通过 `GET /placements/{placement}/volume-cleanups` 查询该放置的清理历史。后者按申请时间倒序排列。两者支持 `limit` 和 `after`，默认 50 条且最多 100 条。有更多记录时，使用同一列表返回的 `data.next` 续读。

```bash
cloud-manager kvm resolve-volume-cleanup TENANT PROJECT SITE PLACEMENT CLEANUP REVISION < evidence.json
```

### 自有 KVM 虚拟机冷迁移

执行 `cloud-manager migrate` 应用 `0125_kvm_cold_migrations`。首条路径适用于同一项目和站点、已确认源域缺失且原启动卷仍在保留的虚拟机，以备份恢复到另一宿主机。源虚拟机可经正常退役进入 `retired`，也可经故障隔离后的独立核对收敛为保留启动卷的 `rejected` 或 `retired`。

源宿主机仍可访问时，站点人员用该宿主机绑定的代理凭据执行 `backup-vm`，并传入当前宿主机代次、VM 版本及新的镜像 ID。代理通过 `/hosts/{host}/vm-work/{vm}/backup-plan` 核对项目、物理宿主机和已保留的启动卷，在本机确认域缺失、卷为独占 raw 文件且没有其他域引用。源卷上限为 32 GiB。代理在复制前后复核授权、卷和域状态，校验两次读取的长度及摘要，再以 `0600` 权限发布 `image_root/SHA256.raw`。同一内容可复用已校验的文件。将 `image_root` 设为独立的受控目录。预置启动卷还需列在 `boot_volumes` 中。

```bash
cloud-manager kvm backup-vm TENANT PROJECT SITE HOST GENERATION VM REVISION IMAGE_ID < agent.json > backup-receipt.json
jq '.image' backup-receipt.json > image.json
cloud-manager kvm register-image TENANT PROJECT SITE < image.json
```

站点人员保存收据原件，核对源 VM、预留和备份时间点，将收据或现场核验记录的 SHA-256 用作迁移证据。站点也可提交已独立核对的外部备份。目标代理从 `image_root` 读取已部署的备份。发起迁移的管理员通过原放置接口在另一宿主机预留等量 CPU、内存及磁盘、相同逻辑网络和原 IPv4。目标主机仍需有效容量与网络配置。源 VM 有 IPv4 时，目标的实际网络要支持原地址和 MAC，代理会核对生效及持久 DHCP 映射。

向站点下的 `/vms/{vm}/cold-migration` 提交 `migration_id`、`target_placement_id`、`image_id` 和关联证据的 `evidence_sha256`。用强 `If-Match` 固定原 VM 版本。独立审批人核对镜像和备份关联证据，再向 `/cold-migration/review` 提交 `migration_id` 与 `decision=approve` 或 `reject`。审批使用原版本。相同申请和审批可以重放。过期的目标预留须经拒绝本次申请后重新申请。项目成员用 GET 查询最新申请。

批准事务绑定目标预留、保留原 VM ID 并推进版本，将虚拟机移交到目标宿主机的 `queued` 状态。源启动卷继续保留，旧宿主机的代理无法领取或上报该 VM。现有 `run-vm` 代理核对备份数据、网络和目标主机，依次准备卷、定义和启动。定义与启动各登记一次写入许可，结果未知时只读核对。迁移审批后，通过普通 VM 查询等到 `running` 并核对访客状态。

在控制台选择同站点、同网络的目标宿主机及已登记备份。页面沿用原 VM 的容量、网络与 IPv4 申请目标预留。未提交的草稿可放弃。同一标签页刷新后，可沿用原预留键、迁移 ID、源版本和备份资料继续提交。拒绝显式预留的迁移后，原申请人可从回执释放仍有效的目标预留。

站点虚拟机路径下的 `GET /cold-migration-eligibility` 返回当前源状态和适用的隔离代次。`GET /cold-migrations` 按申请时间倒序分页，默认 50 条且最多 100 条，使用 `after` 续读。`GET /cold-migrations/{migration}` 按 ID 读取审批、目标与源端清理回执。

```bash
cloud-manager kvm request-cold-migration TENANT PROJECT SITE VM REVISION < migration.json
cloud-manager kvm get-cold-migration TENANT PROJECT SITE VM
cloud-manager kvm review-cold-migration TENANT PROJECT SITE VM REVISION < review.json
cloud-manager kvm get-vm TENANT PROJECT SITE VM
```

### 自有 KVM 实时迁移

应用 `0132_kvm_live_migrations`，升级 KVM API 和宿主机代理。首条路径适用于同站点、同一已验证 NFSv4 池、同名 libvirt 池及同一个外部二层网络上的运行中 VM。两台宿主机的 libvirt 网络须是已激活且持久的 `forward mode="bridge"`，使用同一网络 UUID 和网桥名。地址由站点网络分配。此路径要求单个管理 raw 启动卷、明确的 `cache="none"`、兼容的 CPU、QEMU 及 libvirt 配置。 [libvirt 迁移前提](https://www.libvirt.org/manpages/virsh.html#migrate)

站点为源、目标及源 libvirt 守护进程配置经过认证的 TLS 连接。源代理本地配置以目标宿主机 ID 映射 `qemu+tls://主机名/system`，目标代理可以使用空对象。源代理在派发前检查目标的 TLS 连接、libvirt 宿主机 UUID、桥接网络和域缺失状态。迁移使用 libvirt TLS 隧道保护虚拟机内存数据。 [libvirt 对等迁移与隧道](https://libvirt.org/migration.html)

```json
{"destinations":{"host-two":"qemu+tls://host-two.example.net/system"}}
```

```bash
cloud-manager kvm request-live-migration TENANT PROJECT SITE VM VM_REVISION < migration.json
cloud-manager kvm review-live-migration TENANT PROJECT SITE VM MIGRATION VM_REVISION < review.json
cloud-manager kvm get-live-migration TENANT PROJECT SITE VM MIGRATION
cloud-manager kvm run-live-migration TENANT PROJECT SITE HOST GENERATION VM MIGRATION < migration-agent.json
```

管理员在申请正文提供 `migration_id` 和 `target_host`。独立审批人提交 `{"decision":"approve"}` 后，目标 CPU 和内存进入预留，磁盘继续沿用同一共享卷。先在目标运行代理完成本地检查，再在源运行代理派发。目标准备证据有效期为 90 秒，可再次采样。已派发的后续调用只进行观测。源代理报告原域已消失后，目标代理报告同一个 UUID、MAC 和持久域在运行。平台在同一事务切换宿主机归属和计算预留，保留 VM ID、放置 ID、启动卷和原创建请求的幂等记录。

在虚拟机 API 下，`GET .../vms/{vm}/live-migrations` 为项目成员提供申请记录，包含最近一次准备、派发和交接时间。`GET .../vms/{vm}/live-migration-targets` 为管理员提供同站点候选，按照池、网络、排空和隔离状态、上报新鲜度及报告容量初筛。候选查询不会预留容量。审批时根据实际占用重新计算，宿主机代理在执行时核验 TLS 与本地桥接。两个列表都支持 `limit` 和 `after`，默认 50 条且最多 100 条；有更多记录时返回 `data.next`。控制台在同一次确认的重试中沿用原申请 ID 与 VM 版本。

派发前，独立审批人可拒绝迁移并释放目标计算预留。派发后发生无法收敛的故障时，先按宿主机隔离协议分别确认两端的新隔离代次。现场证据须证明旧派发和 libvirt 迁移任务已结束，目标没有残留域定义或自动启动路径。独立审批人使用当前**迁移版本**和 `evidence_sha256` 提交终止。控制面保留原归属和审计回执，之后可继续使用原有故障疏散流程。

```bash
cloud-manager kvm abort-live-migration TENANT PROJECT SITE VM MIGRATION MIGRATION_REVISION < evidence.json
```

### 自有 KVM 故障疏散

执行 `cloud-manager migrate` 应用 `0126_kvm_evacuation`。源宿主机失联且仍可能保留原域时，站点人员先通过带外手段确保旧宿主机及其网络不能对外运行，并由独立审批人确认该次隔离。管理员核对已经存在的可信备份、可恢复时间点和源 VM 身份。流程支持已有运行或停机历史的 VM，要求源放置仍绑定。

管理员使用原放置接口申请目标容量，在放置正文中填写 `evacuation_vm_id` 和 `required_host`，沿用源 VM 的网络和 CPU、内存及磁盘规格。平台保留源 IPv4 到审批提交，目标预留只占容量。向 `/vms/{vm}/cold-migration` 提交目标预留、备份、证据，以及已独立确认的 `source_fence_generation`。独立审批再次核对源隔离和目标代次。事务同时转移 VM ID、IPv4 及 MAC，保留原启动卷，并为目标代理入队。即使地址池没有空闲地址，也能使用原地址恢复。

应用 `0127_kvm_evacuation_placement` 后，疏散申请可省略 `target_placement_id`，由控制面在其他故障域内选择可用宿主机。控制面沿用源网络与资源规格，核对目标代理授权、容量观测及剩余容量，并将目标放置与待审批申请同事务写入。候选宿主机须可取得已登记的备份。默认预留 15 分钟，收据包含 `target_host`、`target_placement_id` 和 `automatic_placement=true`。同一申请重试时继续省略目标预留。审批拒绝会释放自动预留。若没有可用目标或候选正被其他事务占用，申请返回 409。预留过期时，拒绝原申请后用新迁移 ID 提交，控制面重新选择目标。

需要控制面验证备份可达时，在单项冷迁移请求、批次的每项或疏散预案的每项设置 `require_verified_backup: true`。自动选址只接受同时具备足够容量和当前回执的目标，显式目标也要通过回执校验。审批时再次核对当前回执，失效则返回 409。自动预案在没有合格目标时记录 `verified_target_unavailable` 并重试。目标代理在实际暂存启动卷时仍校验文件字节。

控制台从当前已确认隔离的 VM 发起单机疏散，可自动选址或由管理员指定目标。申请绑定当前隔离代次，并可要求有效的备份校验回执。批准后到原宿主机的隔离工作台登记疏散源端清理证据。

源宿主机保持隔离，直到站点人员确认原域和自动启动配置已清除、遗留网络映射已清理，并确保原域无法再次接入。独立审批人以当前宿主机 `If-Match` 向源宿主机的 `/fence/cleanup` 提交 `vm_id`、`migration_id` 和现场清理证据的 `evidence_sha256`。在该隔离代次内，`GET /fence/work` 将未完成清理的疏散显示为 `cleanup_required`，并提供 `evacuation_id`。所有阻塞清除后，仍需完成原宿主机重新接入审批及新的容量上报。

```bash
cloud-manager kvm reserve TENANT PROJECT SITE EVACUATION_KEY < target-placement.json
cloud-manager kvm request-cold-migration TENANT PROJECT SITE VM REVISION < evacuation.json
cloud-manager kvm review-cold-migration TENANT PROJECT SITE VM REVISION < review.json
cloud-manager kvm list-host-fence-work TENANT PROJECT SITE SOURCE_HOST
cloud-manager kvm confirm-evacuation-cleanup TENANT PROJECT SITE SOURCE_HOST HOST_REVISION < cleanup.json
```

#### 宿主机批量疏散

应用 `0128_kvm_evacuation_batches`。项目管理员向源宿主机的 `/fence/evacuation-batches` 提交 `batch_id`、`source_fence_generation` 和 `items`，并用强 `If-Match` 提交当前宿主机版本。一个批次可包含 1 至 16 台已确认属于该宿主机的 VM，`items` 每项填写 `vm_id`、`vm_revision`、`image_id` 和 `evidence_sha256`。

控制面按清单顺序在其他故障域内预留容量。任一项版本、备份或容量无效时，整个事务回滚，批次和预留都不会生成。成功返回 202、批次地址和各 VM 的迁移 ID。重试使用原宿主机版本和相同清单，包含相同顺序。项目成员可查询原批次地址，查看当时受理的全部 VM 及当前审批和现场清理状态。

独立审批人沿用单 VM 迁移审批逐台批准或拒绝。批准后才转移原 IPv4 并允许目标代理按备份恢复。拒绝会释放该项的自动预留。源宿主机重新接入仍需核对每台已疏散 VM 的源端清理证据。

```bash
cloud-manager kvm request-evacuation-batch TENANT PROJECT SITE SOURCE_HOST HOST_REVISION < batch.json
cloud-manager kvm get-evacuation-batch TENANT PROJECT SITE SOURCE_HOST BATCH_ID
```

#### 隔离确认后自动提交疏散预案

应用 `0129_kvm_evacuation_plans` 后，用同一套 PostgreSQL 连接配置启动 `cloud-manager kvm-evacuation-controller`。多个控制器副本可共享一个任务表。进程只需数据库连接配置，站点人员仍负责带外隔离并由独立审批人提交 `confirm`。

项目管理员可在宿主机正常或隔离待确认时，向其 `/fence/evacuation-plans` 提交 `plan_id` 和 1 至 16 项与批量疏散相同的 `items`，并附当前宿主机 `If-Match`。预案绑定隔离代次、申请人的项目授权版本和各 VM 的版本及已登记备份。同一宿主机的多个预案可分别覆盖不同 VM。用同一个 `plan_id`、原宿主机版本和相同清单可查询登记结果。变更清单时先取消旧预案，再使用新 ID 登记。

隔离确认与激活预案同事务写入。控制器再次核对隔离代次、当前授权、VM 和备份，随后原子受理批次。项目成员通过预案的 `batch_id` 查询各 VM 的待审批申请，独立审批人逐台审核。出现容量不足、源宿主机正在申请重新接入或暂时的数据库写入失败时，控制器每 30 秒重试。查询中的 `last_result` 和 `next_attempt_at` 表明等待原因及下次执行时间。授权、源代次或 VM 状态变化导致终止时，先查看失败原因，再重新核对现场状态。已确认隔离的宿主机可使用手工批次接口提交修订后的方案。

批次受理后，目标预留有效期为 15 分钟。超过有效期的申请由独立审批人拒绝后，可按当前状态重新提交手工批次。

```bash
cloud-manager kvm-evacuation-controller
cloud-manager kvm arm-evacuation-plan TENANT PROJECT SITE SOURCE_HOST HOST_REVISION < plan.json
cloud-manager kvm get-evacuation-plan TENANT PROJECT SITE SOURCE_HOST PLAN_ID
cloud-manager kvm cancel-evacuation-plan TENANT PROJECT SITE SOURCE_HOST PLAN_ID
```

实际停机一致性备份、恢复后的访客数据、物理网络连通、带外隔离及真实 NFSv4 故障恢复仍需现场验收。实时迁移需在实际 KVM 双宿主机量测访客停顿、数据完整性和中途断连恢复。保留卷清理需在实际 libvirt 池及 NFS 服务端验证删除、目录刷新和容量归还。
