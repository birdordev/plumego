"use strict";

const find = (id) => document.getElementById(id);
const state = { profile: null, project: null, view: "assets", cursor: "", filters: "", rows: [], selected: new Map(), load: 0, detail: 0, batch: null, catalogAttempts: new Map(), networkAttempts: new Map() };
const titles = { assets: "资源清单", operations: "操作记录", "service-catalog": "服务目录", alerts: "项目告警", "console-grants": "原生控制台", batch: "批量操作", audit: "项目审计" };
const kinds = { instance: "实例", volume: "卷", snapshot: "云盘快照", network: "网络", subnet: "子网", "security-group": "安全组", "security-group-rule": "安全组规则", "elastic-ip": "弹性公网 IP", image: "镜像", zone: "可用区", region: "地域" };
const statuses = { awaiting_approval: "待审批", queued: "已排队", applying: "执行中", observing: "观测中", needs_review: "待复核", succeeded: "成功", failed: "失败",
  approved: "已批准", rejected: "已拒绝", revoked: "已撤销", in_use: "连接中", closed: "已断开", expired: "已过期",
  key_sent: "公钥已发放", dispatch_unknown: "发放结果不确定",
  open: "待确认", acknowledged: "已确认", resolved: "已恢复", failure: "失败", throttled: "限流", stale: "数据过期" };
const grantPurposes = { troubleshoot: "故障排查", maintenance: "维护", incident: "应急处置" };
const alertRules = { operation_review: "操作待复核", operation_delayed: "操作超时", scan_failures: "连续盘点失败", scan_stale: "盘点数据过期" };
const actions = { start: "启动", stop: "停止", reboot: "重启", delete: "删除", create: "创建实例", "create-network": "创建网络",
  "create-subnet": "创建子网", "delete-network": "删除 VPC", "delete-subnet": "删除子网", "delete-security-group": "删除安全组", "create-snapshot": "创建云盘快照", "delete-snapshot": "删除云盘快照", "delete-image": "删除自有镜像", "create-image": "制作系统盘镜像", "copy-image": "跨地域复制镜像", "restore-snapshot": "从快照恢复云盘", "rollback-snapshot": "回滚原云盘", "create-security-group": "创建安全组", "create-security-group-rule": "创建安全组规则",
  "create-elastic-ip": "申请弹性公网 IP", "release-elastic-ip": "释放弹性公网 IP", "bind-elastic-ip": "绑定弹性公网 IP",
  "unbind-elastic-ip": "解绑弹性公网 IP", "revoke-security-group-rule": "撤销安全组规则", "attach-volume": "挂卷", "detach-volume": "卸卷" };
const auditKinds = { account: "云账号", ownership: "资产归属", operation: "操作状态", approval: "审批", resolution: "人工处置", observation: "续查", catalog: "服务目录", alert: "项目告警", console_access: "原生控制台" };
const auditActions = { register: "登记", rotate: "轮换凭据", suspend: "停用", resume: "恢复", transfer: "转移归属", assign: "分配归属",
  share: "共享", unshare: "取消共享", publish: "发布模板", retire: "停用模板", opened: "告警触发", acknowledged: "确认告警", resolved: "告警恢复" };
const catalogFields = { title: "名称", account: "云账号", region: "地域", image_id: "镜像 ID", size: "规格", network_id: "网络或子网 ID", security_group_id: "安全组 ID" };
const auditOutcomes = { approve: "批准", reject: "拒绝", restarted: "已恢复观测" };
const reasons = { ready: "可受理", stale: "实例版本已变更", volume_stale: "卷版本已变更", protected: "受删除保护",
  busy: "有活动操作", unavailable: "资源不可访问", unsupported_parameters: "厂商不支持这些参数" };
const inventoryStates = { missing_candidate: "待确认缺失", stale: "观测已过期" };
const dateFormat = new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "short" });

function el(tag, text, className) {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  if (className) node.className = className;
  return node;
}

function button(text, handler) {
  const node = el("button", text);
  node.type = "button";
  node.addEventListener("click", handler);
  return node;
}

function message(id, text = "", error = false) {
  const target = find(id);
  target.textContent = text;
  target.dataset.error = String(error);
}

function projectURL() {
  return "/v1/tenants/" + encodeURIComponent(state.profile.tenant) + "/projects/" + encodeURIComponent(state.project.id);
}

function statusLabel(code) {
  const node = el("span", statuses[code] || code || "未知", "state");
  node.dataset.state = code;
  return node;
}

function timestamp(value) {
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? "—" : dateFormat.format(date);
}

function canExecute() {
  return ["operator", "admin"].includes(state.project?.role);
}

async function request(path, { method = "GET", body, key, match, csv = false } = {}) {
  const headers = { Accept: csv ? "text/csv" : "application/json" };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (method !== "GET" && method !== "HEAD" && state.profile?.csrf) headers["X-Console-CSRF"] = state.profile.csrf;
  if (key) headers["Idempotency-Key"] = key;
  if (match !== undefined) headers["If-Match"] = '"' + match + '"';
  let response;
  try {
    response = await fetch(path, { method, body, headers, credentials: "same-origin", redirect: "error", cache: "no-store" });
  } catch {
    throw new Error("连接已中断。请检查网络后重试。");
  }
  if (!response.ok) {
    const known = { 400: "输入有误。请检查字段。", 401: "会话已失效。请重新登录。", 403: "当前身份无权执行此操作。",
      409: "操作发生冲突。请刷新状态。", 412: "资源版本已变更。请刷新后重新预览。", 413: "请求过大。请缩小批次。",
      422: "导出超过 2000 条。请缩小筛选范围。", 429: "项目剩余配额不足。" };
    const error = new Error(known[response.status] || "暂时无法读取服务。请稍后重试。");
    error.status = response.status;
    throw error;
  }
  if (csv) {
    if (response.headers.get("Content-Type")?.split(";")[0] !== "text/csv") throw new Error("无法读取审计文件。");
    return response.blob();
  }
  return (await response.json()).data;
}

function showEntry() {
  state.load++;
  state.detail++;
  state.profile = null;
  state.project = null;
  state.rows = [];
  state.selected.clear();
  state.catalogAttempts.clear();
  state.networkAttempts.clear();
  resetBatch();
  find("batch-input").value = "";
  find("batch-id").value = "";
  find("items").replaceChildren();
  find("detail").close();
  find("workspace").hidden = true;
  find("entry").hidden = false;
}

async function showProfile() {
  const profile = await request("/console/session");
  state.profile = profile;
  if (profile.projects.length === 0) {
    showEntry();
    message("login-message", "当前身份没有可查看的项目。", true);
    return;
  }
  const select = find("project");
  select.replaceChildren(...profile.projects.map((project) => {
    const option = el("option", project.id);
    option.value = project.id;
    return option;
  }));
  find("entry").hidden = true;
  find("workspace").hidden = false;
  chooseProject();
}

function chooseProject() {
  state.detail++;
  find("detail").close();
  state.project = state.profile.projects.find((project) => project.id === find("project").value);
  find("audit-nav").hidden = state.project.role !== "admin";
  find("native-nav").hidden = !nativeConsoleEnabled();
  state.selected.clear();
  find("identity").textContent = state.profile.tenant + " / " + state.profile.principal;
  find("batch-input").value = "";
  find("batch-id").value = "";
  resetBatch();
  selectView("assets");
}

function selectView(view) {
  state.view = view;
  state.load++;
  state.cursor = "";
  state.filters = "";
  state.rows = [];
  find("title").textContent = titles[view];
  for (const nav of find("navigation").querySelectorAll("button")) {
    if (nav.dataset.view === view) nav.setAttribute("aria-current", "page");
    else nav.removeAttribute("aria-current");
  }
  find("collection").hidden = view === "batch";
  find("asset-tools").hidden = view !== "assets" || !canExecute();
  find("filters").hidden = view === "service-catalog" || view === "console-grants";
  find("native-tools").hidden = view !== "console-grants";
  find("catalog-tools").hidden = view !== "service-catalog";
  find("catalog-create").hidden = state.project.role !== "admin";
  showCatalogRetry();
  find("batch").hidden = view !== "batch";
  find("batch-editor").hidden = !canExecute();
  find("fill-batch").hidden = state.selected.size === 0;
  find("filters").reset();
  find("search-filter").hidden = view !== "assets";
  find("account-filter").hidden = view === "alerts";
  find("kind-filter").hidden = view !== "assets";
  find("status-filter").hidden = view !== "operations";
  find("audit-export").hidden = view !== "audit";
  for (const label of find("filters").querySelectorAll(".audit-filter")) label.hidden = view !== "audit";
  for (const label of find("filters").querySelectorAll(".alert-filter")) label.hidden = view !== "alerts";
  message("workspace-message");
  if (view !== "batch") {
    renderRows();
    loadPage();
  }
}

async function loadPage(append = false) {
  const revision = ++state.load;
  if (!append) {
    const fieldsByView = { assets: ["q", "account", "kind"], operations: ["account", "status"],
      alerts: ["alert_status", "rule"], audit: ["category", "account", "subject_id", "from", "until"] };
    const fields = new FormData(find("filters"));
    const filters = new URLSearchParams();
    for (const field of fieldsByView[state.view] || []) {
      const value = fields.get(field);
      if (!value) continue;
      if (field === "from" || field === "until") {
        const date = new Date(value);
        if (!Number.isFinite(date.valueOf())) {
          message("workspace-message", "请填写有效的时间。", true);
          return;
        }
        filters.set(field, date.toISOString());
      } else {
        filters.set(field === "alert_status" ? "status" : field, value);
      }
    }
    state.filters = filters.toString();
  }
  const query = new URLSearchParams(state.filters);
  query.set("limit", "30");
  const catalog = state.view === "service-catalog";
  if (catalog && state.project.role === "admin") query.set("include_disabled", "true");
  if (append && state.cursor) query.set(catalog || ["alerts", "console-grants"].includes(state.view) ? "after" : "cursor", state.cursor);
  const url = projectURL() + "/" + state.view + "?" + query;
  const more = find("more");
  more.disabled = true;
  message("workspace-message", "正在加载…");
  try {
    const page = await request(url);
    if (revision !== state.load) return;
    state.rows = append ? state.rows.concat(page.items) : page.items;
    state.cursor = page.next || "";
    renderRows();
    message("workspace-message", state.rows.length ? "" : "当前筛选没有结果。");
  } catch (error) {
    if (revision !== state.load) return;
    if (error.status === 401) return showEntry();
    message("workspace-message", error.message, true);
  } finally {
    if (revision === state.load) more.disabled = false;
  }
}

function rowOf(values) {
  const row = el("tr");
  for (const value of values) {
    const cell = el("td");
    if (value instanceof Node) cell.append(value);
    else cell.textContent = value ?? "—";
    row.append(cell);
  }
  return row;
}

function headings(labels) {
  const row = el("tr");
  for (const label of labels) {
    const cell = el("th", label);
    cell.scope = "col";
    row.append(cell);
  }
  return row;
}

function renderRows() {
  if (state.view === "audit") {
    renderAudit();
    return;
  }
  if (state.view === "service-catalog") {
    renderCatalog();
    return;
  }
  if (state.view === "alerts") {
    renderAlerts();
    return;
  }
  if (state.view === "console-grants") {
    renderNativeGrants();
    return;
  }
  const assetView = state.view === "assets";
  const selectable = assetView && canExecute();
  const cols = assetView ? ["资源", "类型", "账号 / 地域", "状态", "归属版本"] : ["动作", "资源 ID", "状态", "创建时间", "详情"];
  const native = selectable && nativeConsoleEnabled();
  const assetActions = native || (selectable && state.rows.some((asset) => canDeleteNetwork(asset) || canRevokeRule(asset) || canReleaseElasticIP(asset) || canUnbindElasticIP(asset) || canCreateSnapshot(asset) || canDeleteSnapshot(asset) || canDeleteImage(asset) || canCreateImage(asset)));
  if (assetActions) cols.push("操作");
  if (selectable) cols.unshift("选择");
  find("columns").replaceChildren(headings(cols));
  find("items").replaceChildren(...state.rows.map((item) => {
    if (!assetView) return rowOf([actions[item.action] || item.action, item.asset_id || "待分配", statusLabel(item.status),
      timestamp(item.created_at), button("查看", () => showOperation(item.id))]);
    const name = el("div", item.name || item.id);
    if (item.name && item.name !== item.id) name.append(el("span", item.id, "minor"));
    if (item.public_addresses?.length) name.append(el("span", item.public_addresses.join(" · "), "minor"));
    if (item.kind === "snapshot") {
      const source = item.parent_id ? "源卷 " + item.parent_id : "";
      const size = item.snapshot?.size_gib ? item.snapshot.size_gib + " GiB" : "";
      const details = [source, item.snapshot?.class, size].filter(Boolean);
      if (details.length) name.append(el("span", details.join(" · "), "minor"));
    }
    const fields = [name, kinds[item.kind] || item.kind, item.region ? item.account + " / " + item.region : item.account,
      inventoryStates[item.inventory_state] || item.status || item.inventory_state, item.ownership_revision];
    if (assetActions) {
      const openstack = state.profile.native_console && item.provider === "openstack" && /^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(item.id);
      const serial = state.profile.native_serial_console && item.provider === "aws-ec2" && /^i-(?:[a-f0-9]{8}|[a-f0-9]{17})$/.test(item.id);
      let action = "—";
      if (item.kind === "instance" && (openstack || serial)) action = button("申请访问", () => showNativeRequest(item, serial));
      if (canCreateSnapshot(item)) action = button("创建快照", () => showSnapshotCreation(item));
      if (canDeleteSnapshot(item)) {
        action = el("div", undefined, "row-actions");
        if (item.snapshot.class.toLowerCase() !== "archive") {
          action.append(button("恢复到新盘", () => showSnapshotRestore(item)), button("回滚原云盘", () => showSnapshotRollback(item)));
        }
        action.append(button("申请删除", () => showSnapshotDeletion(item)));
      }
      if (canCreateImage(item)) {
        const controls = el("div", undefined, "row-actions");
        if (action !== "—") controls.append(action);
        controls.append(button("制作系统盘镜像", () => showImageCreation(item)));
        action = controls;
      }
      if (canDeleteImage(item)) {
        action = el("div", undefined, "row-actions");
        action.append(button("申请复制镜像", () => showImageCopy(item)), button("申请删除镜像", () => showImageDeletion(item)));
      }
      if (canDeleteNetwork(item)) action = button(networkDeletionLabels[item.kind], () => showNetworkDeletion(item));
      if (canRevokeRule(item)) action = button("申请撤销", () => showSecurityRuleRevocation(item));
      if (canReleaseElasticIP(item)) {
        action = el("div", undefined, "row-actions");
        action.append(button("申请绑定", () => showElasticIPBinding(item, true)), button("申请释放", () => showElasticIPRelease(item)));
      }
      if (canUnbindElasticIP(item)) action = button("申请解绑", () => showElasticIPBinding(item, false));
      fields.push(action);
    }
    if (selectable) fields.unshift(selectAsset(item));
    return rowOf(fields);
  }));
  find("more").hidden = !state.cursor;
  showSelection();
}

const snapshotVolumeStates = { "tencent-cvm": ["UNATTACHED", "ATTACHED"], "alibaba-ecs": ["Available", "In_use"], "huawei-ecs": ["available", "in-use"] };

function canCreateSnapshot(asset) {
  return asset.kind === "volume" && asset.inventory_state === "observed" && asset.ownership_revision > 0 &&
    snapshotVolumeStates[asset.provider]?.includes(asset.status);
}

function showSnapshotCreation(asset) {
  const { panel, stamp } = openDetail("创建云盘快照");
  panel.append(el("p", `${asset.account} / ${asset.region} · ${asset.id} · 归属版本 ${asset.ownership_revision}`),
    el("p", "申请需独立审批。挂载中的卷支持崩溃一致性快照，应用可在提交前自行完成写入同步。", "hint"));
  const form = el("form", undefined, "catalog-form");
  const name = catalogInput(form, "name", "快照名称", "", 64);
  name.pattern = "[a-zA-Z][a-zA-Z0-9_\\-]{1,63}";
  const consent = el("label");
  const attached = el("input");
  attached.type = "checkbox";
  consent.append(attached, document.createTextNode("允许在云盘挂载时创建崩溃一致性快照"));
  form.append(consent);
  const target = projectURL() + "/snapshots";
  catalogSubmit(form, "提交快照申请", (submit) => {
    const body = JSON.stringify({ account: asset.account, region: asset.region, volume_id: asset.id,
      name: name.value.trim(), allow_attached: attached.checked });
    return submitNetworkOperation(target, body, asset.ownership_revision, submit, stamp);
  });
  panel.append(form);
}

const deletableSnapshotStates = { "tencent-cvm": "NORMAL", "alibaba-ecs": "accomplished", "huawei-ecs": "available" };
const deletableSnapshotClasses = { "tencent-cvm": ["private_snapshot"], "alibaba-ecs": ["standard", "flash", "archive"], "huawei-ecs": ["standard", "flash"] };

function canDeleteSnapshot(asset) {
  return asset.kind === "snapshot" && asset.parent_id && asset.inventory_state === "observed" && asset.ownership_revision > 0 &&
    deletableSnapshotStates[asset.provider] === asset.status &&
    deletableSnapshotClasses[asset.provider]?.includes(asset.snapshot?.class?.toLowerCase());
}

function showSnapshotDeletion(asset) {
  const { panel, stamp } = openDetail("申请删除云盘快照");
  panel.append(el("p", `${asset.account} / ${asset.region} · ${asset.name || asset.id} · ${asset.id}`),
    el("p", `源卷 ${asset.parent_id} · 归属版本 ${asset.ownership_revision} · ${asset.snapshot.class}`),
    el("p", "删除后无法通过此快照恢复数据。提交申请后需独立审批。", "hint"));
  const form = el("form", undefined, "catalog-form");
  const target = projectURL() + "/snapshots/delete";
  const body = JSON.stringify({ account: asset.account, region: asset.region, asset_id: asset.id, volume_id: asset.parent_id });
  catalogSubmit(form, "提交删除申请", (submit) => submitNetworkOperation(target, body, asset.ownership_revision, submit, stamp));
  panel.append(form);
}

function showSnapshotRollback(asset) {
  const { panel, stamp } = openDetail("申请回滚原云盘");
  panel.append(el("p", `${asset.account} / ${asset.region} · 快照 ${asset.id} · 原卷 ${asset.parent_id} · 归属版本 ${asset.ownership_revision}`),
    el("p", "回滚会覆盖原云盘上快照之后的全部写入。原卷须为未挂载的数据盘，提交后需独立审批。", "hint"));
  const form = el("form", undefined, "catalog-form");
  const consent = el("label");
  const confirm = el("input");
  confirm.type = "checkbox";
  confirm.required = true;
  consent.append(confirm, document.createTextNode("确认覆盖原云盘上的数据"));
  form.append(consent);
  const target = projectURL() + "/snapshots/rollback";
  catalogSubmit(form, "提交回滚申请", (submit) => {
    const body = JSON.stringify({ account: asset.account, region: asset.region, asset_id: asset.id, volume_id: asset.parent_id,
      confirm_overwrite: confirm.checked });
    return submitNetworkOperation(target, body, asset.ownership_revision, submit, stamp);
  });
  panel.append(form);
}

const restoreDiskTypes = {
  "tencent-cvm": ["CLOUD_PREMIUM", "CLOUD_SSD", "CLOUD_BSSD"],
  "alibaba-ecs": ["cloud_essd", "cloud_ssd", "cloud_efficiency"],
  "huawei-ecs": ["GPSSD", "SSD", "SAS"]
};

function showSnapshotRestore(asset) {
  const { panel, stamp } = openDetail("从快照恢复到新盘");
  panel.append(el("p", `${asset.account} / ${asset.region} · 快照 ${asset.id} · 源卷 ${asset.parent_id} · 归属版本 ${asset.ownership_revision}`),
    el("p", "恢复将创建一块独立的按需计费数据盘。提交后需独立审批。", "hint"));
  const form = el("form", undefined, "catalog-form");
  const name = catalogInput(form, "name", "新云盘名称", "", 64);
  name.pattern = "[a-zA-Z][a-zA-Z0-9_\\-]{1,63}";
  const zone = catalogInput(form, "zone", "目标可用区", "", 64);
  const size = catalogInput(form, "size_gib", "容量（GiB）", Math.max(10, asset.snapshot?.size_gib || 0), 5);
  size.type = "number";
  size.min = Math.max(10, asset.snapshot?.size_gib || 0);
  size.max = 32768;
  const diskLabel = el("label", "目标盘型");
  const diskType = el("select");
  diskType.required = true;
  diskType.append(el("option", "请选择盘型"));
  diskType.firstChild.value = "";
  for (const type of restoreDiskTypes[asset.provider]) {
    const option = el("option", type);
    option.value = type;
    diskType.append(option);
  }
  diskLabel.append(diskType);
  const encryptedLabel = el("label");
  const encrypted = el("input");
  encrypted.type = "checkbox";
  encryptedLabel.append(encrypted, document.createTextNode("源快照已加密"));
  form.append(diskLabel, encryptedLabel);
  catalogSubmit(form, "提交恢复申请", (submit) => {
    const body = JSON.stringify({ account: asset.account, region: asset.region, asset_id: asset.id, volume_id: asset.parent_id,
      name: name.value.trim(), zone: zone.value.trim(), disk_type: diskType.value, size_gib: size.valueAsNumber, encrypted: encrypted.checked });
    return submitNetworkOperation(projectURL() + "/snapshots/restore", body, asset.ownership_revision, submit, stamp);
  });
  panel.append(form);
}

const networkDeletionLabels = { network: "申请删除 VPC", subnet: "申请删除子网", "security-group": "申请删除安全组" };

function canDeleteNetwork(asset) {
  return Object.hasOwn(networkDeletionLabels, asset.kind) && ["tencent-cvm", "alibaba-ecs", "huawei-ecs"].includes(asset.provider) &&
    asset.inventory_state === "observed" && asset.ownership_revision > 0;
}

function showNetworkDeletion(asset) {
  const { panel, stamp } = openDetail(networkDeletionLabels[asset.kind]);
  panel.append(el("p", asset.account + " / " + asset.region + " · " + (asset.name || asset.id) + " · " + asset.id),
    el("p", "归属版本 " + asset.ownership_revision),
    el("p", "删除不可恢复。存在关联资源时会阻止删除，提交后需独立审批。", "hint"));
  const form = el("form", undefined, "catalog-form");
  const target = projectURL() + "/network-operations/delete";
  const payload = { kind: asset.kind, account: asset.account, region: asset.region, asset_id: asset.id };
  let confirmRules;
  if (asset.kind === "security-group") {
    const label = el("label");
    confirmRules = el("input");
    confirmRules.type = "checkbox";
    confirmRules.required = true;
    label.append(confirmRules, document.createTextNode("确认同时删除安全组内的所有规则"));
    form.append(label);
  }
  catalogSubmit(form, "提交删除申请", (submit) => {
    if (confirmRules) payload.confirm_rule_removal = confirmRules.checked;
    return submitNetworkOperation(target, JSON.stringify(payload), asset.ownership_revision, submit, stamp);
  });
  panel.append(form);
}

function canRevokeRule(asset) {
  return asset.kind === "security-group-rule" && asset.parent_id && asset.inventory_state === "observed";
}

const unboundElasticIP = { "tencent-cvm": "UNBIND", "alibaba-ecs": "Available", "huawei-ecs": "DOWN" };
const boundElasticIP = { "tencent-cvm": "BIND", "alibaba-ecs": "InUse", "huawei-ecs": "ACTIVE" };

function observedIPv4ElasticIP(asset) {
  return asset.kind === "elastic-ip" && asset.inventory_state === "observed" && asset.ownership_revision > 0 &&
    asset.public_addresses?.length === 1 && /^\d+\.\d+\.\d+\.\d+$/.test(asset.public_addresses[0]);
}

function canReleaseElasticIP(asset) {
  return observedIPv4ElasticIP(asset) && unboundElasticIP[asset.provider] === asset.status;
}

function canUnbindElasticIP(asset) {
  return observedIPv4ElasticIP(asset) && boundElasticIP[asset.provider] === asset.status;
}

function showElasticIPBinding(asset, bind) {
  const action = bind ? "绑定" : "解绑";
  const { panel, stamp } = openDetail("申请" + action + "弹性公网 IP");
  const address = asset.public_addresses[0];
  panel.append(el("p", `${asset.account} / ${asset.region} · ${asset.id} · ${address}`),
    el("p", bind ? "绑定会为目标实例配置公网地址，提交后需独立审批。"
      : "解绑会断开该地址与实例的公网关联，地址仍保留在账号内。提交后需独立审批。", "hint"));
  const form = el("form", undefined, "catalog-form");
  const instance = catalogInput(form, "instance_id", bind ? "目标实例 ID" : "当前绑定实例 ID", "", 128);
  const verifiedLabel = el("p", "", "hint");
  verifiedLabel.setAttribute("role", "status");
  let verified;
  let lookupVersion = 0;
  instance.addEventListener("input", () => {
    lookupVersion++;
    verified = undefined;
    verifiedLabel.textContent = "";
  });
  const lookup = button("核对实例", async () => {
    const id = instance.value.trim();
    if (!id) return message("detail-message", "请填写实例 ID。", true);
    const version = ++lookupVersion;
    verified = undefined;
    verifiedLabel.textContent = "";
    message("detail-message");
    lookup.disabled = true;
    try {
      const query = new URLSearchParams({ kind: "instance", account: asset.account, region: asset.region, q: id, limit: "100" });
      const page = await request(projectURL() + "/assets?" + query);
      if (stamp !== state.detail || version !== lookupVersion) return;
      verified = page.items.find((item) => item.id === id && item.inventory_state === "observed" && item.ownership_revision > 0);
      if (!verified) return message("detail-message", "当前项目和账号中找不到可操作的实例。", true);
      verifiedLabel.textContent = `${verified.name || verified.id} · 归属版本 ${verified.ownership_revision}`;
    } catch (error) {
      if (stamp === state.detail && version === lookupVersion) message("detail-message", error.message, true);
    } finally {
      lookup.disabled = false;
    }
  });
  form.append(lookup, verifiedLabel);
  let port;
  if (asset.provider === "huawei-ecs") {
    port = catalogInput(form, "port_id", "网卡端口 ID", "", 36);
    port.pattern = "[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}";
  }
  const target = projectURL() + "/network-operations/" + (bind ? "bind-elastic-ip" : "unbind-elastic-ip");
  catalogSubmit(form, "提交" + action + "申请", (submit) => {
    if (!verified || verified.id !== instance.value.trim()) return message("detail-message", "请先核对实例。", true);
    const body = { account: asset.account, region: asset.region, asset_id: asset.id, public_address: address,
      instance_id: verified.id, instance_revision: verified.ownership_revision };
    if (port) body.port_id = port.value;
    return submitNetworkOperation(target, JSON.stringify(body), asset.ownership_revision, submit, stamp);
  });
  panel.append(form);
}

function showElasticIPRelease(asset) {
  const { panel, stamp } = openDetail("申请释放弹性公网 IP");
  const address = asset.public_addresses[0];
  panel.append(el("p", `${asset.account} / ${asset.region} · ${asset.id} · ${address}`),
    el("p", "释放后可能无法取回原地址。提交后由独立审批人审核，执行前还会复核绑定状态。", "hint"));
  const form = el("form", undefined, "catalog-form");
  const target = projectURL() + "/network-operations/release-elastic-ip";
  const body = JSON.stringify({ account: asset.account, region: asset.region, asset_id: asset.id, public_address: address });
  catalogSubmit(form, "提交释放申请", (submit) => submitNetworkOperation(target, body, asset.ownership_revision, submit, stamp));
  panel.append(form);
}

const stoppedImageSources = { "tencent-cvm": "STOPPED", "alibaba-ecs": "Stopped", "huawei-ecs": "SHUTOFF" };

function canCreateImage(asset) {
  return asset.kind === "instance" && asset.inventory_state === "observed" && asset.ownership_revision > 0 &&
    Object.hasOwn(stoppedImageSources, asset.provider) && stoppedImageSources[asset.provider] === asset.status;
}

function showImageCreation(asset) {
  const { panel, stamp } = openDetail("制作系统盘镜像");
  panel.append(el("p", `${asset.account} / ${asset.region} · ${asset.id} · 归属版本 ${asset.ownership_revision}`),
    el("p", "源实例须保持关机。申请需独立审批。阿里云源实例须只挂载系统云盘。", "hint"));
  const form = el("form", undefined, "catalog-form");
  const name = catalogInput(form, "name", "镜像名称", "", 60);
  name.pattern = "[a-zA-Z][a-zA-Z0-9_\\-]{1,59}";
  const target = projectURL() + "/images";
  catalogSubmit(form, "提交镜像制作申请", (submit) => {
    const body = JSON.stringify({ account: asset.account, region: asset.region, instance_id: asset.id, name: name.value.trim() });
    return submitNetworkOperation(target, body, asset.ownership_revision, submit, stamp);
  });
  panel.append(form);
}

const deletableImageStates = { "tencent-cvm": "NORMAL", "alibaba-ecs": "Available", "huawei-ecs": "active" };

function canDeleteImage(asset) {
  return asset.kind === "image" && asset.inventory_state === "observed" && asset.ownership_revision > 0 &&
    deletableImageStates[asset.provider] === asset.status && Object.hasOwn(deletableImageStates, asset.provider);
}

function showImageCopy(asset) {
  const { panel, stamp } = openDetail("申请跨地域复制镜像");
  panel.append(el("p", `${asset.account} / ${asset.region} · ${asset.id} · 归属版本 ${asset.ownership_revision}`),
    el("p", "目标账号和源镜像均须归当前项目，提交后需独立审批。", "hint"));
  const form = el("form", undefined, "catalog-form");
  const name = catalogInput(form, "name", "目标镜像名称", "", 60);
  name.pattern = "[a-zA-Z][a-zA-Z0-9_\\-]{1,59}";
  const region = catalogInput(form, "destination_region", "目标地域", "", 40);
  const targetAccount = asset.provider === "huawei-ecs" ?
    catalogInput(form, "destination_account", "目标云账号", "", 128) : null;
  catalogSubmit(form, "提交镜像复制申请", (submit) => {
    if (region.value.trim() === asset.region) return message("detail-message", "请选择与源镜像不同的地域。", true);
    const body = JSON.stringify({ account: asset.account, region: asset.region, asset_id: asset.id, name: name.value.trim(),
      destination_region: region.value.trim(), destination_account: targetAccount ? targetAccount.value.trim() : asset.account });
    return submitNetworkOperation(projectURL() + "/images/copy", body, asset.ownership_revision, submit, stamp);
  });
  panel.append(form);
}

function showImageDeletion(asset) {
  const { panel, stamp } = openDetail("申请删除自有镜像");
  panel.append(el("p", asset.name || asset.id),
    el("p", `${asset.account} / ${asset.region} · ${asset.id} · 归属版本 ${asset.ownership_revision}`),
    el("p", "删除后无法再使用该镜像创建实例。提交后需独立审批，关联快照和备份会保留。", "hint"));
  const form = el("form", undefined, "catalog-form");
  const target = projectURL() + "/images/delete";
  const body = JSON.stringify({ account: asset.account, region: asset.region, asset_id: asset.id });
  catalogSubmit(form, "提交镜像删除申请", (submit) => submitNetworkOperation(target, body, asset.ownership_revision, submit, stamp));
  panel.append(form);
}

function showSecurityRuleRevocation(asset) {
  const { panel, stamp } = openDetail("申请撤销安全组规则");
  panel.append(el("p", asset.name), el("p", `${asset.account} / ${asset.region} · ${asset.parent_id} · ${asset.id}`));
  const form = el("form", undefined, "catalog-form");
  const target = projectURL() + "/network-operations/revoke-rule";
  const body = JSON.stringify({ account: asset.account, region: asset.region, parent_id: asset.parent_id, rule_id: asset.id });
  catalogSubmit(form, "提交撤销申请", (submit) => submitNetworkOperation(target, body, asset.ownership_revision, submit, stamp));
  panel.append(form);
}

async function submitNetworkOperation(target, body, match, submit, stamp) {
  if (stamp !== state.detail) return;
  const identity = JSON.stringify([target, body, match]);
  if (!state.networkAttempts.has(identity)) state.networkAttempts.set(identity, crypto.randomUUID());
  submit.disabled = true;
  try {
    const receipt = await request(target, { method: "POST", body, match, key: state.networkAttempts.get(identity) });
    state.networkAttempts.delete(identity);
    if (stamp === state.detail) showOperation(receipt.id);
  } catch (error) {
    if (stamp === state.detail) message("detail-message", error.message, true);
  } finally {
    submit.disabled = false;
  }
}

function showElasticIPAllocation() {
  const { panel, stamp } = openDetail("申请弹性公网 IP");
  panel.append(el("p", "申请一个未绑定的 IPv4 地址，按流量后付费，需独立审批。", "hint"));
  const form = el("form", undefined, "catalog-form");
  const account = catalogInput(form, "account", "账号", new URLSearchParams(state.filters).get("account"), 128);
  const region = catalogInput(form, "region", "地域", "", 128);
  const name = catalogInput(form, "name", "名称", "", 64);
  name.pattern = "[a-zA-Z0-9][a-zA-Z0-9_.\\-]{0,63}";
  const bandwidth = catalogInput(form, "bandwidth_mbps", "带宽上限（Mbps）", "", 3);
  bandwidth.type = "number";
  bandwidth.min = 5;
  bandwidth.max = 200;
  bandwidth.step = 1;
  const pool = catalogInput(form, "address_pool", "地址池（仅华为云必填）", "", 64);
  pool.required = false;
  pool.pattern = name.pattern;
  const target = projectURL() + "/network-operations";
  catalogSubmit(form, "提交申请", (submit) => {
    const body = { kind: "elastic-ip", account: account.value.trim(), region: region.value.trim(),
      name: name.value, bandwidth_mbps: bandwidth.valueAsNumber };
    if (pool.value) body.address_pool = pool.value;
    submitNetworkOperation(target, JSON.stringify(body), undefined, submit, stamp);
  });
  panel.append(form);
}

function renderCatalog() {
  find("columns").replaceChildren(headings(["模板", "版本", "账号 / 地域", "规格", "状态", "操作"]));
  find("items").replaceChildren(...state.rows.map((item) => {
    const title = el("div", item.title);
    title.append(el("span", item.id, "minor"));
    const action = button("查看", () => showCatalogTemplate(item));
    return rowOf([title, item.version, item.account + " / " + item.region, item.size, item.enabled ? "启用" : "已停用", action]);
  }));
  find("more").hidden = !state.cursor;
  showSelection();
}

function openDetail(title) {
  const stamp = ++state.detail;
  const dialog = find("detail");
  const panel = find("detail-body");
  panel.replaceChildren();
  find("detail-title").textContent = title;
  message("detail-message");
  if (!dialog.open) dialog.showModal();
  return { panel, stamp };
}

function catalogInput(form, name, label, value, maxLength) {
  const field = el("label", label);
  const input = el("input");
  input.name = name;
  input.value = value || "";
  input.required = true;
  input.maxLength = maxLength;
  field.append(input);
  form.append(field);
  return input;
}

function catalogSubmit(form, label, handler) {
  const controls = el("div", undefined, "row-actions");
  const submit = el("button", label, "primary");
  controls.append(submit);
  form.append(controls);
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    handler(submit);
  });
  return controls;
}

function showCatalogTemplate(item) {
  const { panel, stamp } = openDetail(item.title);
  const base = projectURL() + "/service-catalog/" + encodeURIComponent(item.id);
  const list = el("dl", undefined, "detail-list");
  const values = [["模板 ID", item.id], ["发布版本", item.version], ["状态", item.enabled ? "启用" : "已停用"],
    ...Object.entries(catalogFields).slice(1).map(([name, label]) => [label, item[name]]),
    ["发布时间", timestamp(item.published_at)]];
  for (const [label, value] of values) list.append(el("dt", label), el("dd", value));
  panel.append(list);
  if (canExecute() && item.enabled) {
    const launch = el("form", undefined, "catalog-form");
    const name = catalogInput(launch, "name", "实例名称", "", 80);
    name.pattern = "[a-zA-Z0-9][a-zA-Z0-9 _.:\\-]{0,79}";
    catalogSubmit(launch, "提交实例申请", (submit) => {
      const body = JSON.stringify({ version: item.version, name: name.value.trim() });
      const target = base + "/launch";
      const lookup = target + " " + body;
      let attempt = state.catalogAttempts.get(lookup);
      if (!attempt) {
        attempt = { target, project: projectURL(), body, key: crypto.randomUUID(), lookup };
        state.catalogAttempts.set(lookup, attempt);
      }
      sendCatalogLaunch(attempt, submit, stamp);
    });
    panel.append(launch);
  }
  if (state.project.role === "admin") {
    const controls = el("div", undefined, "row-actions");
    controls.append(button("发布新版本", () => showCatalogEditor(item)), button("查看历史版本", () => showCatalogHistory(item, base)));
    if (item.enabled) {
      const retire = button("停用模板", async () => {
        if (!window.confirm("停用后将停止受理使用此模板的新申请。确定停用？")) return;
        retire.disabled = true;
        try {
          const saved = await request(base, { method: "DELETE", match: item.revision });
          if (stamp !== state.detail) return;
          loadPage();
          showCatalogTemplate(saved);
        } catch (error) {
          if (stamp === state.detail) message("detail-message", error.message, true);
        } finally {
          retire.disabled = false;
        }
      });
      controls.append(retire);
    }
    panel.append(controls);
  }
}

function showCatalogEditor(item) {
  const { panel, stamp } = openDetail(item ? "发布新版本" : "发布模板");
  const form = el("form", undefined, "catalog-form");
  if (!item) {
    const id = catalogInput(form, "id", "模板 ID", "", 64);
    id.pattern = "[a-z0-9][a-z0-9\\-]{0,63}";
  }
  for (const [field, label] of Object.entries(catalogFields)) {
    catalogInput(form, field, label, item?.[field], field === "title" ? 120 : 128);
  }
  const target = projectURL() + "/service-catalog" + (item ? "/" + encodeURIComponent(item.id) : "");
  catalogSubmit(form, "确认发布", async (submit) => {
    submit.disabled = true;
    try {
      const body = JSON.stringify(Object.fromEntries(new FormData(form)));
      const saved = await request(target, { body, method: item ? "PUT" : "POST", match: item?.revision });
      if (stamp !== state.detail) return;
      loadPage();
      showCatalogTemplate(saved);
    } catch (error) {
      if (stamp === state.detail) message("detail-message", error.message, true);
    } finally {
      submit.disabled = false;
    }
  });
  panel.append(form);
}

function showCatalogHistory(item, base) {
  const { panel, stamp } = openDetail("模板历史版本");
  const form = el("form", undefined, "catalog-form");
  const version = catalogInput(form, "version", "版本号", String(item.version), 19);
  version.pattern = "[1-9][0-9]*";
  version.inputMode = "numeric";
  const result = el("dl", undefined, "detail-list");
  catalogSubmit(form, "查看版本", async (submit) => {
    submit.disabled = true;
    try {
      const saved = await request(base + "/versions/" + encodeURIComponent(version.value));
      if (stamp !== state.detail) return;
      message("detail-message");
      result.replaceChildren(el("dt", "发布人"), el("dd", saved.published_by),
        ...Object.entries(catalogFields).flatMap(([name, label]) => [el("dt", label), el("dd", saved[name])]));
    } catch (error) {
      if (stamp === state.detail) {
        result.replaceChildren();
        message("detail-message", error.message, true);
      }
    } finally {
      submit.disabled = false;
    }
  });
  panel.append(form, result);
}

function pendingCatalogLaunch() {
  if (state.view !== "service-catalog" || !state.project) return undefined;
  const project = projectURL();
  return Array.from(state.catalogAttempts.values()).find((attempt) => attempt.project === project && attempt.uncertain);
}

function showCatalogRetry() {
  find("catalog-retry").hidden = !pendingCatalogLaunch();
}

function retryPendingCatalogLaunch() {
  const attempt = pendingCatalogLaunch();
  if (!attempt) return;
  const { panel, stamp } = openDetail("重试实例申请");
  const retry = button("使用原幂等键重试", () => sendCatalogLaunch(attempt, retry, stamp));
  panel.append(el("p", JSON.parse(attempt.body).name), retry);
}

async function sendCatalogLaunch(attempt, submit, stamp) {
  if (attempt.running) return;
  attempt.running = true;
  submit.disabled = true;
  try {
    const receipt = await request(attempt.target, { method: "POST", body: attempt.body, key: attempt.key });
    if (typeof receipt?.id !== "string" || !receipt.id) throw new Error("暂时无法确认申请收据，可使用原幂等键重试。");
    state.catalogAttempts.delete(attempt.lookup);
    if (stamp === state.detail) showOperation(receipt.id);
  } catch (error) {
    attempt.uncertain = !error.status || error.status >= 500;
    if (stamp === state.detail) message("detail-message", error.message, true);
  } finally {
    attempt.running = false;
    submit.disabled = false;
    showCatalogRetry();
  }
}

function renderNativeGrants() {
  find("columns").replaceChildren(headings(["实例", "用途", "申请人", "状态", "有效期", "详情"]));
  find("items").replaceChildren(...state.rows.map((item) => rowOf([item.asset_id, grantPurposes[item.purpose],
    item.requester, statusLabel(item.status), timestamp(item.expires_at), button("查看", () => showNativeGrant(item.id))])));
  find("more").hidden = !state.cursor;
  showSelection();
}

function nativeConsoleEnabled() {
  return state.profile?.native_console || state.profile?.native_serial_console;
}

function serialPublicKeyField(form) {
  const label = el("label", "OpenSSH RSA 公钥（.pub）");
  const input = el("textarea");
  input.rows = 3;
  input.maxLength = 4096;
  input.required = true;
  input.spellcheck = false;
  label.append(input);
  form.append(label);
  return input;
}

function showNativeRequest(asset, serial) {
  const { panel, stamp } = openDetail("申请原生控制台");
  panel.append(el("p", asset.name || asset.id));
  const form = el("form", undefined, "catalog-form");
  const label = el("label", "用途");
  const purpose = el("select");
  for (const [value, text] of Object.entries(grantPurposes)) {
    const option = el("option", text);
    option.value = value;
    purpose.append(option);
  }
  label.append(purpose);
  form.append(label);
  const publicKey = serial ? serialPublicKeyField(form) : null;
  const attempts = new Map();
  const target = projectURL() + "/console-grants";
  catalogSubmit(form, "提交访问申请", async (submit) => {
    const payload = { account: asset.account, region: asset.region, asset_id: asset.id, purpose: purpose.value };
    if (publicKey) payload.ssh_public_key = publicKey.value.trim();
    const body = JSON.stringify(payload);
    if (!attempts.has(body)) attempts.set(body, crypto.randomUUID());
    submit.disabled = true;
    try {
      const receipt = await request(target, { method: "POST", key: attempts.get(body), match: asset.ownership_revision, body });
      if (stamp === state.detail) showNativeGrant(receipt.id);
    } catch (error) {
      if (stamp === state.detail) message("detail-message", error.message, true);
    } finally {
      submit.disabled = false;
    }
  });
  panel.append(form);
}

async function showNativeGrant(id) {
  const { panel, stamp } = openDetail("原生控制台访问");
  const target = projectURL() + "/console-grants/" + encodeURIComponent(id);
  message("detail-message", "正在加载…");
  try {
    const item = await request(target);
    if (stamp !== state.detail) return;
    message("detail-message");
    const fields = [["实例", item.asset_id], ["账号 / 地域", item.account + " / " + item.region],
      ["用途", grantPurposes[item.purpose]], ["申请人", item.requester], ["审批人", item.reviewer || "—"],
      ["状态", item.provider === "aws-ec2" && item.status === "in_use" ? "正在发放" : statusLabel(item.status)],
      ["有效期", timestamp(item.expires_at)]];
    if (item.key_fingerprint) fields.push(["公钥指纹", item.key_fingerprint]);
    if (item.provider_request_id) fields.push(["AWS 请求 ID", item.provider_request_id]);
    const list = el("dl", undefined, "detail-list");
    for (const [label, value] of fields) {
      const entry = el("dd");
      entry.append(value);
      list.append(el("dt", label), entry);
    }
    panel.append(list, nativeGrantControls(item, target, stamp));
  } catch (error) {
    if (stamp === state.detail) message("detail-message", error.message, true);
  }
}

function nativeGrantControls(item, target, stamp) {
  const controls = el("div", undefined, "row-actions");
  const send = async (suffix, decision) => {
    for (const control of controls.querySelectorAll("button")) control.disabled = true;
    try {
      await request(target + "/" + suffix, { method: "POST", match: item.revision,
        body: decision ? JSON.stringify({ decision }) : undefined });
      if (stamp !== state.detail) return;
      if (state.view === "console-grants") loadPage();
      showNativeGrant(item.id);
    } catch (error) {
      if (stamp === state.detail) message("detail-message", error.message, true);
    } finally {
      for (const control of controls.querySelectorAll("button")) control.disabled = false;
    }
  };
  const own = item.requester === state.profile.principal;
  if (item.status === "awaiting_approval" && state.project.role === "approver" && !own) {
    controls.append(button("批准访问", () => send("approval", "approve")), button("拒绝访问", () => send("approval", "reject")));
  }
  if (own && item.status === "approved" && item.provider === "openstack" && state.profile.native_console) {
    const link = el("a", "打开 noVNC");
    link.href = "/console-assets/novnc/vnc.html#" + new URLSearchParams({ path: target + "/stream", autoconnect: "1", reconnect: "0" });
    link.target = "_blank";
    link.rel = "noopener noreferrer";
    controls.append(link);
  }
  if (own && item.status === "approved" && item.provider === "aws-ec2" && state.profile.native_serial_console) {
    controls.append(serialActivationForm(item, target, stamp));
  }
  const revocable = ["awaiting_approval", "approved"].includes(item.status) || (item.provider === "openstack" && item.status === "in_use");
  if ((own || state.project.role === "admin") && revocable) {
    controls.append(button("撤销访问", () => send("revoke")));
  }
  return controls;
}

function serialActivationForm(item, target, stamp) {
  const form = el("form", undefined, "catalog-form");
  form.append(el("p", "发放后请立即使用对应的本地私钥连接。"));
  const key = serialPublicKeyField(form);
  catalogSubmit(form, "发放串口公钥", async (submit) => {
    submit.disabled = true;
    try {
      const result = await request(target + "/serial-key", { method: "POST", match: item.revision,
        body: JSON.stringify({ ssh_public_key: key.value.trim() }) });
      if (stamp !== state.detail) return;
      if (state.view === "console-grants") loadPage();
      const { host, username, port, valid_until: until } = result.ssh;
      const { panel } = openDetail("AWS 串口公钥已发放");
      panel.append(el("p", "建连截止 " + new Date(until).toLocaleTimeString("zh-CN")),
        el("code", "ssh -o StrictHostKeyChecking=yes -o IdentitiesOnly=yes -i 本地私钥 -p " + port + " " + username + "@" + host));
    } catch (error) {
      if (stamp === state.detail) message("detail-message", error.message, true);
    } finally {
      submit.disabled = false;
    }
  });
  return form;
}

function renderAlerts() {
  find("columns").replaceChildren(headings(["触发时间", "规则", "账号 / 地域", "对象", "状态", "详情"]));
  find("items").replaceChildren(...state.rows.map((item) => rowOf([
    timestamp(item.opened_at), alertRules[item.rule], item.region ? item.account + " / " + item.region : item.account,
    item.rule.startsWith("operation_") ? item.subject_id : (kinds[item.resource_kind] || item.resource_kind),
    statusLabel(item.status), button("查看", () => showAlert(item.id))
  ])));
  find("more").hidden = !state.cursor;
  showSelection();
}

async function showAlert(id) {
  const { panel, stamp } = openDetail("告警详情");
  const target = projectURL() + "/alerts/" + encodeURIComponent(id);
  message("detail-message", "正在加载…");
  try {
    const alert = await request(target);
    if (stamp !== state.detail) return;
    message("detail-message");
    const list = el("dl", undefined, "detail-list");
    const fields = [["规则", alertRules[alert.rule]], ["状态", statusLabel(alert.status)], ["触发时间", timestamp(alert.opened_at)],
      ["触发状态", statuses[alert.source_status] || alert.source_status], ["云账号", alert.account], ["地域", alert.region || "全局"],
      ["资源类型", kinds[alert.resource_kind] || alert.resource_kind], ["对象 ID", alert.subject_id]];
    if (alert.acknowledged_at) fields.push(["确认人", alert.acknowledged_by], ["确认时间", timestamp(alert.acknowledged_at)]);
    if (alert.resolved_at) fields.push(["恢复时间", timestamp(alert.resolved_at)]);
    for (const [label, value] of fields) {
      const entry = el("dd");
      entry.append(value);
      list.append(el("dt", label), entry);
    }
    const controls = el("div", undefined, "row-actions");
    if (alert.rule.startsWith("operation_")) controls.append(button("查看关联操作", () => showOperation(alert.subject_id)));
    if (alert.status === "open" && state.project.role === "admin") {
      const acknowledge = button("确认告警", async () => {
        acknowledge.disabled = true;
        try {
          await request(target + "/acknowledgement", { method: "POST", match: alert.revision });
          if (stamp !== state.detail) return;
          loadPage();
          showAlert(id);
        } catch (error) {
          if (stamp === state.detail) message("detail-message", error.message, true);
        } finally {
          acknowledge.disabled = false;
        }
      });
      controls.append(acknowledge);
    }
    panel.append(list, controls);
  } catch (error) {
    if (stamp === state.detail) message("detail-message", error.message, true);
  }
}

function renderAudit() {
  find("columns").replaceChildren(headings(["时间", "事件", "动作", "对象", "主体", "结果"]));
  find("items").replaceChildren(...state.rows.map((item) => {
    let subject = el("div", item.subject_id);
    if (["operation", "approval", "resolution", "observation"].includes(item.category)) {
      subject = button(item.subject_id, () => showOperation(item.subject_id));
    } else if (item.category === "alert") {
      subject = button(item.subject_id, () => showAlert(item.subject_id));
    } else if (item.category === "console_access" && nativeConsoleEnabled()) {
      subject = button(item.subject_id, () => showNativeGrant(item.subject_id));
    }
    const target = el("div");
    target.append(subject);
    if (item.related_project) target.append(el("span", "关联项目 " + item.related_project, "minor"));
    return rowOf([timestamp(item.occurred_at), auditKinds[item.category] || item.category,
      auditActions[item.action] || actions[item.action] || statuses[item.action] || item.action, target,
      item.actor || "系统", auditOutcomes[item.outcome] || grantPurposes[item.outcome] || alertRules[item.outcome] || statuses[item.outcome] || item.outcome || "—"]);
  }));
  find("more").hidden = !state.cursor;
  showSelection();
}

async function exportAudit() {
  const previous = state.load;
  const control = find("audit-export");
  control.disabled = true;
  try {
    const filters = state.filters ? "?" + state.filters : "";
    const blob = await request(projectURL() + "/audit/export" + filters, { csv: true });
    if (previous !== state.load || state.view !== "audit") return;
    const link = el("a");
    link.href = URL.createObjectURL(blob);
    link.download = "project-audit.csv";
    document.body.append(link);
    link.click();
    link.remove();
    setTimeout(() => URL.revokeObjectURL(link.href), 1000);
  } catch (error) {
    if (previous === state.load) message("workspace-message", error.message, true);
  } finally {
    control.disabled = false;
  }
}

function selectAsset(asset) {
  if (asset.kind !== "instance") return "—";
  const key = [asset.account, asset.region, asset.id].join("\0");
  const select = el("input");
  select.type = "checkbox";
  select.setAttribute("aria-label", "选择 " + (asset.name || asset.id));
  select.checked = state.selected.has(key);
  select.addEventListener("change", () => {
    if (!select.checked) state.selected.delete(key);
    else if (state.selected.size < 25) state.selected.set(key, asset);
    else {
      select.checked = false;
      message("workspace-message", "每批最多选择 25 个实例。", true);
    }
    showSelection();
  });
  return select;
}

function showSelection() {
  find("selected").hidden = state.view !== "assets" || state.selected.size === 0;
  find("selected").textContent = "批量操作（" + state.selected.size + "）";
}

function resetBatch() {
  state.batch = null;
  find("submit-batch").disabled = true;
  find("batch-report").hidden = true;
}

function fillBatch() {
  resetBatch();
  const action = find("batch-action").value;
  const items = Array.from(state.selected.values(), (asset) => ({
    account: asset.account, region: asset.region, asset_id: asset.id, ownership_revision: asset.ownership_revision, action,
  }));
  find("batch-input").value = JSON.stringify({ items }, null, 2);
}

function showBatchReceipt(receipt) {
  const report = find("batch-report");
  report.replaceChildren(el("h2", "批次 " + receipt.id));
  const list = el("ul");
  for (const op of receipt.operations) {
    const item = el("li");
    item.append(button((actions[op.action] || op.action) + " " + (op.asset_id || op.id), () => showOperation(op.id)), " ", statusLabel(op.status));
    list.append(item);
  }
  report.append(list);
  report.hidden = false;
}

async function previewBatch() {
  const payload = find("batch-input").value;
  const base = projectURL();
  const old = state.batch?.payload === payload ? state.batch : null;
  find("submit-batch").disabled = true;
  find("preview-batch").disabled = true;
  try {
    JSON.parse(payload);
  } catch {
    message("workspace-message", "请填写有效的 JSON。", true);
    find("preview-batch").disabled = false;
    return;
  }
  try {
    const preview = await request(base + "/operation-batches/preview", { method: "POST", body: payload });
    if (!state.project || base !== projectURL() || payload !== find("batch-input").value) return;
    state.batch = { payload, base, key: old?.key || crypto.randomUUID(), ready: preview.can_submit || !!old?.uncertain, uncertain: !!old?.uncertain };
    const report = find("batch-report");
    report.replaceChildren(el("h2", "预览结果"), el("p", "剩余活动操作配额 " + preview.remaining, "hint"));
    const list = el("ol");
    for (const item of preview.items) {
      const summary = el("li", (reasons[item.reason] || item.reason) + (item.approval_required ? "，需要审批" : ""));
      const revisions = [];
      if (item.ownership_revision) revisions.push("实例版本 " + item.ownership_revision);
      if (item.volume_revision) revisions.push("卷版本 " + item.volume_revision);
      if (revisions.length) summary.append(el("span", revisions.join(" / "), "minor"));
      list.append(summary);
    }
    report.append(list);
    report.hidden = false;
    find("submit-batch").disabled = !state.batch.ready;
    if (state.batch.uncertain) message("workspace-message", "上次提交结果待确认。可用原幂等键重取收据。");
    else message("workspace-message", preview.can_submit ? "预览通过，可以提交。" : "请处理阻塞项或缩小批次。", !preview.can_submit);
  } catch (error) {
    message("workspace-message", error.message, true);
  } finally {
    find("preview-batch").disabled = false;
  }
}

async function submitBatch() {
  const batch = state.batch;
  if (!batch?.ready || batch.payload !== find("batch-input").value || batch.base !== projectURL()) return;
  find("submit-batch").disabled = true;
  try {
    const receipt = await request(batch.base + "/operation-batches", { method: "POST", body: batch.payload, key: batch.key });
    if (state.batch !== batch) return;
    batch.ready = false;
    find("batch-id").value = receipt.id;
    state.selected.clear();
    find("fill-batch").hidden = true;
    showBatchReceipt(receipt);
    message("workspace-message", "批次已受理，请查看各项状态。");
  } catch (error) {
    if (state.batch !== batch) return;
    batch.uncertain = !error.status || error.status >= 500;
    message("workspace-message", error.message, true);
    find("submit-batch").disabled = false;
  }
}

async function showOperation(id) {
  const stamp = ++state.detail;
  const dialog = find("detail");
  const target = projectURL() + "/operations/" + encodeURIComponent(id);
  find("detail-title").textContent = "操作详情";
  find("detail-body").replaceChildren();
  message("detail-message", "正在加载…");
  if (!dialog.open) dialog.showModal();
  try {
    const [op, page] = await Promise.all([request(target), request(target + "/events")]);
    if (stamp !== state.detail || !dialog.open) return;
    message("detail-message");
    const list = el("dl", undefined, "detail-list");
    const fields = [["操作 ID", op.id], ["动作", actions[op.action] || op.action], ["状态", statusLabel(op.status)],
      ["资源", op.asset_id || "待分配"], ["厂商请求", op.provider_request_id || "—"]];
    if (op.catalog_template) fields.push(["模板版本", op.catalog_template + " / " + op.catalog_version]);
    if (op.image_copy_intent) {
      const info = op.image_copy_intent;
      fields.push(["源账号 / 地域", `${info.account} / ${info.region}`], ["源镜像 / 归属版本", `${info.asset_id} / ${info.source_revision}`],
        ["目标账号 / 地域", `${info.destination_account} / ${info.destination_region}`], ["目标镜像名称", info.name]);
    }
    if (op.image_creation_intent) {
      const { name, instance_id, instance_revision } = op.image_creation_intent;
      fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["镜像名称", name],
        ["源实例 / 归属版本", `${instance_id} / ${instance_revision}`]);
    }
    if (op.network_deletion_intent) {
      const { name, ownership_revision, parent_id, parent_revision, rule_removal_confirmed } = op.network_deletion_intent;
      fields.push(["账号 / 地域", op.account + " / " + op.region], ["资源名称", name], ["归属版本", ownership_revision]);
      if (parent_id) fields.push(["父 VPC / 归属版本", parent_id + " / " + parent_revision]);
      if (rule_removal_confirmed) fields.push(["规则删除", "已确认同时删除组内全部规则"]);
    }
    if (op.image_deletion_intent) {
      const { name, ownership_revision } = op.image_deletion_intent;
      fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["镜像名称", name], ["归属版本", ownership_revision]);
    }
    if (op.snapshot_restore_intent) {
      const info = op.snapshot_restore_intent;
      fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["源快照 / 类型", `${info.snapshot_id} / ${info.snapshot_class}`],
        ["源卷 / 归属版本", `${info.source_volume_id} / ${info.source_volume_revision}`], ["新云盘名称", info.name],
        ["可用区 / 盘型", `${info.zone} / ${info.disk_type}`], ["容量", `${info.size_gib} GiB`],
        ["源快照加密", info.encrypted ? "是" : "否"], ["计费方式", "按需"]);
    }
    if (op.snapshot_intent) {
      const { name, volume_id, volume_revision, allow_attached, class: snapshotClass } = op.snapshot_intent;
      fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["快照名称", name],
        ["源卷 / 归属版本", `${volume_id} / ${volume_revision}`]);
      if (["delete-snapshot", "rollback-snapshot"].includes(op.action)) fields.push(["快照类别", snapshotClass]);
      else fields.push(["允许使用挂载中的源卷", allow_attached ? "是，崩溃一致性" : "否"]);
    }
    if (op.action === "create-subnet" && op.network_intent) {
      const { name, parent_id, parent_revision, cidr, zone } = op.network_intent;
      fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["子网名称", name], ["父网络 / 归属版本", `${parent_id} / ${parent_revision}`],
        ["子网 CIDR", cidr], ["可用区", zone || "未指定"]);
    }
    if (op.action === "create-security-group" && op.network_intent) {
      const { name, parent_id, parent_revision } = op.network_intent;
      fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["安全组名称", name]);
      if (parent_id) fields.push(["父网络 / 归属版本", `${parent_id} / ${parent_revision}`]);
    }
    if (op.action === "create-elastic-ip" && op.network_intent) {
      const { name, bandwidth_mbps, address_pool } = op.network_intent;
      fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["公网 IP 名称", name],
        ["带宽上限", `${bandwidth_mbps} Mbps`], ["计费方式", "按流量后付费"]);
      if (address_pool) fields.push(["地址池", address_pool]);
    }
    if (op.action === "release-elastic-ip" && op.network_intent) {
      const { name, asset_revision, public_address } = op.network_intent;
      fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["公网 IP 名称", name],
        ["待释放地址", public_address], ["归属版本", asset_revision]);
    }
    if (["bind-elastic-ip", "unbind-elastic-ip"].includes(op.action) && op.network_intent) {
      const { name, asset_revision, public_address, parent_id, parent_revision, port_id } = op.network_intent;
      fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["公网 IP 名称", name],
        ["公网地址 / 归属版本", `${public_address} / ${asset_revision}`], ["实例 / 归属版本", `${parent_id} / ${parent_revision}`]);
      if (port_id) fields.push(["网卡端口", port_id]);
    }
    if (op.network_intent?.rule) {
      const { parent_id, parent_revision, rule } = op.network_intent;
      fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["安全组 / 归属版本", `${parent_id} / ${parent_revision}`],
        ["规则方向", rule.direction === "ingress" ? "入站" : "出站"],
        ["协议 / 端口", `${rule.protocol.toUpperCase()} / ${rule.from_port}–${rule.to_port}`],
        ["远端 CIDR", rule.remote_cidr]);
    }
    if (op.action === "revoke-security-group-rule" && op.network_intent) {
      const { parent_id, parent_revision, name } = op.network_intent;
      fields.push(["账号 / 地域", `${op.account} / ${op.region}`], ["安全组 / 归属版本", `${parent_id} / ${parent_revision}`], ["待撤销规则", name]);
    }
    for (const [label, value] of fields) {
      const item = el("dd");
      item.append(value);
      list.append(el("dt", label), item);
    }
    const timeline = el("ol");
    const addEvents = (events) => {
      for (const event of events) timeline.append(el("li", timestamp(event.recorded_at) + " " + (statuses[event.status] || event.status)));
    };
    addEvents(page.events);
    const panel = find("detail-body");
    panel.replaceChildren(list, el("h2", "状态记录"), timeline);
    if (page.has_more) {
      let cursor = page.next_after;
      const more = button("加载更多事件", async () => {
        more.disabled = true;
        try {
          const next = await request(target + "/events?after=" + encodeURIComponent(cursor));
          if (stamp !== state.detail) return;
          addEvents(next.events);
          cursor = next.next_after;
          more.hidden = !next.has_more;
        } catch (error) {
          message("detail-message", error.message, true);
        } finally {
          more.disabled = false;
        }
      });
      panel.append(more);
    }
    operationDecisions(op, target, stamp);
  } catch (error) {
    if (stamp === state.detail) message("detail-message", error.message, true);
  }
}

function operationDecisions(op, target, stamp) {
  if (state.project.role !== "approver" || !["awaiting_approval", "needs_review"].includes(op.status)) return;
  const controls = el("div", undefined, "row-actions");
  const send = async (suffix, payload) => {
    if (stamp !== state.detail) return;
    for (const control of controls.querySelectorAll("button")) control.disabled = true;
    try {
      await request(target + "/" + suffix, { method: "POST", body: JSON.stringify(payload) });
      if (stamp === state.detail && find("detail").open) {
        showOperation(op.id);
        if (state.view === "operations") loadPage();
      }
    } catch (error) {
      if (stamp === state.detail) message("detail-message", error.message, true);
      for (const control of controls.querySelectorAll("button")) control.disabled = false;
    }
  };
  if (op.status === "awaiting_approval") {
    controls.append(button("批准", () => send("approval", { decision: "approve" })),
      button("拒绝", () => send("approval", { decision: "reject" })));
  } else {
    const label = el("label", "证据引用");
    const evidence = el("input");
    evidence.maxLength = 200;
    label.append(evidence);
    find("detail-body").append(label);
    const review = (suffix, fields) => {
      if (!evidence.value.trim()) return message("detail-message", "请填写证据引用。", true);
      send(suffix, { evidence_ref: evidence.value.trim(), ...fields });
    };
    const resolution = (decision) => review("resolution", { decision, provider_request_id: op.provider_request_id || undefined });
    controls.append(button("记录失败", () => resolution("failed")));
    if (op.action === "rollback-snapshot" && op.snapshot_intent?.can_retry_observation) {
      controls.append(button("恢复只读观测", () => review("retry-observation", {})));
    }
    if (op.action === "copy-image") {
      if (op.image_copy_intent?.can_retry_observation) {
        controls.append(button("恢复只读观测", () => review("retry-observation", {})));
      }
    } else if (op.action.startsWith("create") || ["revoke-security-group-rule", "release-elastic-ip", "bind-elastic-ip", "unbind-elastic-ip", "delete-snapshot", "delete-image", "delete-network", "delete-subnet", "delete-security-group", "restore-snapshot"].includes(op.action)) {
      controls.append(button("恢复只读观测", () => review("retry-observation", {})));
    } else {
      controls.append(button("记录成功", () => resolution("succeeded")));
    }
  }
  find("detail-body").append(controls);
}

find("login").addEventListener("submit", async (event) => {
  event.preventDefault();
  const submit = event.submitter;
  submit.disabled = true;
  try {
    await request("/console/login", { method: "POST", body: JSON.stringify({ tenant: find("tenant").value.trim() }) });
    message("login-message");
    await showProfile();
  } catch (error) {
    message("login-message", error.message, true);
  } finally {
    submit.disabled = false;
  }
});
find("project").addEventListener("change", chooseProject);
for (const nav of find("navigation").querySelectorAll("button")) nav.addEventListener("click", () => selectView(nav.dataset.view));
find("filters").addEventListener("submit", (event) => { event.preventDefault(); loadPage(); });
find("more").addEventListener("click", () => loadPage(true));
find("audit-export").addEventListener("click", exportAudit);
find("allocate-elastic-ip").addEventListener("click", showElasticIPAllocation);
find("catalog-create").addEventListener("click", () => showCatalogEditor());
find("catalog-refresh").addEventListener("click", () => loadPage());
find("native-refresh").addEventListener("click", () => loadPage());
find("catalog-retry").addEventListener("click", retryPendingCatalogLaunch);
find("selected").addEventListener("click", () => { selectView("batch"); fillBatch(); });
find("fill-batch").addEventListener("click", fillBatch);
find("batch-input").addEventListener("input", resetBatch);
find("batch-editor").addEventListener("submit", (event) => { event.preventDefault(); previewBatch(); });
find("submit-batch").addEventListener("click", submitBatch);
find("lookup-batch").addEventListener("submit", async (event) => {
  event.preventDefault();
  const base = projectURL();
  try {
    const receipt = await request(base + "/operation-batches/" + encodeURIComponent(find("batch-id").value.trim()));
    if (state.project && base === projectURL()) showBatchReceipt(receipt);
  } catch (error) {
    message("workspace-message", error.message, true);
  }
});
find("close-detail").addEventListener("click", () => find("detail").close());
find("detail").addEventListener("close", () => { state.detail++; });
find("logout").addEventListener("click", async () => {
  try {
    await request("/console/logout", { method: "POST" });
    showEntry();
    message("login-message", "已退出本地会话。");
  } catch (error) {
    message("workspace-message", error.message, true);
  }
});

showProfile().catch((error) => {
  if (error.status !== 401) message("login-message", error.message, true);
});
