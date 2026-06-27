#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
QUERYLOG_HTML="${REPO_ROOT}/tools/arbuzas-rs/crates/arbuzas-dns/src/identity_assets/querylog.html"

if ! command -v node >/dev/null 2>&1; then
  echo "FAIL: node is required" >&2
  exit 1
fi

if [[ ! -f "${QUERYLOG_HTML}" ]]; then
  echo "FAIL: query log page missing: ${QUERYLOG_HTML}" >&2
  exit 1
fi

node - "${QUERYLOG_HTML}" <<'NODE'
const fs = require("fs");
const vm = require("vm");

const htmlPath = process.argv[2];
const html = fs.readFileSync(htmlPath, "utf8");
const scriptMatch = html.match(/<script>\s*([\s\S]*?)\s*<\/script>/i);
if (!scriptMatch) {
  throw new Error("querylog.html is missing its inline script");
}
const scriptSource = scriptMatch[1];

const fail = (message) => {
  throw new Error(message);
};

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const ROW_HEIGHT = 40;
const PAGE_VIEWPORT_HEIGHT = 120;
const BASE_PAGE_HEIGHT = 160;

let windowObject = null;
const observerInstances = [];

class Element {
  constructor(tagName, ownerDocument) {
    this.tagName = String(tagName || "").toUpperCase();
    this.ownerDocument = ownerDocument;
    this.children = [];
    this.parentNode = null;
    this.listeners = new Map();
    this.attributes = new Map();
    this.style = {};
    this.dataset = {};
    this.hidden = false;
    this.disabled = false;
    this.value = "";
    this.id = "";
    this.className = "";
    this.colSpan = 1;
    this._textContent = "";
  }

  appendChild(child) {
    child.parentNode = this;
    this.children.push(child);
    if (this.ownerDocument && typeof this.ownerDocument.recalculateLayout === "function") {
      this.ownerDocument.recalculateLayout();
    }
    return child;
  }

  removeChild(child) {
    const index = this.children.indexOf(child);
    if (index >= 0) {
      this.children.splice(index, 1);
      child.parentNode = null;
      if (this.ownerDocument && typeof this.ownerDocument.recalculateLayout === "function") {
        this.ownerDocument.recalculateLayout();
      }
    }
    return child;
  }

  setAttribute(name, value) {
    const normalizedName = String(name || "");
    const normalizedValue = String(value ?? "");
    this.attributes.set(normalizedName, normalizedValue);
    if (normalizedName === "id") this.id = normalizedValue;
    if (normalizedName === "class") this.className = normalizedValue;
    if (normalizedName.startsWith("data-")) {
      const key = normalizedName
        .slice(5)
        .replace(/-([a-z])/g, (_match, letter) => letter.toUpperCase());
      this.dataset[key] = normalizedValue;
    }
  }

  getAttribute(name) {
    return this.attributes.get(String(name || "")) || null;
  }

  addEventListener(type, handler) {
    const key = String(type || "");
    if (!this.listeners.has(key)) this.listeners.set(key, []);
    this.listeners.get(key).push(handler);
  }

  dispatchEvent(eventOrType) {
    const event = typeof eventOrType === "string"
      ? { type: eventOrType }
      : { ...eventOrType };
    event.target = event.target || this;
    event.currentTarget = this;
    event.defaultPrevented = Boolean(event.defaultPrevented);
    event.preventDefault = event.preventDefault || (() => {
      event.defaultPrevented = true;
    });
    const handlers = this.listeners.get(String(event.type || "")) || [];
    for (const handler of handlers) {
      handler(event);
    }
    return !event.defaultPrevented;
  }

  set textContent(value) {
    this._textContent = String(value ?? "");
    this.children = [];
    if (this.ownerDocument && typeof this.ownerDocument.recalculateLayout === "function") {
      this.ownerDocument.recalculateLayout();
    }
  }

  get textContent() {
    if (this.children.length) {
      return this.children.map((child) => child.textContent).join("");
    }
    return this._textContent;
  }

  set innerHTML(value) {
    this._textContent = String(value ?? "").replace(/<[^>]+>/g, "");
    this.children = [];
    if (this.ownerDocument && typeof this.ownerDocument.recalculateLayout === "function") {
      this.ownerDocument.recalculateLayout();
    }
  }

  get innerHTML() {
    if (this.children.length) {
      return this.children.map((child) => child.innerHTML || child.textContent).join("");
    }
    return this._textContent;
  }

  getBoundingClientRect() {
    if (!windowObject || this.parentNode !== this.ownerDocument.querylogRowsNode) {
      return {
        top: 0,
        bottom: 0,
        left: 0,
        right: 0,
        width: 0,
        height: 0,
      };
    }

    const index = this.parentNode.children.indexOf(this);
    const top = BASE_PAGE_HEIGHT + (index * ROW_HEIGHT) - Number(windowObject.scrollY || 0);
    const height = ROW_HEIGHT;
    return {
      top,
      bottom: top + height,
      left: 0,
      right: 800,
      width: 800,
      height,
    };
  }
}

class Document {
  constructor() {
    this.body = new Element("body", this);
    this.documentElement = new Element("html", this);
    this.querylogRowsNode = null;
    this.bodyClientHeightFollowsScrollHeight = false;
  }

  createElement(tagName) {
    return new Element(tagName, this);
  }

  getElementById(id) {
    const targetId = String(id || "");
    const visit = (node) => {
      if (node.id === targetId) return node;
      for (const child of node.children) {
        const match = visit(child);
        if (match) return match;
      }
      return null;
    };
    return visit(this.body);
  }

  recalculateLayout() {
    const rowCount = this.querylogRowsNode ? this.querylogRowsNode.children.length : 0;
    const scrollHeight = BASE_PAGE_HEIGHT + (rowCount * ROW_HEIGHT) + 120;
    const viewportHeight = windowObject ? Number(windowObject.innerHeight || PAGE_VIEWPORT_HEIGHT) : PAGE_VIEWPORT_HEIGHT;
    this.body.scrollHeight = scrollHeight;
    this.documentElement.scrollHeight = scrollHeight;
    this.body.clientHeight = this.bodyClientHeightFollowsScrollHeight ? scrollHeight : viewportHeight;
    this.documentElement.clientHeight = viewportHeight;
  }
}

class FakeIntersectionObserver {
  constructor(callback) {
    this.callback = callback;
    this.targets = new Set();
    observerInstances.push(this);
  }

  observe(target) {
    this.targets.add(target);
  }

  unobserve(target) {
    this.targets.delete(target);
  }

  disconnect() {
    this.targets.clear();
  }
}

class FakeResponse {
  constructor(url, payload, status = 200, jsonFailureKey = "") {
    this.url = String(url || "");
    this.status = status;
    this.ok = status >= 200 && status < 300;
    this._payload = payload;
    this._jsonFailureKey = jsonFailureKey;
  }

  async json() {
    if (this._jsonFailureKey) {
      const remainingFailures = jsonReadFailures.get(this._jsonFailureKey) || 0;
      if (remainingFailures > 0) {
        jsonReadFailures.set(this._jsonFailureKey, remainingFailures - 1);
        throw new Error("Failed to fetch");
      }
    }
    return JSON.parse(JSON.stringify(this._payload));
  }
}

const abortError = () => {
  const error = new Error("Aborted");
  error.name = "AbortError";
  return error;
};

const dataset = new Map();
const detailDataset = new Map();
const detailDelays = new Map();
const detailFailures = new Map();
const delays = new Map();
const jsonReadFailures = new Map();
const fetchLog = [];

const datasetKey = ({ search = "", responseStatus = "all", pageSize = "2", cursor = "" }) => (
  `${search}|${responseStatus}|${pageSize}|${cursor}`
);

const buildPagePayload = (
  prefix,
  pageIndex,
  pageSize,
  totalPages,
  responseStatus = "processed",
  {
    detailDelayByRowNumber = {},
    detailFailureByRowNumber = {},
  } = {},
) => {
  const rows = [];
  const pageNumber = pageIndex + 1;
  const blocked = responseStatus === "blocked";
  for (let index = 0; index < pageSize; index += 1) {
    const rowNumber = pageIndex * pageSize + index + 1;
    const rowFingerprint = `${prefix}-fingerprint-${rowNumber}`;
    const row = {
      time: `2026-04-21T12:00:${String(rowNumber).padStart(2, "0")}Z`,
      rowFingerprint,
      identityId: prefix,
      identity: { id: prefix, label: prefix },
      originalClient: `${prefix}-client`,
      question: { name: `${prefix}-${rowNumber}` },
      status: blocked ? "blocked" : "processed",
      client_proto: "doh",
      detailMode: "summary",
      display: {
        clientLabel: `${prefix}-client`,
        statusLabel: blocked ? "blocked" : "processed",
        protocolLabel: "doh",
        statusTone: blocked ? "blocked" : "ok",
      },
    };
    rows.push(row);
    detailDataset.set(rowFingerprint, { ...row, detailMode: "full", extra: `detail-${rowNumber}` });
    if (detailDelayByRowNumber[rowNumber]) {
      detailDelays.set(rowFingerprint, Number(detailDelayByRowNumber[rowNumber]));
    }
    if (detailFailureByRowNumber[rowNumber]) {
      detailFailures.set(rowFingerprint, String(detailFailureByRowNumber[rowNumber]));
    }
  }
  const nextCursor = pageNumber < totalPages ? `${prefix}-cursor-${pageNumber + 1}` : "";
  return {
    data: rows,
    rows,
    next_cursor: nextCursor,
    has_more: Boolean(nextCursor),
    meta: { hasMore: Boolean(nextCursor) },
  };
};

const registerScenario = ({
  search = "",
  responseStatus = "all",
  pageSize = "2",
  prefix,
  totalPages,
  delayByCursor = {},
  detailDelayByRowNumber = {},
  detailFailureByRowNumber = {},
}) => {
  for (let pageIndex = 0; pageIndex < totalPages; pageIndex += 1) {
    const cursor = pageIndex === 0 ? "" : `${prefix}-cursor-${pageIndex + 1}`;
    dataset.set(
      datasetKey({ search, responseStatus, pageSize, cursor }),
      buildPagePayload(prefix, pageIndex, Number(pageSize), totalPages, responseStatus, {
        detailDelayByRowNumber,
        detailFailureByRowNumber,
      }),
    );
    delays.set(
      datasetKey({ search, responseStatus, pageSize, cursor }),
      Number(delayByCursor[cursor] || 0),
    );
  }
};

registerScenario({
  prefix: "default",
  totalPages: 7,
  delayByCursor: { "default-cursor-7": 90 },
});
registerScenario({
  pageSize: "50",
  prefix: "default50",
  totalPages: 7,
  delayByCursor: {
    "default50-cursor-3": 20,
    "default50-cursor-6": 30,
  },
});
jsonReadFailures.set(datasetKey({
  pageSize: "50",
  cursor: "default50-cursor-3",
}), 1);
registerScenario({
  search: "manual",
  prefix: "manual",
  totalPages: 8,
  delayByCursor: { "manual-cursor-8": 90 },
});
registerScenario({
  search: "stale",
  prefix: "stale",
  totalPages: 4,
  delayByCursor: { "stale-cursor-2": 90 },
});
registerScenario({
  search: "fresh",
  prefix: "fresh",
  totalPages: 2,
});
registerScenario({
  search: "fresh",
  pageSize: "3",
  prefix: "fresh3",
  totalPages: 2,
});
registerScenario({
  search: "tall",
  prefix: "tall",
  totalPages: 4,
});
registerScenario({
  responseStatus: "blocked",
  prefix: "blocked",
  totalPages: 2,
  detailDelayByRowNumber: {
    1: 90,
  },
  detailFailureByRowNumber: {
    2: "Failed to load row details.",
  },
});

const documentRef = new Document();

const makeNode = (tagName, id, initialValue = "") => {
  const node = documentRef.createElement(tagName);
  if (id) node.setAttribute("id", id);
  if (initialValue !== undefined) node.value = initialValue;
  return node;
};

const wrap = documentRef.createElement("div");
documentRef.body.appendChild(wrap);

const statusNode = makeNode("div", "status");
const searchNode = makeNode("input", "search", "");
const identityNode = makeNode("input", "identity-filter", "");
const responseNode = makeNode("select", "response", "all");
const pageSizeNode = makeNode("input", "page-size", "2");
const refreshNode = makeNode("button", "refresh");
const rowsNode = makeNode("tbody", "rows");
const loadMoreNode = makeNode("button", "load-more");
const renderBottomSentinelNode = makeNode("div", "render-bottom-sentinel");
const logoutNode = makeNode("button", "logout");
const detailOverlayNode = makeNode("div", "detail-overlay");
const detailBackdropNode = makeNode("div", "detail-backdrop");
const detailPanelNode = makeNode("section", "detail-panel");
const detailTitleNode = makeNode("div", "detail-title");
const detailMetaNode = makeNode("div", "detail-meta");
const detailStateNode = makeNode("div", "detail-state");
const detailOutputNode = makeNode("pre", "detail-output");
const clearDetailNode = makeNode("button", "clear-detail");
detailOverlayNode.className = "detail-overlay";
detailStateNode.className = "detail-state";
detailOutputNode.className = "detail-body";
detailOverlayNode.hidden = false;
detailStateNode.hidden = false;
detailOutputNode.hidden = false;

wrap.appendChild(statusNode);
wrap.appendChild(searchNode);
wrap.appendChild(identityNode);
wrap.appendChild(responseNode);
wrap.appendChild(pageSizeNode);
wrap.appendChild(refreshNode);
wrap.appendChild(rowsNode);
wrap.appendChild(loadMoreNode);
wrap.appendChild(renderBottomSentinelNode);
wrap.appendChild(logoutNode);
wrap.appendChild(detailOverlayNode);
detailOverlayNode.appendChild(detailBackdropNode);
detailOverlayNode.appendChild(detailPanelNode);
detailPanelNode.appendChild(detailTitleNode);
detailPanelNode.appendChild(detailMetaNode);
detailPanelNode.appendChild(detailStateNode);
detailPanelNode.appendChild(detailOutputNode);
detailPanelNode.appendChild(clearDetailNode);

documentRef.querylogRowsNode = rowsNode;

const windowListeners = new Map();
windowObject = {
  document: documentRef,
  location: {
    pathname: "/dns/queries",
    search: "?page_size=2",
    hash: "",
    origin: "https://dns.example.test",
    assign(url) {
      const parsed = new URL(String(url || ""), this.origin);
      this.pathname = parsed.pathname;
      this.search = parsed.search;
      this.hash = parsed.hash;
    },
  },
  history: {
    replaceState(_state, _title, url) {
      const parsed = new URL(String(url || ""), windowObject.location.origin);
      windowObject.location.pathname = parsed.pathname;
      windowObject.location.search = parsed.search;
      windowObject.location.hash = parsed.hash;
    },
  },
  innerHeight: PAGE_VIEWPORT_HEIGHT,
  scrollY: 0,
  pageYOffset: 0,
  setTimeout,
  clearTimeout,
  addEventListener(type, handler) {
    const key = String(type || "");
    if (!windowListeners.has(key)) windowListeners.set(key, []);
    windowListeners.get(key).push(handler);
  },
  dispatchEvent(eventOrType) {
    const event = typeof eventOrType === "string" ? { type: eventOrType } : { ...eventOrType };
    const handlers = windowListeners.get(String(event.type || "")) || [];
    for (const handler of handlers) {
      handler(event);
    }
  },
  fetch(url, init = {}) {
    const resolvedUrl = String(url || "");
    const method = String(init.method || "GET").toUpperCase();
    fetchLog.push({ url: resolvedUrl, method });
    if (resolvedUrl.includes("/dns/api/session") && method === "DELETE") {
      return Promise.resolve(new FakeResponse(resolvedUrl, { ok: true }));
    }
    if (resolvedUrl.includes("/dns/api/queries/")) {
      const fingerprint = decodeURIComponent(resolvedUrl.split("/dns/api/queries/")[1] || "");
      const payload = detailDataset.get(fingerprint);
      const waitMs = detailDelays.get(fingerprint) || 0;
      const failure = detailFailures.get(fingerprint);
      return new Promise((resolve, reject) => {
        const signal = init.signal;
        if (signal && signal.aborted) {
          reject(abortError());
          return;
        }
        const timer = setTimeout(() => {
          if (signal && signal.aborted) {
            reject(abortError());
            return;
          }
          if (!payload) {
            resolve(new FakeResponse(resolvedUrl, { error: `No row detail for ${fingerprint}` }, 404));
            return;
          }
          if (failure) {
            resolve(new FakeResponse(resolvedUrl, { error: failure }, 500));
            return;
          }
          resolve(new FakeResponse(resolvedUrl, payload));
        }, waitMs);
        if (signal) {
          signal.addEventListener("abort", () => {
            clearTimeout(timer);
            reject(abortError());
          }, { once: true });
        }
      });
    }
    if (!resolvedUrl.includes("/dns/api/queries")) {
      return Promise.reject(new Error(`Unexpected fetch ${resolvedUrl}`));
    }

    const requestUrl = new URL(resolvedUrl, windowObject.location.origin);
    const key = datasetKey({
      search: requestUrl.searchParams.get("search") || "",
      responseStatus: requestUrl.searchParams.get("response_status") || "all",
      pageSize: requestUrl.searchParams.get("page_size") || "2",
      cursor: requestUrl.searchParams.get("cursor") || "",
    });
    if (!dataset.has(key)) {
      return Promise.resolve(new FakeResponse(resolvedUrl, { error: `No payload for ${key}` }, 500));
    }

    const payload = dataset.get(key);
    const waitMs = delays.get(key) || 0;
    const signal = init.signal;
    return new Promise((resolve, reject) => {
      if (signal && signal.aborted) {
        reject(abortError());
        return;
      }
      const timer = setTimeout(() => {
        if (signal && signal.aborted) {
          reject(abortError());
          return;
        }
        resolve(new FakeResponse(resolvedUrl, payload, 200, key));
      }, waitMs);
      if (signal) {
        signal.addEventListener("abort", () => {
          clearTimeout(timer);
          reject(abortError());
        }, { once: true });
      }
    });
  },
};

documentRef.recalculateLayout();

const context = {
  window: windowObject,
  document: documentRef,
  location: windowObject.location,
  history: windowObject.history,
  fetch: windowObject.fetch,
  URL,
  URLSearchParams,
  AbortController,
  IntersectionObserver: FakeIntersectionObserver,
  setTimeout,
  clearTimeout,
  console,
};
windowObject.window = windowObject;
windowObject.globalThis = windowObject;
windowObject.IntersectionObserver = FakeIntersectionObserver;
context.globalThis = windowObject;

vm.runInNewContext(scriptSource, context, { filename: "querylog.html" });

const rowNames = () => rowsNode.children.map((row) => row.children[3] ? row.children[3].textContent : "");
const rowCount = () => rowsNode.children.length;
const hasClass = (node, className) => String(node && node.className ? node.className : "")
  .split(/\s+/)
  .filter(Boolean)
  .includes(className);
const queryFetches = (matcher) => fetchLog.filter((entry) => (
  entry.url.includes("/dns/api/queries?") && (!matcher || matcher(entry.url))
));
const detailFetches = () => fetchLog.filter((entry) => entry.url.includes("/dns/api/queries/") && !entry.url.includes("/dns/api/queries?"));
const sessionFetches = () => fetchLog.filter((entry) => entry.url.includes("/dns/api/session"));

const waitFor = async (predicate, label, timeoutMs = 2000) => {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    if (predicate()) return;
    await delay(10);
  }
  fail(`Timed out waiting for ${label}`);
};

const pressEnter = (node) => {
  node.dispatchEvent({
    type: "keydown",
    key: "Enter",
    preventDefault() {},
  });
};

const setScrollTop = (value) => {
  const maxScroll = Math.max(
    0,
    Math.max(
      Number(documentRef.body.scrollHeight || 0),
      Number(documentRef.documentElement.scrollHeight || 0),
    ) - Number(windowObject.innerHeight || PAGE_VIEWPORT_HEIGHT),
  );
  const nextValue = Math.max(0, Math.min(Number(value || 0), maxScroll));
  windowObject.scrollY = nextValue;
  windowObject.pageYOffset = nextValue;
  documentRef.body.scrollTop = nextValue;
  documentRef.documentElement.scrollTop = nextValue;
  windowObject.dispatchEvent({ type: "scroll" });
};

const scrollToBottom = () => {
  setScrollTop(Number.MAX_SAFE_INTEGER);
};

const activeObservedTargets = () => observerInstances.flatMap((observer) => Array.from(observer.targets));

const expectSingleObservedTarget = (label) => {
  const targets = activeObservedTargets();
  if (targets.length !== 1) {
    fail(`${label} should have exactly one observed bottom sentinel (saw ${targets.length})`);
  }
  return targets[0];
};

const triggerObservedIntersection = (target) => {
  let triggered = 0;
  for (const observer of observerInstances) {
    if (!observer.targets.has(target)) continue;
    triggered += 1;
    observer.callback([
      {
        target,
        isIntersecting: true,
        intersectionRatio: 1,
      },
    ], observer);
  }
  if (triggered === 0) {
    fail("Expected an active observer target before triggering intersection");
  }
};

const expectRowNames = (expected, label) => {
  const actual = rowNames();
  if (JSON.stringify(actual) !== JSON.stringify(expected)) {
    fail(`${label} (saw ${JSON.stringify(actual)})`);
  }
};

const detailPopupVisible = () => hasClass(detailOverlayNode, "is-open");
const detailLoadingVisible = () => hasClass(detailStateNode, "is-visible") && detailStateNode.textContent.includes("Loading row details");
const detailErrorVisible = () => hasClass(detailStateNode, "is-visible") && hasClass(detailStateNode, "error");
const detailOutputVisible = () => hasClass(detailOutputNode, "is-visible");

(async () => {
  if (detailPopupVisible()) {
    fail("Detail popup should start closed even when hidden cannot be trusted");
  }
  await waitFor(() => queryFetches().length === 6, "initial bootstrap fetch chain");
  await waitFor(() => rowCount() === 2, "initial first page rendered");
  expectRowNames([
    "default-1",
    "default-2",
  ], "Initial bootstrap should render only the first page while staging the rest");
  if (statusNode.textContent !== "") {
    fail(`Initial bootstrap should clear status after success (saw ${JSON.stringify(statusNode.textContent)})`);
  }
  if (detailFetches().length !== 0) {
    fail(`Row details should stay on demand during bootstrap loading (saw ${detailFetches().length} detail fetches)`);
  }
  const bootstrapSentinel = expectSingleObservedTarget("initial bootstrap");
  if (bootstrapSentinel.id !== "render-bottom-sentinel") {
    fail(`Initial observer should watch the render sentinel (saw ${bootstrapSentinel.id || "missing"})`);
  }
  if (loadMoreNode.hidden !== false) {
    fail("Load more should stay available while prefetched pages are staged");
  }
  setScrollTop(32);
  await delay(140);
  if (rowCount() !== 2) {
    fail(`Ordinary scrolling above the bottom should not reveal more rows (saw ${rowCount()})`);
  }

  scrollToBottom();
  scrollToBottom();
  scrollToBottom();
  await waitFor(
    () => queryFetches((url) => url.includes("cursor=default-cursor-7")).length === 1,
    "single refill fetch for default-cursor-7 after bottom scroll",
  );
  await waitFor(() => rowCount() === 4, "first staged page revealed after scroll fallback");
  expectRowNames([
    "default-1",
    "default-2",
    "default-3",
    "default-4",
  ], "Scroll fallback should reveal exactly one staged page at a time");
  await delay(30);
  if (queryFetches((url) => url.includes("cursor=default-cursor-7")).length !== 1) {
    fail(`Fast scroll fallback should not duplicate the same refill request (saw ${queryFetches((url) => url.includes("cursor=default-cursor-7")).length})`);
  }
  for (const expectedCount of [6, 8, 10, 12, 14]) {
    scrollToBottom();
    await waitFor(() => rowCount() === expectedCount, `default reveal to ${expectedCount} rows`);
  }
  expectRowNames([
    "default-1",
    "default-2",
    "default-3",
    "default-4",
    "default-5",
    "default-6",
    "default-7",
    "default-8",
    "default-9",
    "default-10",
    "default-11",
    "default-12",
    "default-13",
    "default-14",
  ], "Repeated bottom reaches should eventually reveal the full default dataset");
  if (loadMoreNode.hidden !== true) {
    fail("Load more should hide once the final default page has been loaded");
  }

  pageSizeNode.value = "50";
  searchNode.value = "";
  responseNode.value = "all";
  documentRef.bodyClientHeightFollowsScrollHeight = true;
  setScrollTop(0);
  pressEnter(pageSizeNode);
  await waitFor(
    () => queryFetches((url) => url.includes("page_size=50")).length === 7,
    "default page-size 50 five-page buffer bootstrap chain",
  );
  await waitFor(
    () => rowCount() === 50 && rowNames()[0] === "default50-1",
    "default page-size 50 first page rendered",
  );
  await delay(40);
  if (rowCount() !== 50) {
    fail(`Default page-size 50 should keep prefetched rows hidden until reveal (saw ${rowCount()} rendered rows)`);
  }
  if (loadMoreNode.hidden !== false || loadMoreNode.disabled !== false) {
    fail("Default page-size 50 should keep Load more available while hidden pages are staged");
  }
  if (queryFetches((url) => url.includes("page_size=50") && url.includes("cursor=default50-cursor-2")).length !== 1) {
    fail("Default page-size 50 should prefetch the second page exactly once");
  }
  if (queryFetches((url) => url.includes("page_size=50") && url.includes("cursor=default50-cursor-3")).length !== 2) {
    fail("Default page-size 50 should retry a failed buffered response body once");
  }
  for (const cursor of ["default50-cursor-4", "default50-cursor-5", "default50-cursor-6"]) {
    if (queryFetches((url) => url.includes("page_size=50") && url.includes(`cursor=${cursor}`)).length !== 1) {
      fail(`Default page-size 50 should prefetch ${cursor} exactly once`);
    }
  }
  scrollToBottom();
  await waitFor(() => rowCount() === 100, "default page-size 50 second page reveal");
  if (rowNames()[99] !== "default50-100") {
    fail(`Default page-size 50 should reveal page two at the bottom (saw ${JSON.stringify(rowNames().slice(95, 100))})`);
  }
  await waitFor(
    () => queryFetches((url) => url.includes("page_size=50") && url.includes("cursor=default50-cursor-7")).length === 1,
    "default page-size 50 buffer refill after first reveal",
  );
  scrollToBottom();
  await waitFor(() => rowCount() === 150, "default page-size 50 final page reveal");
  if (rowNames()[149] !== "default50-150") {
    fail(`Default page-size 50 should keep revealing one page at a time (saw ${JSON.stringify(rowNames().slice(145, 150))})`);
  }
  if (loadMoreNode.hidden !== false) {
    fail("Load more should stay available while the default page-size 50 buffer still has rows staged");
  }
  documentRef.bodyClientHeightFollowsScrollHeight = false;
  documentRef.recalculateLayout();

  pageSizeNode.value = "2";
  searchNode.value = "manual";
  setScrollTop(0);
  pressEnter(searchNode);
  await waitFor(
    () => queryFetches((url) => url.includes("search=manual")).length === 6,
    "manual scenario bootstrap",
  );
  await waitFor(() => rowCount() === 2 && rowNames()[0] === "manual-1", "manual scenario first page rendered");
  if (detailFetches().length !== 0) {
    fail(`Auto-loading should still avoid detail prefetches after query changes (saw ${detailFetches().length})`);
  }
  const manualSentinel = expectSingleObservedTarget("manual bootstrap");
  if (manualSentinel.id !== "render-bottom-sentinel") {
    fail(`Manual observer should keep watching the render sentinel (saw ${manualSentinel.id || "missing"})`);
  }
  setScrollTop(1);
  triggerObservedIntersection(manualSentinel);
  await delay(140);
  if (rowCount() !== 2) {
    fail(`Observer hints above the bottom should not reveal more rows (saw ${rowCount()})`);
  }
  scrollToBottom();
  triggerObservedIntersection(manualSentinel);
  await waitFor(
    () => queryFetches((url) => url.includes("search=manual") && url.includes("cursor=manual-cursor-7")).length === 1,
    "manual observer buffer refill request",
  );
  await waitFor(() => rowCount() === 4 && rowNames()[3] === "manual-4", "manual observer reveal");
  if (loadMoreNode.hidden !== false) {
    fail("Load more should stay visible when manual rows still have staged pages left");
  }
  loadMoreNode.dispatchEvent({ type: "click" });
  await waitFor(() => rowCount() === 6, "manual load-more reveal");
  expectRowNames([
    "manual-1",
    "manual-2",
    "manual-3",
    "manual-4",
    "manual-5",
    "manual-6",
  ], "Manual load more should reveal one staged page without waiting for a fresh fetch");
  await waitFor(
    () => queryFetches((url) => url.includes("search=manual") && url.includes("cursor=manual-cursor-8")).length === 1,
    "manual load-more background refill request",
  );

  searchNode.value = "stale";
  pressEnter(searchNode);
  await waitFor(
    () => queryFetches((url) => url.includes("search=stale") && !url.includes("cursor=")).length === 1,
    "stale initial request",
  );
  await waitFor(
    () => queryFetches((url) => url.includes("search=stale") && url.includes("cursor=stale-cursor-2")).length === 1,
    "stale delayed bootstrap request",
  );
  searchNode.value = "fresh";
  pageSizeNode.value = "3";
  pressEnter(pageSizeNode);
  await waitFor(
    () => queryFetches((url) => url.includes("search=fresh") && url.includes("page_size=3")).length === 2,
    "fresh page-size reset bootstrap",
  );
  await waitFor(() => rowCount() === 3, "fresh page-size first page rendered");
  await delay(140);
  if (rowNames().some((name) => name.startsWith("stale-"))) {
    fail(`Stale append results should not replace the latest query rows (saw ${JSON.stringify(rowNames())})`);
  }
  expectRowNames([
    "fresh3-1",
    "fresh3-2",
    "fresh3-3",
  ], "Fresh query should own the page after reset while keeping page two staged");

  windowObject.innerHeight = 520;
  documentRef.recalculateLayout();
  windowObject.dispatchEvent({ type: "resize" });
  searchNode.value = "tall";
  pageSizeNode.value = "2";
  setScrollTop(0);
  pressEnter(searchNode);
  await waitFor(
    () => queryFetches((url) => url.includes("search=tall")).length === 4,
    "tall viewport staged fetch chain",
  );
  await waitFor(() => rowCount() === 8, "tall viewport auto-reveal until scrollable");
  expectRowNames([
    "tall-1",
    "tall-2",
    "tall-3",
    "tall-4",
    "tall-5",
    "tall-6",
    "tall-7",
    "tall-8",
  ], "Large viewport should reveal staged pages only until the page becomes scrollable");
  windowObject.innerHeight = PAGE_VIEWPORT_HEIGHT;
  documentRef.recalculateLayout();
  windowObject.dispatchEvent({ type: "resize" });

  searchNode.value = "";
  pageSizeNode.value = "2";
  responseNode.value = "blocked";
  setScrollTop(0);
  responseNode.dispatchEvent({ type: "change" });
  await waitFor(
    () => queryFetches((url) => url.includes("response_status=blocked")).length === 2,
    "blocked filter bootstrap",
  );
  await waitFor(() => rowCount() === 2, "blocked first page rendered");
  scrollToBottom();
  await waitFor(() => rowCount() === 4, "blocked staged reveal");
  if (rowsNode.children.some((row) => !hasClass(row, "row-blocked"))) {
    fail("Blocked query rows should stay styled as blocked after buffered loading");
  }
  if (detailFetches().length !== 0) {
    fail(`Blocked rows should still wait for explicit detail clicks (saw ${detailFetches().length})`);
  }

  const firstBlockedRow = rowsNode.children[0];
  firstBlockedRow.dispatchEvent({ type: "click" });
  await waitFor(detailPopupVisible, "blocked detail popup open");
  await waitFor(() => detailFetches().length === 1, "blocked row detail fetch");
  await waitFor(detailLoadingVisible, "blocked detail loading state");
  if (documentRef.body.style.overflow !== "hidden" || documentRef.documentElement.style.overflow !== "hidden") {
    fail("Opening row details should lock page scroll");
  }
  if (!hasClass(firstBlockedRow, "row-blocked") || !hasClass(firstBlockedRow, "selected")) {
    fail(`Blocked row should remain highlighted while selected (saw ${JSON.stringify(firstBlockedRow.className)})`);
  }
  if (detailOutputVisible() || detailOutputNode.textContent !== "") {
    fail("Loading detail state should not show stale detail content");
  }

  clearDetailNode.dispatchEvent({ type: "click" });
  await waitFor(() => !detailPopupVisible(), "detail popup closed by button while loading");
  await delay(140);
  if (detailPopupVisible()) {
    fail("Closing the detail popup while loading should keep it closed");
  }
  if (documentRef.body.style.overflow !== "" || documentRef.documentElement.style.overflow !== "") {
    fail("Closing row details should restore page scroll");
  }
  if (rowsNode.children.some((row) => hasClass(row, "selected"))) {
    fail("Closing row details should clear row selection");
  }

  firstBlockedRow.dispatchEvent({ type: "click" });
  await waitFor(detailPopupVisible, "blocked detail popup reopened");
  await waitFor(detailLoadingVisible, "blocked detail loading state after reopen");
  await waitFor(() => detailOutputNode.textContent.includes("blocked-fingerprint-1"), "blocked detail payload rendered");
  if (hasClass(detailStateNode, "is-visible") || !detailOutputVisible()) {
    fail("Successful detail loads should hide the loading state and show the payload");
  }

  const secondBlockedRow = rowsNode.children[1];
  secondBlockedRow.dispatchEvent({ type: "click" });
  await waitFor(() => detailFetches().length === 3, "second blocked row detail fetch");
  await waitFor(detailErrorVisible, "second blocked row detail error state");
  if (detailStateNode.textContent !== "Failed to load row details.") {
    fail(`Detail failures should show the error message in the popup (saw ${JSON.stringify(detailStateNode.textContent)})`);
  }
  if (detailOutputVisible() || detailOutputNode.textContent !== "") {
    fail("Detail failures should not keep stale payload content visible");
  }
  if (detailPanelNode.parentNode !== detailOverlayNode || detailOverlayNode.children.length !== 2) {
    fail("Detail popup should be reused instead of duplicating overlay structure");
  }
  if (!hasClass(secondBlockedRow, "selected") || hasClass(firstBlockedRow, "selected")) {
    fail("Opening another row should move selection to the new row");
  }

  const thirdBlockedRow = rowsNode.children[2];
  thirdBlockedRow.dispatchEvent({ type: "click" });
  await waitFor(() => detailFetches().length === 4, "third blocked row detail fetch");
  await waitFor(() => detailOutputNode.textContent.includes("blocked-fingerprint-3"), "third blocked detail payload rendered");
  await waitFor(() => !hasClass(detailStateNode, "is-visible"), "third blocked row hides loading state after success");
  windowObject.dispatchEvent({ type: "keydown", key: "Escape", preventDefault() {} });
  await waitFor(() => !detailPopupVisible(), "detail popup closed by Escape");
  if (documentRef.body.style.overflow !== "" || documentRef.documentElement.style.overflow !== "") {
    fail("Closing row details should restore page scroll");
  }
  if (rowsNode.children.some((row) => hasClass(row, "selected"))) {
    fail("Closing row details should clear row selection");
  }

  thirdBlockedRow.dispatchEvent({ type: "click" });
  await waitFor(detailPopupVisible, "detail popup reopened for backdrop close");
  detailBackdropNode.dispatchEvent({ type: "click" });
  await waitFor(() => !detailPopupVisible(), "detail popup closed by backdrop");

  firstBlockedRow.dispatchEvent({ type: "click" });
  await waitFor(detailPopupVisible, "detail popup open before reset");
  searchNode.value = "manual";
  responseNode.value = "all";
  pressEnter(searchNode);
  await waitFor(() => !detailPopupVisible(), "detail popup closed by query reset");
  if (rowsNode.children.some((row) => hasClass(row, "selected"))) {
    fail("Query resets should clear selection after closing the popup");
  }
  await waitFor(() => rowCount() === 2 && rowNames()[0] === "manual-1", "manual rows restored after popup reset");

  logoutNode.dispatchEvent({ type: "click" });
  await waitFor(() => sessionFetches().length === 1, "logout request");
  await waitFor(() => windowObject.location.pathname === "/dns/login", "logout redirect");
  if (windowObject.location.pathname !== "/dns/login") {
    fail(`Logout should redirect to /dns/login (saw ${windowObject.location.pathname})`);
  }

  console.log("PASS: querylog.html stages rows ahead, reveals one page at a time at the bottom, and opens row details in a reusable modal popup with on-demand fetches");
})().catch((error) => {
  console.error(`FAIL: ${error && error.message ? error.message : error}`);
  process.exit(1);
});
NODE
