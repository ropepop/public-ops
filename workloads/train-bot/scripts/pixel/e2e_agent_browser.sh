#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=./common.sh
source "$SCRIPT_DIR/common.sh"
# shellcheck source=./agent_browser.sh
source "$SCRIPT_DIR/agent_browser.sh"

log "Using agent-browser for Telegram Web smoke flow"

ensure_output_dirs
agent_browser_require_cli

out_dir="${AGENT_BROWSER_SMOKE_OUT_DIR:-$REPO_ROOT/output/agent-browser/pixel-bot-smoke}"
session_name="${AGENT_BROWSER_BOT_SESSION:-ttb}"
profile_dir="${AGENT_BROWSER_PROFILE_DIR:-$HOME/.cache/agent-browser/telegram-web}"
chat_url="${AGENT_BROWSER_CHAT_URL:-https://web.telegram.org/a/#8792187636}"
mobile_view="${AGENT_BROWSER_MOBILE_VIEW:-0}"

mkdir -p "$out_dir"
rm -f "$out_dir/smoke-console.log" "$out_dir/smoke-network.log" "$out_dir/e2e-evidence.txt" "$out_dir/bot-smoke.png"

agent_browser_prepare_profile_dir "$profile_dir"

if pgrep -f "Google Chrome.*${profile_dir}" >/dev/null 2>&1; then
  pkill -f "Google Chrome.*${profile_dir}" || true
  sleep 1
fi

cleanup() {
  agent_browser_run "$session_name" close >/dev/null 2>&1 || true
}
trap cleanup EXIT

fail() {
  log "$1"
  exit 1
}

js_bot_flow="$(cat <<'JS'
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

  const keyboardState = () => {
    const buttons = Array.from(document.querySelectorAll('button, div[role="button"], a'));
    const has = (matcher) => buttons.some((node) => visible(node) && matcher.test(text(node)));
    return {
      checkinVisible: has(/^🚆\s*(Check in|Piesakies)$/i),
      reportVisible: has(/^📣\s*(Report|Ziņot)/i),
      showKeyboardVisible: has(/Show bot keyboard|Parādīt bota tastatūru/i),
      hideKeyboardVisible: has(/Hide bot keyboard|Paslēpt bota tastatūru/i),
    };
  };

  const checkinEntryState = () => {
    const buttons = Array.from(document.querySelectorAll('button, div[role="button"], a'));
    const has = (matcher) => buttons.some((node) => visible(node) && matcher.test(text(node)));
    const textNodes = Array.from(document.querySelectorAll('div, span, p'));
    const hasText = (matcher) => textNodes.some((node) => visible(node) && matcher.test(text(node)));
    return {
      stationOptionVisible: has(/Type station name|Ieraksti stacijas nosaukumu/i),
      timeVisible: has(/Choose by time|Izvēlēties pēc laika/i),
      stationPromptVisible: hasText(/Send the first few letters of your boarding station|Nosūti savas iekāpšanas stacijas pirmos burtus/i),
    };
  };

  const clickMatching = async (matcher) => {
    const node = firstVisible('button, div[role="button"], a', matcher);
    if (!clickNode(node)) {
      return false;
    }
    await sleep(500);
    return true;
  };

  let chatReady = false;
  let state = keyboardState();
  let entry = checkinEntryState();
  let byTimeClicked = false;
  let timeWindowClicked = false;

  for (let attempt = 0; attempt < 6; attempt++) {
    chatReady = await openBotChat();
    state = keyboardState();
    entry = checkinEntryState();
    if (chatReady && state.reportVisible && (state.checkinVisible || (entry.timeVisible && (entry.stationOptionVisible || entry.stationPromptVisible)))) {
      break;
    }
    await clickMatching(/Show bot keyboard|Hide bot keyboard|Parādīt bota tastatūru|Paslēpt bota tastatūru/i);
    await clickMatching(/^(Start|START|Sākt)$/i);
    await clickMatching(/^\/start$/i);
    await clickMatching(/Agree|Piekrītu/i);
    await sleep(600);
  }

  if (!entry.timeVisible && state.checkinVisible) {
    await clickMatching(/^🚆\s*(Check in|Piesakies)$/i);
    await sleep(500);
    entry = checkinEntryState();
  }

  const entryReady = entry.timeVisible && (entry.stationOptionVisible || entry.stationPromptVisible);
  if (entry.timeVisible) {
    byTimeClicked = await clickMatching(/Choose by time|Izvēlēties pēc laika/i);
    await sleep(450);
    timeWindowClicked = await clickMatching(/^(Now|Next hour|Later today|Tagad|Nākamā stunda|Vēlāk šodien)$/i);
    await sleep(400);
  }

  state = keyboardState();
  return `chatReady=${chatReady ? 1 : 0};checkinVisible=${state.checkinVisible ? 1 : 0};reportVisible=${state.reportVisible ? 1 : 0};entryReady=${entryReady ? 1 : 0};stationOptionVisible=${entry.stationOptionVisible ? 1 : 0};timeVisible=${entry.timeVisible ? 1 : 0};stationPromptVisible=${entry.stationPromptVisible ? 1 : 0};byTimeClicked=${byTimeClicked ? 1 : 0};timeWindowClicked=${timeWindowClicked ? 1 : 0}`;
})()
JS
)"

agent_browser_run_with_profile "$session_name" "$profile_dir" open "$chat_url"
if [[ "$mobile_view" == "1" ]]; then
  agent_browser_run "$session_name" set viewport 390 844 >/dev/null 2>&1 || true
else
  agent_browser_run "$session_name" set viewport 1280 1000 >/dev/null 2>&1 || true
fi
agent_browser_run "$session_name" console --clear >/dev/null 2>&1 || true
agent_browser_run "$session_name" network requests --clear >/dev/null 2>&1 || true

if ! agent_browser_wait_for_eval_match "$session_name" "$js_bot_flow" 'chatReady=1.*reportVisible=1.*(checkinVisible=1|timeVisible=1)' 6 1; then
  fail "agent-browser smoke failed: Telegram bot controls or guided check-in entry were not visible"
fi

if ! agent_browser_output_has 'entryReady=1'; then
  fail "agent-browser smoke failed: guided check-in flow did not expose station entry"
fi

if ! agent_browser_output_has 'byTimeClicked=1'; then
  fail "agent-browser smoke failed: by-time command was not clickable"
fi

agent_browser_run "$session_name" screenshot "$out_dir/bot-smoke.png" >/dev/null
agent_browser_write_output "$session_name" "$out_dir/smoke-console.log" console || true
agent_browser_write_output "$session_name" "$out_dir/smoke-network.log" network requests || true

{
  echo "session=${session_name}"
  echo "chat_url=${chat_url}"
  echo "required_markers=ok(agent_browser_verified)"
} >"$out_dir/e2e-evidence.txt"

log "agent-browser smoke completed; artifacts in $out_dir"
