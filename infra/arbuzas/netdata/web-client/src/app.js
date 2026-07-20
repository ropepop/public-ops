import { html, reactive } from "@arrow-js/core";
import "./app.css";
import {
  activeAlerts,
  dimensionSeries,
  formatMemory,
  formatPercent,
  formatRate,
  formatUptime,
  latestValue,
  mergeContainers,
  ratioSeries,
  serviceStates,
  sumSeries,
  svgPoints,
} from "./metrics.js";

const REFRESH_INTERVAL_MS = 10_000;
const METRIC_STALE_AFTER_MS = 90_000;
const RAM_DIMENSIONS = ["free", "used", "cached", "buffers"];
const CORE_SERVICES = [
  ["containerd.service", "Container runtime"],
  ["docker.service", "Docker"],
  ["netdata.service", "Netdata"],
  ["ssh.service", "SSH"],
  ["tailscaled.service", "Tailscale"],
];
const ESSENTIAL_METRICS = new Set(["nodes", "cpu", "memory", "load", "network", "uptime"]);
const urls = {
  nodes: "/api/v3/nodes",
  alerts: "/api/v1/alarms?active",
  cpu: "/api/v3/data?contexts=system.cpu&after=-900&points=60&time_group=avg&group_by=dimension",
  memory: "/api/v3/data?contexts=system.ram&after=-900&points=60&time_group=avg&group_by=dimension",
  load: "/api/v3/data?contexts=system.load&after=-900&points=60&time_group=avg&group_by=dimension",
  network: "/api/v3/data?contexts=system.net&after=-900&points=60&time_group=avg&group_by=dimension",
  diskIo: "/api/v3/data?contexts=disk.io&instances=disk.sda&after=-900&points=60&time_group=avg&group_by=dimension",
  diskUtil: "/api/v3/data?contexts=disk.util&instances=disk_util.sda&after=-900&points=60&time_group=avg&group_by=dimension",
  diskSpace: "/api/v3/data?contexts=disk.space&instances=disk_space.%2F&after=-60&points=1&time_group=avg&group_by=dimension",
  uptime: "/api/v3/data?contexts=system.uptime&after=-60&points=1&time_group=avg&group_by=dimension",
  services: "/api/v3/data?contexts=systemd.service_unit_state&after=-60&points=1&time_group=avg&group_by=instance,dimension",
  containerCpu: "/api/v3/data?contexts=cgroup.cpu&after=-60&points=1&time_group=avg&group_by=instance",
  containerMemory: "/api/v3/data?contexts=cgroup.mem_usage&after=-60&points=1&time_group=avg&group_by=instance",
};

const state = reactive({
  phase: "loading",
  error: "",
  lastUpdated: 0,
  version: "",
  cpu: [],
  memory: [],
  load: [],
  networkIn: [],
  networkOut: [],
  diskReads: [],
  diskWrites: [],
  diskUtil: [],
  uptime: null,
  disk: { used: null, available: null, reserved: null, percent: null },
  services: CORE_SERVICES.map(([name, label]) => ({ name, label, state: "unknown" })),
  containers: [],
  alerts: [],
});

let refreshInFlight = false;

const fetchJson = async (url) => {
  const response = await fetch(url, {
    cache: "no-store",
    headers: { Accept: "application/json" },
    signal: AbortSignal.timeout(8_000),
  });
  if (!response.ok) throw new Error(`${url} returned ${response.status}`);
  return response.json();
};

const latestTimestamp = (series) => (series.length ? series[series.length - 1].time : 0);
const combinedLatest = (...series) => {
  const values = series.map((items) => latestValue(items));
  return values.some((value) => value === null) ? null : values.reduce((total, value) => total + value, 0);
};
const formatDecimal = (value) => (value === null ? "—" : Number(value).toFixed(2));

const completeServiceStates = (payload) => {
  const observed = new Map(serviceStates(payload).map((service) => [service.name, service]));
  return CORE_SERVICES.map(([name, label]) => observed.get(name) || { name, label, state: "unknown" });
};

const refresh = async () => {
  if (refreshInFlight) return;
  refreshInFlight = true;
  if (!state.lastUpdated) state.phase = "loading";
  try {
    const entries = Object.entries(urls);
    const results = await Promise.allSettled(entries.map(([, url]) => fetchJson(url)));
    const payloads = {};
    const failures = new Set();
    results.forEach((result, index) => {
      const [name] = entries[index];
      if (result.status === "fulfilled") payloads[name] = result.value;
      else failures.add(name);
    });

    if (payloads.nodes) {
      const version = String(payloads.nodes?.nodes?.[0]?.v || payloads.nodes?.nodes?.[0]?.version || "");
      if (version) state.version = version;
      else failures.add("nodes");
    }
    if (payloads.cpu) {
      const series = sumSeries(payloads.cpu);
      if (series.length) state.cpu = series;
      else failures.add("cpu");
    }
    if (payloads.memory) {
      const series = ratioSeries(payloads.memory, "used", RAM_DIMENSIONS);
      if (series.length) state.memory = series;
      else failures.add("memory");
    }
    if (payloads.load) {
      const series = dimensionSeries(payloads.load, "load1");
      if (series.length) state.load = series;
      else failures.add("load");
    }
    if (payloads.network) {
      const incoming = dimensionSeries(payloads.network, "received", { absolute: true });
      const outgoing = dimensionSeries(payloads.network, "sent", { absolute: true });
      if (incoming.length && outgoing.length) {
        state.networkIn = incoming;
        state.networkOut = outgoing;
      } else failures.add("network");
    }
    if (payloads.diskIo) {
      const reads = dimensionSeries(payloads.diskIo, "reads", { absolute: true });
      const writes = dimensionSeries(payloads.diskIo, "writes", { absolute: true });
      if (reads.length && writes.length) {
        state.diskReads = reads;
        state.diskWrites = writes;
      } else failures.add("diskIo");
    }
    if (payloads.diskUtil) {
      const series = dimensionSeries(payloads.diskUtil, "utilization", { absolute: true });
      if (series.length) state.diskUtil = series;
      else failures.add("diskUtil");
    }
    if (payloads.uptime) {
      const series = dimensionSeries(payloads.uptime, "uptime");
      if (series.length) state.uptime = latestValue(series);
      else failures.add("uptime");
    }
    if (payloads.diskSpace) {
      const used = latestValue(dimensionSeries(payloads.diskSpace, "used", { absolute: true }));
      const available = latestValue(dimensionSeries(payloads.diskSpace, "avail", { absolute: true }));
      const reserved = latestValue(dimensionSeries(payloads.diskSpace, "reserved for root", { absolute: true }));
      if ([used, available, reserved].every((value) => value !== null)) {
        const total = used + available + reserved;
        state.disk = { used, available, reserved, percent: total > 0 ? (used / total) * 100 : null };
      } else failures.add("diskSpace");
    }
    if (payloads.services) {
      const observed = serviceStates(payloads.services);
      const observedNames = new Set(observed.map((service) => service.name));
      if (CORE_SERVICES.every(([name]) => observedNames.has(name))) state.services = completeServiceStates(payloads.services);
      else failures.add("services");
    }
    if (payloads.containerCpu && payloads.containerMemory) {
      const containers = mergeContainers(payloads.containerCpu, payloads.containerMemory);
      if (containers.length && containers.every((container) => container.cpu !== null)) state.containers = containers;
      else failures.add("containers");
    }
    if (payloads.alerts) {
      if (payloads.alerts.alarms && typeof payloads.alerts.alarms === "object" && !Array.isArray(payloads.alerts.alarms)) {
        state.alerts = activeAlerts(payloads.alerts);
      }
      else failures.add("alerts");
    }

    const successfulMetrics = Object.keys(payloads).filter((name) => !failures.has(name));
    const failedEssentials = [...failures].filter((name) => ESSENTIAL_METRICS.has(name));
    const newestMetricTime = Math.max(
      latestTimestamp(state.cpu),
      latestTimestamp(state.memory),
      latestTimestamp(state.load),
      latestTimestamp(state.networkIn),
      latestTimestamp(state.diskReads),
      latestTimestamp(state.diskUtil),
    );
    if (newestMetricTime > 0) state.lastUpdated = newestMetricTime;
    const metricIsDelayed = !newestMetricTime || Date.now() - newestMetricTime > METRIC_STALE_AFTER_MS;
    state.error = failures.size
      ? `Some sections could not refresh: ${[...failures].join(", ")}.`
      : metricIsDelayed
        ? "The latest metric sample is delayed."
        : "";
    if (!successfulMetrics.length) state.phase = state.lastUpdated ? "stale" : "error";
    else if (failedEssentials.length || metricIsDelayed) state.phase = "stale";
    else state.phase = failures.size ? "partial" : "live";
  } catch (error) {
    state.error = error instanceof Error ? error.message : "Live metrics are unavailable";
    state.phase = state.lastUpdated ? "stale" : "error";
  } finally {
    refreshInFlight = false;
  }
};

const alertCounts = () => ({
  critical: state.alerts.filter((alert) => alert.status === "CRITICAL").length,
  warning: state.alerts.filter((alert) => alert.status === "WARNING").length,
  unknown: state.alerts.filter((alert) => !["CRITICAL", "WARNING"].includes(alert.status)).length,
});

const health = () => {
  if (state.phase === "loading") return { label: "Connecting", tone: "warning" };
  if (state.phase === "error") return { label: "Unavailable", tone: "critical" };
  const counts = alertCounts();
  if (counts.critical) return { label: "Critical", tone: "critical" };
  if (state.services.some((service) => !["active", "unknown"].includes(service.state))) {
    return { label: "Service issue", tone: "critical" };
  }
  if (counts.warning || counts.unknown) return { label: "Attention", tone: "warning" };
  if (state.services.some((service) => service.state === "unknown") || state.phase === "partial") {
    return { label: "Data incomplete", tone: "warning" };
  }
  if (state.phase === "stale") return { label: "Data delayed", tone: "warning" };
  return { label: "Healthy", tone: "healthy" };
};

const formattedUpdate = () =>
  state.lastUpdated
    ? new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit" }).format(state.lastUpdated)
    : "Waiting for metrics";

const chartCard = ({ title, value, detail, primary, secondary = null, tone = "green" }) => html`
  <article class="panel chart-panel" data-testid="${`chart-${title.toLowerCase().replaceAll(" ", "-")}`}">
    <div class="panel-heading">
      <div>
        <p class="eyebrow">${title}</p>
        <p class="chart-value">${value}</p>
      </div>
      <span class="chart-detail">${detail}</span>
    </div>
    <svg class="sparkline" viewBox="0 0 420 112" preserveAspectRatio="none" role="img" aria-label="${`${title} over the last 15 minutes`}">
      <line class="grid-line" x1="0" x2="420" y1="28" y2="28"></line>
      <line class="grid-line" x1="0" x2="420" y1="56" y2="56"></line>
      <line class="grid-line" x1="0" x2="420" y1="84" y2="84"></line>
      <polyline class="${`series series-${tone}`}" points="${primary}"></polyline>
      ${secondary ? html`<polyline class="series series-secondary" points="${secondary}"></polyline>` : ""}
    </svg>
    <div class="chart-axis"><span>15 minutes ago</span><span>Now</span></div>
  </article>
`;

const serviceView = (service) => html`
  <li class="service-row">
    <span class="${`status-dot status-${service.state === "active" ? "healthy" : service.state === "unknown" ? "warning" : "critical"}`}"></span>
    <span>${service.label}</span>
    <strong>${service.state}</strong>
  </li>
`;

const containerView = (container, maximumMemory) => html`
  <li class="container-row">
    <div class="container-title">
      <span>${container.name}</span>
      <span class="container-metrics">${formatPercent(container.cpu)} CPU · ${formatMemory(container.memory)}</span>
    </div>
    <div class="meter"><span style="${`width: ${Math.max(2, (container.memory / Math.max(1, maximumMemory)) * 100)}%`}"></span></div>
  </li>
`;

const alertView = (alert) => html`
  <li class="${`alert-row alert-${alert.status.toLowerCase()}`}">
    <span class="alert-badge">${alert.status}</span>
    <div>
      <strong>${alert.title}</strong>
      <p>${alert.detail}</p>
    </div>
  </li>
`;

const dashboard = html`
  <div class="dashboard-shell">
    <header class="topbar">
      <div class="identity">
        <div class="mark" aria-hidden="true">K</div>
        <div>
          <p class="eyebrow">Production host</p>
          <h1>Kitty-gration</h1>
        </div>
      </div>
      <div class="top-actions">
        <div class="live-state" data-testid="dashboard-status">
          <span class="${() => `status-dot status-${health().tone}`}"></span>
          <span>${() => health().label}</span>
        </div>
        <button class="refresh-button" type="button" @click="${refresh}">Refresh</button>
        <a class="netdata-link" href="/" target="_blank" rel="noopener" aria-label="Open the full Netdata console"><span class="netdata-label">Full Netdata</span><span aria-hidden="true">↗</span></a>
      </div>
    </header>

    <section class="hero">
      <div>
        <p class="eyebrow">Operations overview</p>
        <h2>One glance. Every essential signal.</h2>
        <p class="hero-copy">Live host, service, and container health from Netdata. This view is shared across every device.</p>
      </div>
      <div class="freshness">
        <span>Updated</span>
        <strong>${formattedUpdate}</strong>
        <small>${() => (state.version ? `Netdata ${state.version}` : "Connecting…")}</small>
      </div>
    </section>

    ${() =>
      state.error
        ? html`<div class="notice" role="status"><strong>Live refresh delayed.</strong> ${state.error}</div>`
        : ""}

    <section class="summary-grid" aria-label="Host summary" data-testid="host-summary">
      <article class="summary-card summary-health">
        <span class="summary-label">Overall health</span>
        <strong class="${() => `summary-value text-${health().tone}`}">${() => health().label}</strong>
        <span class="summary-note">${() => `${state.services.filter((service) => service.state === "active").length}/${state.services.length || 5} core services active`}</span>
      </article>
      <article class="summary-card">
        <span class="summary-label">CPU</span>
        <strong class="summary-value">${() => formatPercent(latestValue(state.cpu))}</strong>
        <span class="summary-note">2 virtual cores</span>
      </article>
      <article class="summary-card">
        <span class="summary-label">Memory</span>
        <strong class="summary-value">${() => formatPercent(latestValue(state.memory))}</strong>
        <span class="summary-note">Application memory</span>
      </article>
      <article class="summary-card">
        <span class="summary-label">Root disk</span>
        <strong class="summary-value">${() => formatPercent(state.disk.percent)}</strong>
        <span class="summary-note">${() => (state.disk.available === null ? "Waiting for data" : `${state.disk.available.toFixed(1)} GiB available`)}</span>
      </article>
      <article class="summary-card">
        <span class="summary-label">Load / uptime</span>
        <strong class="summary-value">${() => formatDecimal(latestValue(state.load))}</strong>
        <span class="summary-note">${() => `${formatUptime(state.uptime)} online`}</span>
      </article>
      <article class="summary-card">
        <span class="summary-label">Active alerts</span>
        <strong class="summary-value">${() => state.alerts.length}</strong>
        <span class="summary-note">${() => {
          const counts = alertCounts();
          return `${counts.critical} critical · ${counts.warning} warning${counts.unknown ? ` · ${counts.unknown} unknown` : ""}`;
        }}</span>
      </article>
    </section>

    <section class="chart-grid" aria-label="Host trends" data-testid="host-trends">
      ${chartCard({
        title: "CPU usage",
        value: () => formatPercent(latestValue(state.cpu)),
        detail: "All cores",
        primary: () => svgPoints(state.cpu),
      })}
      ${chartCard({
        title: "Memory used",
        value: () => formatPercent(latestValue(state.memory)),
        detail: "Excludes cache",
        primary: () => svgPoints(state.memory),
        tone: "blue",
      })}
      ${chartCard({
        title: "Network traffic",
        value: () => formatRate(combinedLatest(state.networkIn, state.networkOut), "kilobits/s"),
        detail: "In + out",
        primary: () => svgPoints(state.networkIn),
        secondary: () => svgPoints(state.networkOut),
        tone: "cyan",
      })}
      ${chartCard({
        title: "Disk activity",
        value: () => formatRate(combinedLatest(state.diskReads, state.diskWrites), "KiB/s"),
        detail: () => `${formatPercent(latestValue(state.diskUtil))} busy`,
        primary: () => svgPoints(state.diskReads),
        secondary: () => svgPoints(state.diskWrites),
        tone: "amber",
      })}
    </section>

    <section class="operations-grid">
      <article class="panel services-panel" data-testid="core-services">
        <div class="panel-heading">
          <div><p class="eyebrow">Host services</p><h3>Core control plane</h3></div>
          <span class="panel-count">${() => `${state.services.filter((service) => service.state === "active").length} active`}</span>
        </div>
        <ul class="service-list">${() => state.services.map(serviceView)}</ul>
      </article>

      <article class="panel alerts-panel" data-testid="active-alerts">
        <div class="panel-heading">
          <div><p class="eyebrow">Health alerts</p><h3>Needs attention</h3></div>
          <span class="panel-count">${() => state.alerts.length}</span>
        </div>
        ${() =>
          state.alerts.length
            ? html`<ul class="alert-list">${() => state.alerts.map(alertView)}</ul>`
            : html`<div class="empty-state"><span>✓</span><strong>No active alerts</strong><p>Netdata reports a clear host.</p></div>`}
      </article>
    </section>

    <section class="panel containers-panel" data-testid="containers">
      <div class="panel-heading">
        <div><p class="eyebrow">Runtime</p><h3>Containers by memory</h3></div>
        <span class="panel-count">${() => `${state.containers.length} running`}</span>
      </div>
      <ul class="container-grid">${() => {
        const maximumMemory = Math.max(...state.containers.map((container) => container.memory), 1);
        return state.containers.map((container) => containerView(container, maximumMemory));
      }}</ul>
    </section>

    <footer>
      <span>Private on Tailscale</span>
      <span>Auto-refreshes every 10 seconds</span>
      <span>Last 15 minutes</span>
    </footer>
  </div>
`;

const root = document.querySelector("#dashboard-root");
root.textContent = "";
dashboard(root);
document.documentElement.dataset.netdataDashboardUi = "arrow";
document.documentElement.dataset.dashboardState = "loading";

state.$on("phase", (phase) => {
  document.documentElement.dataset.dashboardState = phase;
});

refresh();
setInterval(() => {
  if (!document.hidden) refresh();
}, REFRESH_INTERVAL_MS);
document.addEventListener("visibilitychange", () => {
  if (!document.hidden && Date.now() - state.lastUpdated > REFRESH_INTERVAL_MS) refresh();
});
