import { html, reactive } from '@arrow-js/core';

const DEFAULT_TIME_ZONE = 'Europe/Riga';
const DEFAULT_DAY_COUNT = 30;
const DEFAULT_SECONDS_PER_TICK = 5;
const MILLIS_PER_DAY = 24 * 60 * 60 * 1000;

function integerInRange(value, fallback, minimum, maximum) {
  const parsed = Math.floor(Number(value));
  return Number.isFinite(parsed) && parsed >= minimum && parsed <= maximum ? parsed : fallback;
}

function normalizedScope(value) {
  return String(value || '').trim().toLowerCase();
}

function normalizedDay(value) {
  const day = String(value || '').trim();
  return /^\d{4}-\d{2}-\d{2}$/.test(day) ? day : '';
}

function currentCalendarDay(nowValue, timeZone) {
  const now = new Date(nowValue);
  const safeNow = Number.isFinite(now.getTime()) ? now : new Date();
  let formatter;
  try {
    formatter = new Intl.DateTimeFormat('en-CA', {
      timeZone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit'
    });
  } catch (_) {
    formatter = new Intl.DateTimeFormat('en-CA', {
      timeZone: DEFAULT_TIME_ZONE,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit'
    });
  }
  const parts = Object.fromEntries(formatter.formatToParts(safeNow).map((part) => [part.type, part.value]));
  return `${parts.year}-${parts.month}-${parts.day}`;
}

export function activityDayKeys(nowValue, timeZone = DEFAULT_TIME_ZONE, count = DEFAULT_DAY_COUNT) {
  const dayCount = integerInRange(count, DEFAULT_DAY_COUNT, 1, 90);
  const currentDay = currentCalendarDay(nowValue, timeZone);
  const [year, month, day] = currentDay.split('-').map(Number);
  const cursor = Date.UTC(year, month - 1, day, 12);
  return Array.from({ length: dayCount }, (_, index) => new Date(cursor - (index * MILLIS_PER_DAY)).toISOString().slice(0, 10));
}

export function formatActivityDuration(value) {
  let seconds = Math.max(0, Math.floor(Number(value) || 0));
  const hours = Math.floor(seconds / 3600);
  seconds -= hours * 3600;
  const minutes = Math.floor(seconds / 60);
  seconds -= minutes * 60;
  const parts = [];
  if (hours > 0) parts.push(`${hours}h`);
  if (minutes > 0) parts.push(`${minutes}m`);
  if (seconds > 0 || parts.length === 0) parts.push(`${seconds}s`);
  return parts.join(' ');
}

function normalizedTick(value) {
  const tick = Math.floor(Number(value));
  if (!Number.isFinite(tick) || tick <= 0) return 0;
  return Math.min(tick, Number.MAX_SAFE_INTEGER);
}

const ACTION_METRICS = [
  { key: 'registration', shortLabel: 'Reg', label: 'Registrations', accessibleLabel: 'Registration' },
  { key: 'controlCode', shortLabel: 'Code', label: 'Control codes', accessibleLabel: 'Control-code generation' }
];

function emptyCounts() {
  return { registrationAttempts: 0, registrationSuccesses: 0, controlCodeAttempts: 0, controlCodeSuccesses: 0 };
}

function addCounts(total, counts) {
  for (const { key } of ACTION_METRICS) {
    for (const suffix of ['Attempts', 'Successes']) {
      const field = `${key}${suffix}`;
      total[field] = Math.min(Number.MAX_SAFE_INTEGER, total[field] + counts[field]);
    }
  }
}

function metricsFor(counts) {
  return ACTION_METRICS.filter(({ key }) => counts[`${key}Attempts`] > 0).map((metric) => ({
    ...metric,
    text: `${counts[`${metric.key}Successes`]}/${counts[`${metric.key}Attempts`]}`,
    description: `${metric.accessibleLabel}: ${counts[`${metric.key}Successes`]} successful, ${counts[`${metric.key}Attempts`]} accepted requests`
  }));
}

function trackingStartLabel(value, timeZone) {
  const date = new Date(value);
  if (!value || !Number.isFinite(date.getTime())) return 'Action tracking has not started yet.';
  let time;
  try {
    time = new Intl.DateTimeFormat('en-GB', { timeZone, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(date);
  } catch (_) {
    return trackingStartLabel(value, DEFAULT_TIME_ZONE);
  }
  return `Action counts since ${currentCalendarDay(value, timeZone)} at ${time} (${timeZone}).`;
}

export function buildActivityStatisticsModel(payload = {}) {
  const timeZone = String(payload.timeZone || DEFAULT_TIME_ZONE).trim() || DEFAULT_TIME_ZONE;
  const dayCount = integerInRange(payload.days, DEFAULT_DAY_COUNT, 1, 90);
  const secondsPerTick = integerInRange(payload.secondsPerTick, DEFAULT_SECONDS_PER_TICK, 1, 3600);
  const dayKeys = activityDayKeys(payload.serverTime || Date.now(), timeZone, dayCount);
  const visibleDays = new Set(dayKeys);
  const memberByScope = new Map();

  for (const rawMember of Array.isArray(payload.members) ? payload.members : []) {
    const accountScopeId = normalizedScope(rawMember && rawMember.accountScopeId);
    if (!accountScopeId) continue;
    const member = {
      accountScopeId,
      email: String(rawMember && rawMember.email || '').trim(),
      active: rawMember && rawMember.active === true
    };
    const previous = memberByScope.get(accountScopeId);
    if (previous && previous.active && !member.active) continue;
    memberByScope.set(accountScopeId, member);
  }

  const cells = new Map();
  const cellFor = (day, hour, accountScopeId) => {
    const key = `${day}|${hour}|${accountScopeId}`;
    if (!cells.has(key)) cells.set(key, { day, hour, accountScopeId, ticks: 0, ...emptyCounts() });
    return cells.get(key);
  };
  for (const row of Array.isArray(payload.pageActivityDaily) ? payload.pageActivityDaily : []) {
    const day = normalizedDay(row && row.day);
    const accountScopeId = normalizedScope(row && row.accountScopeId);
    if (!day || !visibleDays.has(day) || !accountScopeId) continue;
    const hourlyTicks = Array.isArray(row && row.hourlyTicks) ? row.hourlyTicks : [];
    for (let hour = 0; hour < 24; hour += 1) {
      const ticks = normalizedTick(hourlyTicks[hour]);
      if (ticks === 0) continue;
      const cell = cellFor(day, hour, accountScopeId);
      cell.ticks = Math.min(Number.MAX_SAFE_INTEGER, cell.ticks + ticks);
    }
  }

  for (const row of Array.isArray(payload.actionActivityDaily) ? payload.actionActivityDaily : []) {
    const day = normalizedDay(row && row.day);
    const scope = normalizedScope(row && row.accountScopeId);
    if (!visibleDays.has(day) || !scope) continue;
    for (let hour = 0; hour < 24; hour += 1) {
      const counts = emptyCounts();
      for (const { key } of ACTION_METRICS) {
        counts[`${key}Attempts`] = normalizedTick(row[`${key}Attempts`]?.[hour]);
        counts[`${key}Successes`] = Math.min(counts[`${key}Attempts`], normalizedTick(row[`${key}Successes`]?.[hour]));
      }
      if (counts.registrationAttempts || counts.controlCodeAttempts) addCounts(cellFor(day, hour, scope), counts);
    }
  }

  const entriesByHour = new Map();
  for (const cell of cells.values()) {
    let member = memberByScope.get(cell.accountScopeId);
    if (!member) {
      member = { accountScopeId: cell.accountScopeId, email: '', active: false };
      memberByScope.set(cell.accountScopeId, member);
    }
    const seconds = cell.ticks * secondsPerTick;
    const key = `${cell.day}|${cell.hour}`;
    if (!entriesByHour.has(key)) entriesByHour.set(key, []);
    entriesByHour.get(key).push({ ...member, ...cell, seconds, duration: seconds ? formatActivityDuration(seconds) : '', metrics: metricsFor(cell) });
  }

  let totalSeconds = 0;
  const totals = emptyCounts();
  const activeScopes = new Set();
  const days = dayKeys.map((day, dayIndex) => {
    let dayTotalSeconds = 0;
    const dayTotals = emptyCounts();
    const hours = Array.from({ length: 24 }, (_, hour) => {
      const entries = entriesByHour.get(`${day}|${hour}`) || [];
      let hourSeconds = 0;
      for (const entry of entries) {
        hourSeconds += entry.seconds;
        dayTotalSeconds += entry.seconds;
        totalSeconds += entry.seconds;
        activeScopes.add(entry.accountScopeId);
        addCounts(dayTotals, entry);
        addCounts(totals, entry);
      }
      entries.sort((left, right) => left.email.localeCompare(right.email) || left.accountScopeId.localeCompare(right.accountScopeId));
      return { hour, label: String(hour).padStart(2, '0'), entries, totalSeconds: hourSeconds };
    });
    const activeHours = hours.filter((hour) => hour.entries.length > 0);
    const maxHourSeconds = Math.max(0, ...hours.map((hour) => hour.totalSeconds));
    const relativeLabel = dayIndex === 0 ? 'Today' : dayIndex === 1 ? 'Yesterday' : '';
    return {
      day,
      displayLabel: relativeLabel ? `${relativeLabel} · ${day}` : day,
      buttonId: `adminStatisticsDayButton${day.replace(/-/g, '')}`,
      panelId: `adminStatisticsDayPanel${day.replace(/-/g, '')}`,
      hours,
      activeHours,
      maxHourSeconds,
      ...dayTotals,
      metrics: metricsFor(dayTotals),
      totalSeconds: dayTotalSeconds,
      totalDuration: dayTotalSeconds ? formatActivityDuration(dayTotalSeconds) : ''
    };
  });
  const activeDays = days.filter((day) => day.activeHours.length > 0);

  return {
    timeZone,
    dayCount,
    secondsPerTick,
    days,
    activeDays,
    hasActiveActivity: activeDays.length > 0,
    ...totals,
    metrics: metricsFor(totals),
    trackingStartLabel: trackingStartLabel(payload.actionStatisticsStartedAt, timeZone),
    activeUserCount: activeScopes.size,
    totalSeconds,
    totalDuration: formatActivityDuration(totalSeconds)
  };
}

function renderMetrics(metrics, fullLabels = false) {
  return metrics.length === 0 ? '' : html`
    <span class="admin-statistics-metrics">
      ${() => metrics.map((metric) => html`
        <span class="admin-statistics-metric" role="img" aria-label="${() => metric.description}">
          <span aria-hidden="true">${() => fullLabels ? metric.label : metric.shortLabel} <span class="admin-statistics-ratio">${() => metric.text}</span></span>
        </span>
      `.key(metric.key))}
    </span>
  `;
}

function renderEntry(entry) {
  return html`
    <span class="${() => entry.active ? 'admin-statistics-entry' : 'admin-statistics-entry is-inactive'}" title="${() => entry.email ? `${entry.email}${entry.active ? '' : ' · Inactive'}` : 'Unknown account'}">
      <span class="admin-statistics-entry-main">
        <span class="admin-statistics-entry-email">${() => entry.email || 'Unknown account'}</span>
        ${() => entry.duration ? html`<span class="admin-statistics-entry-duration">${() => entry.duration}</span>` : ''}
      </span>
      ${() => renderMetrics(entry.metrics)}
    </span>
  `.key(entry.accountScopeId);
}

export function mountActivityStatistics(documentRef = document) {
  const mount = documentRef.getElementById('ticketActivityStatistics');
  const dataNode = documentRef.getElementById('ticketActivityStatisticsData');
  if (!mount || !dataNode) return false;

  if (typeof mount.ticketActivityStatisticsCleanup === 'function') {
    mount.ticketActivityStatisticsCleanup();
  }

  let payload;
  try {
    payload = JSON.parse(String(dataNode.textContent || '{}'));
  } catch (_) {
    payload = {};
  }
  const statistics = reactive({ model: buildActivityStatisticsModel(payload) });
  const viewRef = documentRef.defaultView || (typeof window !== 'undefined' ? window : null);
  const viewState = reactive({
    mode: 'compact',
    updatedAt: payload.serverTime || '',
    refreshStatus: '',
    expandedDay: statistics.model.activeDays.length > 0 ? statistics.model.activeDays[0].day : ''
  });
  mount.textContent = '';

  html`
    <div class="admin-statistics-view" data-view-mode="${() => viewState.mode}">
      <div class="admin-statistics-summary">
        <span><strong>${() => statistics.model.activeDays.length}</strong> active ${() => statistics.model.activeDays.length === 1 ? 'day' : 'days'} in the last ${() => statistics.model.dayCount}</span>
        <span>·</span>
        <span><strong>${() => statistics.model.activeUserCount}</strong> user(s) with activity</span>
        <span>·</span>
        <span><strong>${() => statistics.model.totalDuration}</strong> measured use</span>
      </div>
      ${() => statistics.model.hasActiveActivity ? '' : html`
        <p class="admin-statistics-empty">No activity was recorded in this 30-day window.</p>
      `}
      <p class="admin-statistics-refresh" role="status">${() => viewState.refreshStatus || `Updated ${viewState.updatedAt ? new Date(viewState.updatedAt).toLocaleTimeString('en-GB', { timeZone: statistics.model.timeZone }) : 'just now'} (${statistics.model.timeZone}). Updates every 15 seconds while visible.`}</p>
      <div class="admin-statistics-view-controls">
        <p class="admin-statistics-view-note">
          ${() => viewState.mode === 'compact'
            ? 'Each day is scaled to its busiest hour; details show exact times.'
            : 'All 30 days and 24 hours are shown.'}
        </p>
        <button
          class="admin-statistics-view-toggle"
          id="adminStatisticsViewToggle"
          type="button"
          aria-controls="adminStatisticsCompactView adminStatisticsDetailedView"
        >
          ${() => viewState.mode === 'compact' ? 'Detailed table' : 'Compact list'}
        </button>
      </div>
      <section
        class="admin-statistics-compact"
        id="adminStatisticsCompactView"
        aria-label="Activity grouped by active date and hour"
        hidden="${() => viewState.mode !== 'compact'}"
      >
        <div class="admin-statistics-day-list">
          ${() => statistics.model.activeDays.map((day) => html`
            <article class="admin-statistics-day-card">
              <h3 class="admin-statistics-day-heading">
                <button
                  class="admin-statistics-day-toggle"
                  id="${() => day.buttonId}"
                  type="button"
                  data-statistics-day-toggle
                  data-statistics-day="${() => day.day}"
                  aria-controls="${() => day.panelId}"
                  aria-expanded="${() => viewState.expandedDay === day.day ? 'true' : 'false'}"
                >
                  <span class="admin-statistics-day-label">${() => day.displayLabel}</span>
                  <span class="admin-statistics-day-meta">
                    <span class="admin-statistics-day-totals">
                      ${() => day.totalDuration ? html`<span class="admin-statistics-day-duration">${() => day.totalDuration}</span>` : ''}
                      ${() => renderMetrics(day.metrics)}
                    </span>
                    <span class="admin-statistics-day-chevron" aria-hidden="true"></span>
                  </span>
                </button>
              </h3>
              <div class="admin-statistics-chart" role="group" aria-label="Measured viewing time by hour">
                <ol class="admin-statistics-bars">
                  ${() => day.hours.map((hour) => html`
                    <li>
                      <span class="${() => hour.totalSeconds ? `admin-statistics-bar is-active level-${Math.max(1, Math.ceil(10 * hour.totalSeconds / day.maxHourSeconds))}` : 'admin-statistics-bar'}" aria-hidden="true"></span>
                      <span class="admin-statistics-bar-label">${() => `${hour.label}:00–${hour.label}:59: ${formatActivityDuration(hour.totalSeconds)} measured viewing time`}</span>
                    </li>
                  `.key(hour.hour))}
                </ol>
                <div class="admin-statistics-chart-hours" aria-hidden="true"><span>00</span><span>06</span><span>12</span><span>18</span><span>23</span></div>
                ${() => day.totalSeconds ? '' : html`<p class="admin-statistics-chart-empty">Actions recorded; no measured viewing time.</p>`}
              </div>
              <div
                class="admin-statistics-day-panel"
                id="${() => day.panelId}"
                role="region"
                aria-labelledby="${() => day.buttonId}"
                hidden="${() => viewState.expandedDay !== day.day}"
              >
                <ul class="admin-statistics-active-hour-list">
                  ${() => day.activeHours.map((hour) => html`
                    <li class="admin-statistics-active-hour" data-statistics-active-hour="${() => hour.hour}">
                      <span class="admin-statistics-active-hour-label">${() => hour.label}:00–${() => hour.label}:59</span>
                      <div class="admin-statistics-entry-list admin-statistics-active-hour-entries">
                        ${() => hour.entries.map(renderEntry)}
                      </div>
                    </li>
                  `.key(hour.hour))}
                </ul>
              </div>
            </article>
          `.key(day.day))}
        </div>
        ${() => statistics.model.activeDays.length === 1 ? html`
          <p class="admin-statistics-no-other">No other activity in the last 30 days.</p>
        ` : ''}
      </section>
      <div
        class="admin-statistics-table-wrap"
        id="adminStatisticsDetailedView"
        tabindex="0"
        aria-label="Daily user activity by hour"
        hidden="${() => viewState.mode !== 'table'}"
      >
        <table class="admin-statistics-table">
          <caption>Page use and accepted actions, grouped by Europe/Riga calendar day and hour. Action results use the original request hour.</caption>
          <thead>
            <tr>
              <th class="admin-statistics-day" scope="col">Date</th>
              ${() => statistics.model.days[0].hours.map((hour) => html`<th scope="col">${() => hour.label}:00</th>`)}
            </tr>
          </thead>
          <tbody>
            ${() => statistics.model.days.map((day) => html`
              <tr>
                <th class="admin-statistics-day" scope="row"><span>${() => day.day}</span>${() => renderMetrics(day.metrics)}</th>
                ${() => day.hours.map((hour) => html`
                  <td class="admin-statistics-hour-cell">
                    <div class="admin-statistics-entry-list">
                      ${() => hour.entries.map(renderEntry)}
                    </div>
                  </td>
                `.key(hour.hour))}
              </tr>
            `.key(day.day))}
          </tbody>
        </table>
      </div>
      <details class="admin-statistics-count-details"><summary>How activity is counted</summary>
        <div class="admin-statistics-action-summary">${() => renderMetrics(statistics.model.metrics, true)}</div>
        <p class="admin-statistics-count-key">Counts = successful / accepted requests. Reg = registration; Code = code generation.</p>
        <p class="admin-statistics-tracking-note">${() => statistics.model.trackingStartLabel} Earlier totals counted slider registrations only. Requests without success may be pending, failed, or unconfirmed. Viewing time is measured in five-second intervals.</p>
      </details>
    </div>
  `(mount);

  const viewToggle = documentRef.getElementById('adminStatisticsViewToggle');
  const compactView = documentRef.getElementById('adminStatisticsCompactView');
  const toggleView = () => {
    viewState.mode = viewState.mode === 'compact' ? 'table' : 'compact';
  };
  const toggleDay = (event) => {
    const target = event && event.target;
    const button = target && typeof target.closest === 'function'
      ? target.closest('[data-statistics-day-toggle]')
      : null;
    if (!button || !compactView || !compactView.contains(button)) return;
    const day = normalizedDay(button.getAttribute('data-statistics-day'));
    if (!day) return;
    viewState.expandedDay = viewState.expandedDay === day ? '' : day;
  };
  if (viewToggle) viewToggle.addEventListener('click', toggleView);
  if (compactView) compactView.addEventListener('click', toggleDay);

  let request = null;
  let disposed = false;
  let accessRevoked = false;
  const refresh = async () => {
    if (disposed || accessRevoked || documentRef.hidden || request) return;
    const controller = new AbortController();
    request = controller;
    const timeout = viewRef.setTimeout(() => controller.abort(), 10000);
    try {
      const response = await viewRef.fetch('/api/v1/admin/statistics', {
        signal: controller.signal, cache: 'no-store', credentials: 'same-origin', redirect: 'error'
      });
      if (response.status === 401 || response.status === 403) {
        accessRevoked = true;
        viewRef.clearInterval(refreshTimer);
        throw new Error('access revoked');
      }
      if (!response.ok) throw new Error('statistics unavailable');
      const next = await response.json();
      if (disposed || controller.signal.aborted) return;
      if (!next.serverTime || !Array.isArray(next.members)) throw new Error('invalid statistics');
      statistics.model = buildActivityStatisticsModel(next);
      viewState.updatedAt = next.serverTime;
      viewState.refreshStatus = '';
    } catch (_) {
      if (!disposed && !documentRef.hidden && request === controller) {
        viewState.refreshStatus = accessRevoked ? 'Access expired. Sign in again to update statistics.'
          : `Updates unavailable. Showing figures from ${viewState.updatedAt || 'page opening'}. Retrying automatically.`;
      }
    } finally {
      viewRef.clearTimeout(timeout);
      if (request === controller) request = null;
    }
  };
  let refreshTimer = viewRef.setInterval(refresh, 15000);
  const visibilityChanged = () => {
    if (documentRef.hidden) { request?.abort(); request = null; }
    else void refresh();
  };
  const pageHide = () => {
    disposed = true;
    request?.abort();
    request = null;
    viewRef.clearInterval(refreshTimer);
  };
  const pageShow = event => {
    if (!event.persisted) return;
    disposed = false;
    if (!accessRevoked) refreshTimer = viewRef.setInterval(refresh, 15000);
    void refresh();
  };
  documentRef.addEventListener('visibilitychange', visibilityChanged);
  viewRef.addEventListener('pagehide', pageHide);
  viewRef.addEventListener('pageshow', pageShow);
  const cleanup = () => {
    pageHide();
    documentRef.removeEventListener('visibilitychange', visibilityChanged);
    viewRef.removeEventListener('pagehide', pageHide);
    viewRef.removeEventListener('pageshow', pageShow);
    if (viewToggle) viewToggle.removeEventListener('click', toggleView);
    if (compactView) compactView.removeEventListener('click', toggleDay);
    mount.ticketActivityStatisticsCleanup = null;
  };
  mount.ticketActivityStatisticsCleanup = cleanup;

  documentRef.documentElement.dataset.ticketAdminStatisticsUi = 'arrow';
  return true;
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => mountActivityStatistics(document), { once: true });
  } else {
    mountActivityStatistics(document);
  }
}
