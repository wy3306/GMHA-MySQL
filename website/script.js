const header = document.querySelector("[data-header]");
const menuButton = document.querySelector("[data-menu-button]");
const navigation = document.querySelector("[data-nav]");

function updateHeader() {
  header?.classList.toggle("scrolled", window.scrollY > 18);
}

function closeMenu() {
  navigation?.classList.remove("open");
  menuButton?.setAttribute("aria-expanded", "false");
  document.body.classList.remove("menu-open");
}

menuButton?.addEventListener("click", () => {
  const open = menuButton.getAttribute("aria-expanded") !== "true";
  menuButton.setAttribute("aria-expanded", String(open));
  navigation?.classList.toggle("open", open);
  document.body.classList.toggle("menu-open", open);
});

navigation?.querySelectorAll("a").forEach((link) => link.addEventListener("click", closeMenu));
window.addEventListener("resize", () => window.innerWidth > 760 && closeMenu());
window.addEventListener("scroll", updateHeader, { passive: true });

const hero = document.querySelector("[data-hero]");
const capture = document.querySelector("[data-hero-image]");
const toggle = document.querySelector("[data-hero-toggle]");
const scenes = [
 ["cluster-overview", "集群运行概览", "QPS、TPS、资源与复制拓扑"],
 ["performance-flamegraph", "实采 Linux 火焰图", "304 个样本与完整调用栈"],
 ["instance-database-inspection", "数据库健康巡检", "真实评分、风险与修复建议"],
 ["architecture-safety-plan", "架构调整安全计划", "变更前生成 12 步执行护栏"],
 ["ai-assistant-conversation", "AI 运维助手", "理解上下文，衔接受控执行"]
];
let current = 0;
let paused = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
let changing = false;
function updatePlayback() {
 hero?.classList.toggle("paused", paused);
 toggle.textContent = paused ? "▶" : "Ⅱ";
 toggle.setAttribute("aria-label", paused ? "播放首页演示" : "暂停首页演示");
}
toggle?.addEventListener("click", () => { paused = !paused; updatePlayback(); });
updatePlayback();
setInterval(async () => {
 if (paused || changing || document.hidden || !capture) return;
 changing = true;
 const next = (current + 1) % scenes.length;
 const [file, title, description] = scenes[next];
 const source = "/screenshots/v022-complete/" + file + "-4k.jpg";
 const preload = new Image();
 preload.src = source;
 try {
  await preload.decode();
  if (paused) return;
  capture.classList.add("changing");
  await new Promise(resolve => setTimeout(resolve, 350));
  capture.src = source;
  capture.alt = "GMHA " + title + "完整截图";
  current = next;
  document.querySelector("[data-hero-index]").textContent = String(next + 1).padStart(2,"0") + " / 05";
  document.querySelector("[data-hero-title]").textContent = title;
  document.querySelector("[data-hero-description]").textContent = description;
  capture.classList.remove("changing");
 } catch { capture.classList.remove("changing"); }
 finally { changing = false; }
}, 6000);
