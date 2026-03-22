#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./common.sh
source "$SCRIPT_DIR/common.sh"
# shellcheck source=./agent_browser.sh
source "$SCRIPT_DIR/agent_browser.sh"

ensure_output_dirs
ensure_local_env

for cmd in python3; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    log "Missing required command: $cmd"
    exit 1
  fi
done
agent_browser_require_cli

set -a
# shellcheck source=/dev/null
. "$REPO_ROOT/.env"
set +a

public_base_url="${TRAIN_WEB_PUBLIC_BASE_URL:-https://train-bot.jolkins.id.lv}"
chat_url="${AGENT_BROWSER_CHAT_URL:-https://web.telegram.org/a/#8792187636}"
profile_dir="${AGENT_BROWSER_PROFILE_DIR:-$HOME/.cache/agent-browser/telegram-web}"
out_dir="${AGENT_BROWSER_SMOKE_OUT_DIR:-$REPO_ROOT/output/agent-browser/pixel-miniapp-smoke}"
chat_session="${AGENT_BROWSER_MINIAPP_CHAT_SESSION:-ttb-miniapp-smoke-chat}"
app_session="${AGENT_BROWSER_MINIAPP_APP_SESSION:-ttb-miniapp-smoke-app}"
smoke_user_id="${AGENT_BROWSER_SMOKE_USER_ID:-900000001}"
smoke_lang="${AGENT_BROWSER_SMOKE_LANG:-lv}"
app_url="${public_base_url%/}/app"

mkdir -p "$out_dir"
rm -f \
  "$out_dir/chat-console.log" \
  "$out_dir/chat-network.log" \
  "$out_dir/app-console.log" \
  "$out_dir/app-network.log" \
  "$out_dir/chat-bootstrap.png" \
  "$out_dir/app-dashboard.png" \
  "$out_dir/app-map.png"

agent_browser_prepare_profile_dir "$profile_dir"

if pgrep -f "Google Chrome.*${profile_dir}" >/dev/null 2>&1; then
  pkill -f "Google Chrome.*${profile_dir}" || true
  sleep 1
fi

cleanup() {
  agent_browser_run "$chat_session" close >/dev/null 2>&1 || true
  agent_browser_run "$app_session" close >/dev/null 2>&1 || true
}
trap cleanup EXIT

fail() {
  log "$1"
  exit 1
}

signed_init_data="$(
  python3 - "$BOT_TOKEN" "$smoke_user_id" "$smoke_lang" <<'PY'
import hashlib
import hmac
import json
import sys
import time
import urllib.parse

bot_token = sys.argv[1]
user_id = int(sys.argv[2])
language_code = sys.argv[3]

values = {
    "auth_date": str(int(time.time())),
    "query_id": "pixel-smoke",
    "user": json.dumps(
        {
            "id": user_id,
            "first_name": "Pixel Smoke",
            "language_code": language_code,
        },
        separators=(",", ":"),
    ),
}
data_check_string = "\n".join(f"{key}={values[key]}" for key in sorted(values))
secret = hmac.new(b"WebAppData", bot_token.encode("utf-8"), hashlib.sha256).digest()
values["hash"] = hmac.new(secret, data_check_string.encode("utf-8"), hashlib.sha256).hexdigest()
print(urllib.parse.urlencode(values))
PY
)"
smoke_app_url="$(
  python3 - "$app_url" "$signed_init_data" <<'PY'
import json
import sys
import urllib.parse

app_url = sys.argv[1]
init_data = sys.argv[2]
theme = {
    "bg_color": "#ffffff",
    "text_color": "#000000",
    "hint_color": "#707579",
    "link_color": "#3390ec",
    "button_color": "#3390ec",
    "button_text_color": "#ffffff",
    "secondary_bg_color": "#f4f4f5",
    "header_bg_color": "#ffffff",
    "accent_text_color": "#3390ec",
    "section_bg_color": "#ffffff",
    "section_header_text_color": "#707579",
    "subtitle_text_color": "#707579",
    "destructive_text_color": "#e53935",
}
theme_json = json.dumps(theme, separators=(",", ":"))
print(
    f"{app_url}#tgWebAppData={urllib.parse.quote(init_data, safe='')}"
    f"&tgWebAppVersion=9.1"
    f"&tgWebAppPlatform=weba"
    f"&tgWebAppThemeParams={urllib.parse.quote(theme_json, safe='')}"
)
PY
)"

js_chat_bootstrap="$(cat <<'JS'
(async () => {
  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  const text = (node) => String((node && (node.textContent || node.innerText)) || '').trim();
  const visible = (node) => Boolean(
    node
    && node.isConnected
    && node.getClientRects
    && node.getClientRects().length > 0
    && window.getComputedStyle(node).visibility !== 'hidden'
    && window.getComputedStyle(node).display !== 'none'
  );
  const clickNode = (node) => {
    if (!visible(node)) {
      return false;
    }
    node.dispatchEvent(new MouseEvent('mouseover', { bubbles: true, cancelable: true }));
    node.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }));
    node.dispatchEvent(new MouseEvent('mouseup', { bubbles: true, cancelable: true }));
    node.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
    return true;
  };
  const firstVisible = (selector, matcher) => Array.from(document.querySelectorAll(selector)).find((node) => visible(node) && matcher.test(text(node)));
  const botId = '8792187636';
  const botHandle = '@vivi_kontrole_bot';

  const openBotChat = async () => {
    if ((window.location.hash || '').includes(`#${botId}`)) {
      return true;
    }
    const directHref = document.querySelector(`a[href="#${botId}"]`);
    if (clickNode(directHref)) {
      await sleep(700);
      return (window.location.hash || '').includes(`#${botId}`);
    }
    const byName = firstVisible('a, button, div[role="button"], span', /Vivi kontrole bot|Report Bot/i);
    if (clickNode(byName)) {
      await sleep(700);
      return (window.location.hash || '').includes(`#${botId}`);
    }
    const searchInput = document.querySelector('input[type="text"], input[placeholder*="Search"], [contenteditable="true"][data-placeholder*="Search"]');
    if (visible(searchInput)) {
      searchInput.focus();
      if ('value' in searchInput) {
        searchInput.value = botHandle;
        searchInput.dispatchEvent(new Event('input', { bubbles: true }));
      } else {
        searchInput.textContent = botHandle;
        searchInput.dispatchEvent(new InputEvent('input', { bubbles: true, data: botHandle, inputType: 'insertText' }));
      }
      await sleep(400);
      searchInput.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
      searchInput.dispatchEvent(new KeyboardEvent('keyup', { key: 'Enter', bubbles: true }));
      await sleep(900);
      if ((window.location.hash || '').includes(`#${botId}`)) {
        return true;
      }
    }
    window.location.hash = `#${botId}`;
    await sleep(1000);
    return (window.location.hash || '').includes(`#${botId}`);
  };

  const clickMatching = async (matcher) => {
    const node = firstVisible('button, div[role="button"], a', matcher);
    if (!clickNode(node)) {
      return false;
    }
    await sleep(600);
    return true;
  };

  let chatReady = false;
  let launcherCount = 0;
  let launcherClicked = false;
  for (let attempt = 0; attempt < 5; attempt++) {
    chatReady = await openBotChat();
    const launcherButtons = Array.from(document.querySelectorAll('button, a, div[role="button"]')).filter((node) => visible(node) && /Atvērt lietotni|Open app|Mini App/i.test(text(node)));
    launcherCount = launcherButtons.length;
    if (launcherButtons.length) {
      clickNode(launcherButtons[launcherButtons.length - 1]);
      launcherClicked = true;
      await sleep(1200);
      break;
    }
    await clickMatching(/Show bot keyboard|Hide bot keyboard|Parādīt bota tastatūru|Paslēpt bota tastatūru/i);
    await clickMatching(/^(Start|START|Sākt)$/i);
    await clickMatching(/^\/start$/i);
    await clickMatching(/Agree|Piekrītu/i);
    await sleep(800);
  }

  const iframeCount = document.querySelectorAll('iframe').length;
  return `chatReady=${chatReady ? 1 : 0};launcherVisible=${launcherCount > 0 ? 1 : 0};launcherCount=${launcherCount};launcherClicked=${launcherClicked ? 1 : 0};iframeCount=${iframeCount};url=${encodeURIComponent(window.location.href)}`;
})()
JS
)"

js_app_shell_ready="$(cat <<'JS'
(() => {
  const visible = (node) => Boolean(
    node
    && node.isConnected
    && node.getClientRects
    && node.getClientRects().length > 0
    && window.getComputedStyle(node).visibility !== 'hidden'
    && window.getComputedStyle(node).display !== 'none'
  );
  const buttons = Array.from(document.querySelectorAll('button')).filter(visible);
  const dashboardTab = buttons.find((node) => /^(Dashboard|Panelis)$/i.test((node.textContent || '').trim()));
  const mapTab = buttons.find((node) => /^(Map|Karte)$/i.test((node.textContent || '').trim()));
  const settingsTab = buttons.find((node) => /^(Settings|Iestatījumi)$/i.test((node.textContent || '').trim()));
  const initDataLen = ((window.Telegram && window.Telegram.WebApp && window.Telegram.WebApp.initData) || '').length;
  return `tabsReady=${dashboardTab && mapTab && settingsTab ? 1 : 0};initDataLen=${initDataLen}`;
})()
JS
)"

js_verify_direct_app="$(cat <<'JS'
(async () => {
  const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  const text = (node) => String((node && (node.textContent || node.innerText)) || '').trim();
  const visible = (node) => Boolean(
    node
    && node.isConnected
    && node.getClientRects
    && node.getClientRects().length > 0
    && window.getComputedStyle(node).visibility !== 'hidden'
    && window.getComputedStyle(node).display !== 'none'
  );
  const clickNode = (node) => {
    if (!visible(node)) {
      return false;
    }
    node.dispatchEvent(new MouseEvent('mouseover', { bubbles: true, cancelable: true }));
    node.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }));
    node.dispatchEvent(new MouseEvent('mouseup', { bubbles: true, cancelable: true }));
    node.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
    return true;
  };
  const waitFor = async (fn, loops = 24, delay = 400) => {
    for (let i = 0; i < loops; i++) {
      const value = await fn();
      if (value) {
        return value;
      }
      await sleep(delay);
    }
    return null;
  };
  const basePath = (window.TRAIN_APP_CONFIG && window.TRAIN_APP_CONFIG.basePath) || '';
  const state = {
    tabsReady: false,
    departuresLoaded: false,
    selectorVisible: false,
    selectorOptionCount: 0,
    registerVisible: false,
    mapActionVisible: false,
    sightingsShortcutVisible: false,
    registerMetricsVisible: false,
    registerMetricsMatch: false,
    checkinSucceeded: false,
    rideTrainMatched: false,
    rideStationMatched: false,
    selectedTrainId: '',
    selectedStationId: '',
    mapLoaded: false,
  };

  const tabs = await waitFor(() => {
    const buttons = Array.from(document.querySelectorAll('button')).filter(visible);
    const dashboardTab = buttons.find((node) => /^(Dashboard|Panelis)$/i.test(text(node)));
    const mapTab = buttons.find((node) => /^(Map|Karte)$/i.test(text(node)));
    const settingsTab = buttons.find((node) => /^(Settings|Iestatījumi)$/i.test(text(node)));
    return dashboardTab && mapTab && settingsTab ? { dashboardTab, mapTab, settingsTab } : null;
  }, 30, 500);
  if (!tabs) {
    return 'tabsReady=0';
  }
  state.tabsReady = true;
  clickNode(tabs.dashboardTab);
  await sleep(700);

  const stationQueryInput = await waitFor(() => document.querySelector('#station-query'), 20, 300);
  const stationSearchButton = document.querySelector('#station-search');
  if (!stationQueryInput || !visible(stationSearchButton)) {
    return 'tabsReady=1;departuresLoaded=0';
  }

  const discoverQueries = async () => {
    const fallback = ['Rig', 'Jel', 'Aiz', 'Ata', 'Zil'];
    const discovered = [];
    for (const windowName of ['now', 'next_hour', 'today']) {
      try {
        const response = await fetch(`${basePath}/api/v1/windows/${windowName}`, { credentials: 'include' });
        if (!response.ok) {
          continue;
        }
        const payload = await response.json();
        const trains = Array.isArray(payload && payload.trains) ? payload.trains : [];
        for (const item of trains) {
          const train = item && item.train ? item.train : null;
          const fromStation = String((train && train.fromStation) || '').trim();
          if (fromStation) {
            discovered.push(fromStation.slice(0, 4));
          }
          if (discovered.length >= 5) {
            return [...new Set([...discovered, ...fallback])];
          }
        }
      } catch (_) {
        // Ignore discovery failures and use fallbacks.
      }
    }
    return [...new Set([...discovered, ...fallback])];
  };

  const queries = await discoverQueries();
  for (const queryText of queries) {
    stationQueryInput.focus();
    stationQueryInput.value = queryText;
    stationQueryInput.dispatchEvent(new Event('input', { bubbles: true }));
    clickNode(stationSearchButton);
    await sleep(900);

    const stationMatches = Array.from(document.querySelectorAll("[data-action='station-departures']")).filter(visible);
    if (!stationMatches.length) {
      continue;
    }

    clickNode(stationMatches[0]);
    const selectorStack = await waitFor(() => {
      const selectorButton = document.querySelector("[data-action='toggle-checkin-dropdown']");
      const registerButton = document.querySelector("[data-action='selected-checkin']");
      const mapButton = document.querySelector("[data-action='selected-checkin-map']");
      return visible(selectorButton) && visible(registerButton) && visible(mapButton)
        ? { selectorButton, registerButton, mapButton }
        : null;
    }, 24, 400);
    if (!selectorStack) {
      continue;
    }

    state.departuresLoaded = true;
    state.selectorVisible = true;
    state.registerVisible = true;
    state.mapActionVisible = true;
    state.sightingsShortcutVisible = visible(document.querySelector("[data-action='tab-sightings']"));

    const metrics = Array.from(selectorStack.registerButton.querySelectorAll('.checkin-register-metric')).map(text).filter(Boolean);
    state.registerMetricsVisible = metrics.length === 2;
    state.registerMetricsMatch = state.registerMetricsVisible && metrics[0] === metrics[1];
    state.selectedTrainId = selectorStack.registerButton.getAttribute('data-train-id') || '';
    state.selectedStationId = selectorStack.registerButton.getAttribute('data-station-id') || '';

    clickNode(selectorStack.selectorButton);
    await sleep(300);
    const selectorOptions = Array.from(document.querySelectorAll("[data-action='choose-checkin-train']")).filter(visible);
    state.selectorOptionCount = selectorOptions.length;
    if (selectorOptions.length > 1) {
      const alternate = selectorOptions.find((node) => (node.getAttribute('data-train-id') || '') !== state.selectedTrainId) || selectorOptions[0];
      clickNode(alternate);
      await sleep(400);
      const refreshedRegister = document.querySelector("[data-action='selected-checkin']");
      if (visible(refreshedRegister)) {
        state.selectedTrainId = refreshedRegister.getAttribute('data-train-id') || state.selectedTrainId;
        state.selectedStationId = refreshedRegister.getAttribute('data-station-id') || state.selectedStationId;
      }
    }

    clickNode(document.querySelector("[data-action='selected-checkin-map']"));
    state.mapLoaded = Boolean(await waitFor(() => document.querySelector('.train-map') && document.querySelectorAll('.stop-row').length > 0, 24, 500));
    break;
  }

  return `tabsReady=${state.tabsReady ? 1 : 0};departuresLoaded=${state.departuresLoaded ? 1 : 0};selectorVisible=${state.selectorVisible ? 1 : 0};selectorOptionCount=${state.selectorOptionCount};registerVisible=${state.registerVisible ? 1 : 0};mapActionVisible=${state.mapActionVisible ? 1 : 0};sightingsShortcutVisible=${state.sightingsShortcutVisible ? 1 : 0};registerMetricsVisible=${state.registerMetricsVisible ? 1 : 0};registerMetricsMatch=${state.registerMetricsMatch ? 1 : 0};selectedTrainId=${encodeURIComponent(state.selectedTrainId)};selectedStationId=${encodeURIComponent(state.selectedStationId)};mapLoaded=${state.mapLoaded ? 1 : 0}`;
})()
JS
)"

agent_browser_run_with_profile "$chat_session" "$profile_dir" open "$chat_url"
agent_browser_run "$chat_session" set viewport 1280 1400 >/dev/null 2>&1 || true
agent_browser_run "$chat_session" console --clear >/dev/null 2>&1 || true
agent_browser_run "$chat_session" network requests --clear >/dev/null 2>&1 || true

if ! agent_browser_wait_for_eval_match "$chat_session" "$js_chat_bootstrap" 'chatReady=1.*launcherVisible=1.*launcherClicked=1' 5 1; then
  fail "Mini app smoke failed: Telegram chat bootstrap did not expose the launcher button"
fi
agent_browser_run "$chat_session" screenshot "$out_dir/chat-bootstrap.png" >/dev/null
agent_browser_write_output "$chat_session" "$out_dir/chat-console.log" console || true
agent_browser_write_output "$chat_session" "$out_dir/chat-network.log" network requests || true
agent_browser_run "$chat_session" close >/dev/null 2>&1 || true

agent_browser_run "$app_session" close >/dev/null 2>&1 || true
agent_browser_run "$app_session" open "$smoke_app_url"
agent_browser_run "$app_session" set viewport 1400 1100 >/dev/null 2>&1 || true
agent_browser_run "$app_session" console --clear >/dev/null 2>&1 || true
agent_browser_run "$app_session" network requests --clear >/dev/null 2>&1 || true

if ! agent_browser_wait_for_eval_match "$app_session" "$js_app_shell_ready" 'tabsReady=1.*initDataLen=[1-9][0-9]*' 20 1; then
  fail "Mini app smoke failed: Telegram-style app shell did not boot"
fi

agent_browser_run "$app_session" screenshot "$out_dir/app-dashboard.png" >/dev/null

if ! agent_browser_wait_for_eval_match "$app_session" "$js_verify_direct_app" 'tabsReady=1.*departuresLoaded=1.*selectorVisible=1.*selectorOptionCount=[1-9][0-9]*.*registerVisible=1.*mapActionVisible=1.*registerMetricsVisible=1.*registerMetricsMatch=1.*mapLoaded=1' 4 1; then
  fail "Mini app smoke failed: authenticated app flow did not complete the expected dashboard and map assertions"
fi

agent_browser_run "$app_session" screenshot "$out_dir/app-map.png" >/dev/null
agent_browser_write_output "$app_session" "$out_dir/app-console.log" console || true
agent_browser_write_output "$app_session" "$out_dir/app-network.log" network requests || true

log "Mini app smoke completed; artifacts in $out_dir"
