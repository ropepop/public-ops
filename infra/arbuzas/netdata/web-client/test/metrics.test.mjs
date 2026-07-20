import assert from "node:assert/strict";
import test from "node:test";
import {
  activeAlerts,
  cellValue,
  dimensionSeries,
  formatMemory,
  formatPercent,
  formatUptime,
  latestValue,
  mergeContainers,
  ratioSeries,
  serviceStates,
  sumSeries,
  svgPoints,
} from "../src/metrics.js";

const payload = (labels, data) => ({ result: { labels: ["time", ...labels], data } });

test("host metric series are decoded from newest-first Netdata v3 rows", () => {
  const cpu = payload(
    ["user", "system", "iowait"],
    [
      [200, [8], [2], [1]],
      [100, [4], [1], [0]],
    ],
  );
  const series = sumSeries(cpu);
  assert.deepEqual(series, [
    { time: 100_000, value: 5 },
    { time: 200_000, value: 11 },
  ]);
  assert.equal(latestValue(series), 11);
});

test("memory usage is calculated against all Netdata RAM dimensions", () => {
  const memory = payload(["free", "used", "cached", "buffers"], [[100, [256], [1024], [2560], [256]]]);
  assert.equal(latestValue(ratioSeries(memory, "used", ["free", "used", "cached", "buffers"])), 25);
});

test("container CPU and memory instances are merged and ranked", () => {
  const cpu = payload(
    ["cgroup_arbuzas-ticket_remote-1.cpu@node", "cgroup_arbuzas-train_bot-1.cpu@node", "cgroup_system.slice.cpu@node"],
    [[100, [3.5], [1.2], [99]]],
  );
  const memory = payload(
    ["cgroup_arbuzas-ticket_remote-1.mem_usage@node", "cgroup_arbuzas-train_bot-1.mem_usage@node", "cgroup_system.slice.mem_usage@node"],
    [[100, [120], [300], [4096]]],
  );
  assert.deepEqual(mergeContainers(cpu, memory), [
    { id: "arbuzas-train_bot-1", name: "Train Bot", cpu: 1.2, memory: 300 },
    { id: "arbuzas-ticket_remote-1", name: "Ticket Remote", cpu: 3.5, memory: 120 },
  ]);
  assert.deepEqual(
    mergeContainers(payload([], [[100]]), payload(["cgroup_arbuzas-memory_only-1.mem_usage@node"], [[100, [64]]])),
    [{ id: "arbuzas-memory_only-1", name: "Memory Only", cpu: null, memory: 64 }],
  );
});

test("focused systemd services expose their active state", () => {
  const services = payload(
    [
      "active,systemdunits_kitty-gration-core.unit_docker_service_state@node",
      "failed,systemdunits_kitty-gration-core.unit_docker_service_state@node",
      "active,systemdunits_kitty-gration-core.unit_netdata_service_state@node",
      "failed,systemdunits_kitty-gration-core.unit_netdata_service_state@node",
    ],
    [[100, [1], [0], [1], [0]]],
  );
  assert.deepEqual(serviceStates(services), [
    { name: "docker.service", label: "Docker", state: "active" },
    { name: "netdata.service", label: "Netdata", state: "active" },
  ]);
});

test("only active alerts are returned, with critical first", () => {
  const alerts = activeAlerts({
    alarms: {
      clear: { id: 1, status: "CLEAR", summary: "Clear" },
      warning: { id: 2, status: "WARNING", summary: "Reboot required", info: "Kernel update", value: 1, units: "status" },
      critical: { id: 3, status: "CRITICAL", summary: "Disk full", info: "Root disk", value: 99, units: "%" },
    },
  });
  assert.deepEqual(alerts.map((alert) => alert.status), ["CRITICAL", "WARNING"]);
  assert.equal(alerts[1].title, "Reboot required");
});

test("formatters and sparklines handle empty and representative values", () => {
  assert.equal(formatPercent(7.25), "7.3%");
  assert.equal(formatMemory(1536), "1.5 GiB");
  assert.equal(formatUptime(183_600), "2d 3h");
  assert.equal(svgPoints([]), "");
  assert.match(svgPoints(dimensionSeries(payload(["load1"], [[100, [0.4]], [200, [0.8]]]), "load1")), /^0\.0,/);
});

test("malformed payloads degrade to empty data instead of throwing", () => {
  assert.deepEqual(sumSeries({}), []);
  assert.deepEqual(mergeContainers({}, {}), []);
  assert.deepEqual(serviceStates(null), []);
  assert.deepEqual(activeAlerts(null), []);
  assert.equal(cellValue(null), null);
  assert.equal(latestValue([]), null);
  assert.equal(formatPercent(null), "—");
  assert.deepEqual(dimensionSeries(payload(["load1"], [[100, null], [200, [0.8]]]), "load1"), [
    { time: 200_000, value: 0.8 },
  ]);
});
