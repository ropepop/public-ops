const number = (value, fallback = 0) => {
  if (value === null || value === undefined || value === "") return fallback;
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
};

export const cellValue = (cell) => number(Array.isArray(cell) ? cell[0] : cell, null);

const payloadRows = (payload) => {
  const labels = Array.isArray(payload?.result?.labels) ? payload.result.labels.slice(1) : [];
  const data = Array.isArray(payload?.result?.data) ? payload.result.data : [];
  return data
    .map((row) => ({
      time: number(row?.[0]) * 1000,
      values: Object.fromEntries(labels.map((label, index) => [label, cellValue(row?.[index + 1])])),
    }))
    .filter((row) => row.time > 0)
    .sort((left, right) => left.time - right.time);
};

export const dimensionSeries = (payload, dimension, { absolute = false } = {}) =>
  payloadRows(payload)
    .map((row) => {
      const value = number(row.values[dimension], null);
      return value === null ? null : { time: row.time, value: absolute ? Math.abs(value) : value };
    })
    .filter(Boolean);

export const sumSeries = (payload, { absolute = false, exclude = [] } = {}) => {
  const ignored = new Set(exclude);
  return payloadRows(payload)
    .map((row) => {
      const values = Object.entries(row.values)
        .filter(([label, value]) => !ignored.has(label) && value !== null)
        .map(([, value]) => (absolute ? Math.abs(value) : value));
      return values.length ? { time: row.time, value: values.reduce((total, value) => total + value, 0) } : null;
    })
    .filter(Boolean);
};

export const ratioSeries = (payload, numerator, denominatorDimensions) =>
  payloadRows(payload)
    .map((row) => {
      const numeratorValue = number(row.values[numerator], null);
      const denominatorValues = denominatorDimensions.map((dimension) => number(row.values[dimension], null));
      if (numeratorValue === null || denominatorValues.some((value) => value === null)) return null;
      const total = denominatorValues.reduce((sum, value) => sum + Math.abs(value), 0);
      return total > 0 ? { time: row.time, value: (Math.abs(numeratorValue) / total) * 100 } : null;
    })
    .filter(Boolean);

export const latestValue = (series, fallback = null) =>
  series.length ? number(series[series.length - 1].value, fallback) : fallback;

const metricInstance = (label, suffix) => {
  const instance = String(label || "").split("@")[0];
  if (!instance.startsWith("cgroup_") || !instance.endsWith(suffix)) return null;
  const id = instance.slice("cgroup_".length, -suffix.length);
  return id.startsWith("arbuzas-") || id === "tiny-vless-3xui" ? id : null;
};

const containerLabel = (id) => {
  if (id === "tiny-vless-3xui") return "VLESS / 3X-UI";
  return id
    .replace(/^arbuzas-/, "")
    .replace(/-1$/, "")
    .replaceAll("_", " ")
    .replaceAll("-", " ")
    .replace(/\b\w/g, (letter) => letter.toUpperCase());
};

const instanceValues = (payload, suffix) => {
  const labels = Array.isArray(payload?.result?.labels) ? payload.result.labels.slice(1) : [];
  const row = Array.isArray(payload?.result?.data?.[0]) ? payload.result.data[0] : [];
  return labels.reduce((values, label, index) => {
    const id = metricInstance(label, suffix);
    const value = cellValue(row[index + 1]);
    if (id && value !== null) values.set(id, value);
    return values;
  }, new Map());
};

export const mergeContainers = (cpuPayload, memoryPayload) => {
  const cpu = instanceValues(cpuPayload, ".cpu");
  const memory = instanceValues(memoryPayload, ".mem_usage");
  const ids = new Set([...cpu.keys(), ...memory.keys()]);
  return [...ids]
    .map((id) => {
      const cpuValue = number(cpu.get(id), null);
      const memoryValue = number(memory.get(id), null);
      return {
        id,
        name: containerLabel(id),
        cpu: cpuValue === null ? null : Math.max(0, cpuValue),
        memory: memoryValue === null ? null : Math.max(0, memoryValue),
      };
    })
    .filter((container) => container.memory !== null)
    .sort((left, right) => right.memory - left.memory || right.cpu - left.cpu || left.name.localeCompare(right.name));
};

const serviceName = (instance) => {
  const match = String(instance).match(/\.unit_(.+)_service_state$/);
  return match ? `${match[1].replaceAll("_", "-")}.service` : String(instance);
};

const serviceLabels = {
  "containerd.service": "Container runtime",
  "docker.service": "Docker",
  "netdata.service": "Netdata",
  "ssh.service": "SSH",
  "tailscaled.service": "Tailscale",
};

export const serviceStates = (payload) => {
  const labels = Array.isArray(payload?.result?.labels) ? payload.result.labels.slice(1) : [];
  const row = Array.isArray(payload?.result?.data?.[0]) ? payload.result.data[0] : [];
  const services = new Map();
  labels.forEach((label, index) => {
    const [state, instance = ""] = String(label).split(",", 2);
    const name = serviceName(instance.split("@")[0]);
    const current = services.get(name) || { name, state: "unknown", strength: -1 };
    const strength = cellValue(row[index + 1]);
    if (strength !== null && strength > current.strength) services.set(name, { name, state, strength });
  });
  const order = Object.keys(serviceLabels);
  return [...services.values()]
    .map((service) => ({
      name: service.name,
      label: serviceLabels[service.name] || service.name,
      state: service.strength > 0 ? service.state : "unknown",
    }))
    .sort((left, right) => order.indexOf(left.name) - order.indexOf(right.name));
};

const alertRank = { CRITICAL: 0, WARNING: 1, UNDEFINED: 2 };

export const activeAlerts = (payload) =>
  Object.values(payload?.alarms || {})
    .filter((alert) => !["CLEAR", "REMOVED", "UNINITIALIZED"].includes(String(alert?.status || "").toUpperCase()))
    .map((alert) => ({
      id: String(alert.id ?? `${alert.chart || "chart"}:${alert.name || "alert"}`),
      status: String(alert.status || "UNDEFINED").toUpperCase(),
      title: String(alert.summary || alert.info || alert.name || "Active alert"),
      detail: String(alert.info || alert.name || ""),
      value: number(alert.value),
      units: String(alert.units || ""),
    }))
    .sort((left, right) => (alertRank[left.status] ?? 9) - (alertRank[right.status] ?? 9) || left.title.localeCompare(right.title));

export const formatPercent = (value) => {
  const amount = number(value, null);
  return amount === null ? "—" : `${amount.toFixed(amount >= 10 ? 0 : 1)}%`;
};

export const formatMemory = (mib) => {
  const value = number(mib, null);
  if (value === null) return "—";
  return value >= 1024 ? `${(value / 1024).toFixed(value >= 10240 ? 0 : 1)} GiB` : `${value.toFixed(value >= 100 ? 0 : 1)} MiB`;
};

export const formatRate = (value, unit) => {
  const parsed = number(value, null);
  if (parsed === null) return "—";
  const amount = Math.abs(parsed);
  if (unit === "kilobits/s" && amount >= 1000) return `${(amount / 1000).toFixed(1)} Mbit/s`;
  if (unit === "KiB/s" && amount >= 1024) return `${(amount / 1024).toFixed(1)} MiB/s`;
  return `${amount.toFixed(amount >= 100 ? 0 : 1)} ${unit}`;
};

export const formatUptime = (seconds) => {
  const value = number(seconds, null);
  if (value === null) return "—";
  const total = Math.max(0, Math.floor(value));
  const days = Math.floor(total / 86400);
  const hours = Math.floor((total % 86400) / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  if (days) return `${days}d ${hours}h`;
  if (hours) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
};

export const svgPoints = (series, width = 420, height = 112) => {
  if (!series.length) return "";
  const values = series.map((point) => number(point.value));
  const minimum = Math.min(...values, 0);
  const maximum = Math.max(...values, minimum + 1);
  const range = maximum - minimum || 1;
  const denominator = Math.max(1, values.length - 1);
  return values
    .map((value, index) => {
      const x = (index / denominator) * width;
      const y = height - ((value - minimum) / range) * (height - 8) - 4;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
};
