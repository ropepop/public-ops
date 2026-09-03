import { html, reactive } from '@arrow-js/core';

const DEFAULT_TIME_ZONE = 'Europe/Riga';
const DEFAULT_DAY_COUNT = 30;
const DEFAULT_SECONDS_PER_TICK = 5;
const MILLIS_PER_DAY = 24 * 60 * 60 * 1000;
const COMPACT_VIEW_QUERY = '(max-width: 780px)';

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

function shortActivityID(member, accountScopeID) {
  const publicID = String(member && member.publicId || '').trim().toUpperCase();
  if (publicID) return publicID.slice(0, 12);
  const fallback = String(accountScopeID || '').trim().slice(0, 4).toUpperCase();
  return fallback || 'USER';
}

function normalizedTick(value) {
  const tick = Math.floor(Number(value));
  if (!Number.isFinite(tick) || tick <= 0) return 0;
  return Math.min(tick, Number.MAX_SAFE_INTEGER);
}

export function buildActivityStatisticsModel(payload = {}) {
  const timeZone = String(payload.timeZone || DEFAULT_TIME_ZONE).trim() || DEFAULT_TIME_ZONE;
  const dayCount = integerInRange(payload.days, DEFAULT_DAY_COUNT, 1, 90);
  const secondsPerTick = integerInRange(payload.secondsPerTick, DEFAULT_SECONDS_PER_TICK, 1, 3600);
  const dayKeys = activityDayKeys(payload.serverTime || Date.now(), timeZone, dayCount);
  const visibleDays = new Set(dayKeys);
  const members = [];
  const memberByScope = new Map();

  for (const rawMember of Array.isArray(payload.members) ? payload.members : []) {
    const accountScopeId = normalizedScope(rawMember && rawMember.accountScopeId);
    if (!accountScopeId) continue;
    const member = {
      accountScopeId,
      shortId: shortActivityID(rawMember, accountScopeId),
      email: String(rawMember && rawMember.email || '').trim(),
      active: rawMember && rawMember.active === true
    };
    const previous = memberByScope.get(accountScopeId);
    if (previous && previous.active && !member.active) continue;
    if (previous) members.splice(members.indexOf(previous), 1);
    memberByScope.set(accountScopeId, member);
    members.push(member);
  }

  const ticksByCell = new Map();
  const scopesWithVisibleActivity = new Set();
  for (const row of Array.isArray(payload.pageActivityDaily) ? payload.pageActivityDaily : []) {
    const day = normalizedDay(row && row.day);
    const accountScopeId = normalizedScope(row && row.accountScopeId);
    if (!day || !visibleDays.has(day) || !accountScopeId) continue;
    const hourlyTicks = Array.isArray(row && row.hourlyTicks) ? row.hourlyTicks : [];
    for (let hour = 0; hour < 24; hour += 1) {
      const ticks = normalizedTick(hourlyTicks[hour]);
      if (ticks === 0) continue;
      scopesWithVisibleActivity.add(accountScopeId);
      const key = `${day}|${hour}|${accountScopeId}`;
      ticksByCell.set(key, Math.min(Number.MAX_SAFE_INTEGER, (ticksByCell.get(key) || 0) + ticks));
    }
  }

  let totalSeconds = 0;
  const activeScopes = new Set();
  const days = dayKeys.map((day, dayIndex) => {
    let dayTotalSeconds = 0;
    const hours = Array.from({ length: 24 }, (_, hour) => {
      const entries = [];
      for (const member of members) {
        const ticks = ticksByCell.get(`${day}|${hour}|${member.accountScopeId}`) || 0;
        if (ticks === 0) continue;
        const seconds = ticks * secondsPerTick;
        dayTotalSeconds += seconds;
        totalSeconds += seconds;
        activeScopes.add(member.accountScopeId);
        entries.push({
          accountScopeId: member.accountScopeId,
          shortId: member.shortId,
          email: member.email,
          active: member.active,
          ticks,
          seconds,
          duration: formatActivityDuration(seconds)
        });
      }
      entries.sort((left, right) => left.shortId.localeCompare(right.shortId));
      return { hour, label: String(hour).padStart(2, '0'), entries };
    });
    const activeHours = hours.filter((hour) => hour.entries.length > 0);
    const relativeLabel = dayIndex === 0 ? 'Today' : dayIndex === 1 ? 'Yesterday' : '';
    return {
      day,
      displayLabel: relativeLabel ? `${relativeLabel} · ${day}` : day,
      buttonId: `adminStatisticsDayButton${day.replace(/-/g, '')}`,
      panelId: `adminStatisticsDayPanel${day.replace(/-/g, '')}`,
      hours,
      activeHours,
      totalSeconds: dayTotalSeconds,
      totalDuration: formatActivityDuration(dayTotalSeconds)
    };
  });
  const activeDays = days.filter((day) => day.activeHours.length > 0);

  const legend = members
    .filter((member) => scopesWithVisibleActivity.has(member.accountScopeId))
    .sort((left, right) => left.shortId.localeCompare(right.shortId) || left.email.localeCompare(right.email));

  return {
    timeZone,
    dayCount,
    secondsPerTick,
    days,
    activeDays,
    legend,
    hasActiveActivity: totalSeconds > 0,
    activeUserCount: activeScopes.size,
    totalSeconds,
    totalDuration: formatActivityDuration(totalSeconds)
  };
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
  const model = reactive(buildActivityStatisticsModel(payload));
  const viewRef = documentRef.defaultView || (typeof window !== 'undefined' ? window : null);
  let compactViewQuery = null;
  try {
    compactViewQuery = viewRef && typeof viewRef.matchMedia === 'function'
      ? viewRef.matchMedia(COMPACT_VIEW_QUERY)
      : null;
  } catch (_) {
    compactViewQuery = null;
  }
  const viewState = reactive({
    mode: compactViewQuery && compactViewQuery.matches ? 'compact' : 'table',
    manuallySelected: false,
    expandedDay: model.activeDays.length > 0 ? model.activeDays[0].day : ''
  });
  mount.textContent = '';

  html`
    <div class="admin-statistics-view" data-view-mode="${() => viewState.mode}">
      <div class="admin-statistics-summary">
        <span><strong>${() => model.dayCount}</strong> days</span>
        <span>·</span>
        <span><strong>${() => model.activeUserCount}</strong> user(s) with activity</span>
        <span>·</span>
        <span><strong>${() => model.totalDuration}</strong> measured use</span>
      </div>
      ${() => model.hasActiveActivity ? '' : html`
        <p class="admin-statistics-empty">No activity was recorded in this 30-day window.</p>
      `}
      <div class="admin-statistics-view-controls">
        <p class="admin-statistics-view-note">
          ${() => viewState.mode === 'compact'
            ? 'Only active days and hours are shown.'
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
          ${() => model.activeDays.map((day) => html`
            <article class="admin-statistics-day-card">
              <h3 class="admin-statistics-day-heading">
                <button
                  class="admin-statistics-day-toggle"
                  id="${day.buttonId}"
                  type="button"
                  data-statistics-day-toggle
                  data-statistics-day="${day.day}"
                  aria-controls="${day.panelId}"
                  aria-expanded="${() => viewState.expandedDay === day.day ? 'true' : 'false'}"
                >
                  <span class="admin-statistics-day-label">${day.displayLabel}</span>
                  <span class="admin-statistics-day-meta">
                    <span class="admin-statistics-day-duration">${day.totalDuration}</span>
                    <span class="admin-statistics-day-chevron" aria-hidden="true"></span>
                  </span>
                </button>
              </h3>
              <div
                class="admin-statistics-day-panel"
                id="${day.panelId}"
                role="region"
                aria-labelledby="${day.buttonId}"
                hidden="${() => viewState.expandedDay !== day.day}"
              >
                <ul class="admin-statistics-active-hour-list">
                  ${() => day.activeHours.map((hour) => html`
                    <li class="admin-statistics-active-hour" data-statistics-active-hour="${hour.hour}">
                      <span class="admin-statistics-active-hour-label">${hour.label}:00–${hour.label}:59</span>
                      <div class="admin-statistics-entry-list admin-statistics-active-hour-entries">
                        ${() => hour.entries.map((entry) => html`
                          <span class="${entry.active ? 'admin-statistics-entry' : 'admin-statistics-entry is-inactive'}" title="${entry.active ? entry.email : `${entry.email} · Inactive`}">
                            <span class="admin-statistics-entry-id">${entry.shortId}</span>
                            <span class="admin-statistics-entry-duration">${entry.duration}</span>
                          </span>
                        `.key(entry.accountScopeId))}
                      </div>
                    </li>
                  `.key(hour.hour))}
                </ul>
              </div>
            </article>
          `.key(day.day))}
        </div>
        ${() => model.activeDays.length === 1 ? html`
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
          <caption>Foreground activity in five-second intervals, grouped by Europe/Riga calendar day and hour.</caption>
          <thead>
            <tr>
              <th class="admin-statistics-day" scope="col">Date</th>
              ${() => model.days[0].hours.map((hour) => html`<th scope="col">${hour.label}:00</th>`.key(hour.hour))}
            </tr>
          </thead>
          <tbody>
            ${() => model.days.map((day) => html`
              <tr>
                <th class="admin-statistics-day" scope="row">${day.day}</th>
                ${() => day.hours.map((hour) => html`
                  <td class="admin-statistics-hour-cell">
                    <div class="admin-statistics-entry-list">
                      ${() => hour.entries.map((entry) => html`
                        <span class="${entry.active ? 'admin-statistics-entry' : 'admin-statistics-entry is-inactive'}" title="${entry.active ? entry.email : `${entry.email} · Inactive`}">
                          <span class="admin-statistics-entry-id">${entry.shortId}</span>
                          <span class="admin-statistics-entry-duration">${entry.duration}</span>
                        </span>
                      `.key(entry.accountScopeId))}
                    </div>
                  </td>
                `.key(hour.hour))}
              </tr>
            `.key(day.day))}
          </tbody>
        </table>
      </div>
      ${() => model.legend.length === 0 ? '' : html`
        <section class="admin-statistics-legend" aria-labelledby="adminStatisticsLegendTitle">
          <h3 id="adminStatisticsLegendTitle">User IDs</h3>
          <ul class="admin-statistics-legend-list">
            ${() => model.legend.map((member) => html`
              <li class="admin-statistics-legend-item">
                <span class="admin-statistics-legend-id">${member.shortId}</span>
                <span class="admin-statistics-legend-email">${member.email}</span>
                ${member.active ? '' : html`<span class="admin-statistics-inactive">Inactive</span>`}
              </li>
            `.key(member.accountScopeId))}
          </ul>
        </section>
      `}
    </div>
  `(mount);

  const viewToggle = documentRef.getElementById('adminStatisticsViewToggle');
  const compactView = documentRef.getElementById('adminStatisticsCompactView');
  const toggleView = () => {
    viewState.manuallySelected = true;
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
  const useResponsiveDefault = (event) => {
    if (viewState.manuallySelected) return;
    viewState.mode = event && event.matches ? 'compact' : 'table';
  };

  if (viewToggle) viewToggle.addEventListener('click', toggleView);
  if (compactView) compactView.addEventListener('click', toggleDay);
  if (compactViewQuery) {
    if (typeof compactViewQuery.addEventListener === 'function') {
      compactViewQuery.addEventListener('change', useResponsiveDefault);
    } else if (typeof compactViewQuery.addListener === 'function') {
      compactViewQuery.addListener(useResponsiveDefault);
    }
  }

  const cleanup = () => {
    if (viewToggle) viewToggle.removeEventListener('click', toggleView);
    if (compactView) compactView.removeEventListener('click', toggleDay);
    if (compactViewQuery) {
      if (typeof compactViewQuery.removeEventListener === 'function') {
        compactViewQuery.removeEventListener('change', useResponsiveDefault);
      } else if (typeof compactViewQuery.removeListener === 'function') {
        compactViewQuery.removeListener(useResponsiveDefault);
      }
    }
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
