const scenes = [
  { id: "cluster-overview", category: "cluster", title: "集群管理 · 概览", note: "拓扑与核心指标", file: "cluster-overview-4k.jpg", description: "Demo01 三节点 MGR 集群的实时 QPS、TPS、资源、容量与复制拓扑。" },
  { id: "performance-database-metrics", category: "performance", title: "性能监控 · 数据库指标", note: "真实时序数据", file: "performance-database-metrics-4k.jpg", description: "查询吞吐、连接、InnoDB、缓存命中率与复制状态等真实时序指标。" },
  { id: "performance-memory-analysis", category: "performance", title: "性能监控 · 内存分析", note: "进程与模块拆解", file: "performance-memory-analysis-4k.jpg", description: "展示 Buffer Pool、MySQL 进程与内部模块的真实内存构成。" },
  { id: "performance-flamegraph", category: "performance", title: "性能监控 · Linux 火焰图", note: "304 个实采样本", file: "performance-flamegraph-4k.jpg", description: "DB-01 的真实 perf 采集：304 个样本、2 条唯一栈，支持搜索与导出。" },
  { id: "sql-top-sql", category: "sql", title: "SQL 诊断 · TOP-SQL", note: "50 条 SQL Digest", file: "sql-top-sql-4k.jpg", description: "24 小时窗口内 50 条 SQL Digest，按执行次数、耗时和扫描行聚合排序。" },
  { id: "instance-database-inspection", category: "instance", title: "实例管理 · 数据库巡检", note: "真实结果 · 84 分", file: "instance-database-inspection-4k.jpg", description: "一次真实巡检结果：综合评分 84 分、10 项检查，并展开风险与修复建议。" },
  { id: "instance-index-management", category: "instance", title: "实例管理 · 索引管理", note: "索引风险治理", file: "instance-index-management-4k.jpg", description: "识别冗余、未使用与缺失索引，结合表规模和风险等级辅助治理。" },
  { id: "instance-histogram", category: "instance", title: "实例管理 · 直方图", note: "列统计信息", file: "instance-histogram-4k.jpg", description: "查看列统计信息、桶数量和更新时间，并安全创建或删除 MySQL 直方图。" },
  { id: "instance-binlog-analysis", category: "instance", title: "实例管理 · Binlog 分析", note: "事务与事件追踪", file: "instance-binlog-analysis-4k.jpg", description: "按时间范围解析 Binlog，追踪事务、表变更、事件分布与大事务风险。" },
  { id: "instance-create-install", category: "instance", title: "实例管理 · 创建安装", note: "标准化安装向导", file: "instance-create-install-4k.jpg", description: "从目标机器、安装包、端口到初始化参数，形成可审计的一站式安装任务。" },
  { id: "instance-user-management", category: "instance", title: "实例管理 · 用户管理", note: "账户与权限控制", file: "instance-user-management-4k.jpg", description: "集中查看账户、来源、权限与锁定状态，以受控操作完成授权管理。" },
  { id: "instance-parameter-management", category: "instance", title: "实例管理 · 参数管理", note: "参数检索与变更", file: "instance-parameter-management-4k.jpg", description: "搜索运行参数、比较当前值与默认值，并区分动态生效和重启生效。" },
  { id: "instance-version-upgrade", category: "instance", title: "实例管理 · 版本升级", note: "受控滚动升级", file: "instance-version-upgrade-4k.jpg", description: "通过升级前检查、目标版本、滚动顺序和任务审计控制实例升级。" },
  { id: "architecture-adjustment", category: "ha", title: "高可用 · 架构调整", note: "可视化拓扑编排", file: "architecture-adjustment-4k.jpg", description: "在拓扑画布中规划 MGR、主从与 Router 关系，预览目标架构及成员职责。" },
  { id: "architecture-safety-plan", category: "ha", title: "高可用 · 安全计划", note: "12 步变更护栏", file: "architecture-safety-plan-4k.jpg", description: "执行前生成 12 步安全计划，覆盖预检、冻结、验证、一致性检查和解锁。" },
  { id: "backup-recovery", category: "ha", title: "高可用 · 备份恢复", note: "策略、任务与恢复", file: "backup-recovery-4k.jpg", description: "展示备份策略的周期、保留、目标实例、执行窗口和安全控制配置。" },
  { id: "package-management", category: "platform", title: "平台能力 · 安装包管理", note: "版本与校验信息", file: "package-management-4k.jpg", description: "集中管理 Manager、Agent 与 MySQL 制品，包含版本、架构和校验值。" },
  { id: "alerts-events", category: "platform", title: "告警中心 · 告警事件", note: "22 个活跃事件", file: "alerts-events-4k.jpg", description: "展示活跃告警的等级、当前值、阈值、实例、持续时间与通知次数。" },
  { id: "alerts-metrics", category: "platform", title: "告警中心 · 采集指标", note: "指标值与新鲜度", file: "alerts-metrics-4k.jpg", description: "查看每项指标的采集状态、最新值、标签、抓取时间与数据新鲜度。" },
  { id: "alerts-push-config", category: "platform", title: "告警中心 · 推送配置", note: "通知通道管理", file: "alerts-push-config-4k.jpg", description: "统一配置邮件、Webhook 等通知通道，并管理接收人、模板和推送策略。" },
  { id: "alerts-external-interface", category: "platform", title: "告警中心 · 外部接口", note: "系统集成与回调", file: "alerts-external-interface-4k.jpg", description: "向外部系统提供事件接入与回调接口，展示鉴权、地址和调用说明。" },
  { id: "alert-email-html", category: "platform", title: "告警中心 · HTML 告警邮件", note: "具体事件渲染", file: "alert-email-html-4k.png", description: "将具体告警渲染为包含实例拓扑、触发值和事件明细的 HTML 邮件。" },
  { id: "ai-assistant-conversation", category: "ai", title: "AI 与自动化 · 运维助手", note: "多轮上下文会话", file: "ai-assistant-conversation-4k.jpg", description: "AI 理解集群上下文，在多轮会话中分析风险并给出受控操作建议。" },
  { id: "ai-model-integration", category: "ai", title: "AI 与自动化 · 模型接入", note: "模型、鉴权与路由", file: "ai-model-integration-4k.jpg", description: "统一管理模型提供方、鉴权、默认路由、连接测试和可用状态。" },
];

const categories = [
  ["all", "全部", 24], ["cluster", "集群管理", 1], ["performance", "性能监控", 3], ["sql", "SQL 诊断", 1], ["instance", "实例管理", 8], ["ha", "高可用", 3], ["platform", "平台能力", 6], ["ai", "AI 与自动化", 2],
];
const filters = document.querySelector("[data-tour-filters]");
const sceneList = document.querySelector("[data-tour-scenes]");
const image = document.querySelector("[data-viewer-image]");
const indexLabel = document.querySelector("[data-viewer-index]");
const title = document.querySelector("[data-viewer-title]");
const description = document.querySelector("[data-viewer-description]");
const originalLink = document.querySelector("[data-original-link]");
let activeCategory = "all";
let activeIndex = 0;
let requestId = 0;

categories.forEach(([id, label, count], index) => {
  const button = document.createElement("button");
  button.type = "button";
  button.dataset.category = id;
  button.classList.toggle("active", index === 0);
  button.innerHTML = `${label}<span>${String(count).padStart(2, "0")}</span>`;
  button.addEventListener("click", () => filterScenes(id, button));
  filters.append(button);
});

scenes.forEach((scene, index) => {
  const button = document.createElement("button");
  button.type = "button";
  button.dataset.scene = scene.id;
  button.dataset.category = scene.category;
  button.classList.toggle("active", index === 0);
  button.innerHTML = `<span>${String(index + 1).padStart(2, "0")}</span><strong>${scene.title}</strong><small>${scene.note}</small>`;
  button.addEventListener("click", () => activateScene(index));
  sceneList.append(button);
});

function visibleIndexes() {
  return scenes.map((scene, index) => ({ scene, index })).filter(({ scene }) => activeCategory === "all" || scene.category === activeCategory).map(({ index }) => index);
}

function filterScenes(category, activeButton) {
  activeCategory = category;
  filters.querySelectorAll("button").forEach((button) => button.classList.toggle("active", button === activeButton));
  sceneList.querySelectorAll("button").forEach((button) => { button.hidden = category !== "all" && button.dataset.category !== category; });
  const visible = visibleIndexes();
  if (!visible.includes(activeIndex)) activateScene(visible[0]);
}

function activateScene(index, updateUrl = true) {
  if (index < 0 || index >= scenes.length) return;
  activeIndex = index;
  const scene = scenes[index];
  const source = `/screenshots/v022-complete/${scene.file}?v=0242`;
  sceneList.querySelectorAll("button").forEach((button) => button.classList.toggle("active", button.dataset.scene === scene.id));
  indexLabel.textContent = `${String(index + 1).padStart(2, "0")} / ${scenes.length}`;
  title.textContent = scene.title;
  description.textContent = scene.description;
  originalLink.href = source;
  image.classList.add("loading");
  const nextImage = new Image();
  const currentRequest = ++requestId;
  nextImage.src = source;
  nextImage.addEventListener("load", () => {
    if (currentRequest !== requestId) return;
    image.src = source;
    image.alt = `GMHA ${scene.title}`;
    image.classList.remove("loading");
  });
  nextImage.addEventListener("error", () => currentRequest === requestId && image.classList.remove("loading"));
  if (updateUrl) history.replaceState(null, "", `?scene=${scene.id}`);
}

function move(direction) {
  const visible = visibleIndexes();
  const position = Math.max(0, visible.indexOf(activeIndex));
  activateScene(visible[(position + direction + visible.length) % visible.length]);
}

document.querySelector("[data-viewer-prev]")?.addEventListener("click", () => move(-1));
document.querySelector("[data-viewer-next]")?.addEventListener("click", () => move(1));
document.addEventListener("keydown", (event) => {
  if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
  move(event.key === "ArrowRight" ? 1 : -1);
});

const initialScene = new URLSearchParams(location.search).get("scene");
const initialIndex = scenes.findIndex((scene) => scene.id === initialScene);
if (initialIndex >= 0) activateScene(initialIndex, false);
