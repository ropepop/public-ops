#!/bin/sh
set -eu

: "${TICKET_ANDROID_SIM_ADB_TARGET:=ticket_android_sim:5555}"
: "${TICKET_ANDROID_SIM_STATUS_DIR:=/srv/android-sim/status}"
: "${TICKET_ANDROID_SIM_APK_DIR:=/srv/android-sim/apks}"
: "${TICKET_ANDROID_SIM_DISPLAY_SIZE:=540x960}"
: "${TICKET_ANDROID_SIM_DISPLAY_DENSITY:=220}"
: "${TICKET_ANDROID_SIM_TUNE_INTERVAL:=30}"
: "${TICKET_ANDROID_SIM_BOOT_TIMEOUT:=420}"
: "${TICKET_ANDROID_SIM_INSTALLER_TIMEOUT:=180}"

status_file="${TICKET_ANDROID_SIM_STATUS_DIR}/tuning-status.env"

log() {
  printf '[%s] android-sim-tuner: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*"
}

adb_target() {
  adb -s "${TICKET_ANDROID_SIM_ADB_TARGET}" "$@"
}

write_status() {
  result="$1"
  message="$2"
  boot_id="${3:-unknown}"
  swap_total="${4:-unknown}"
  display_size="${5:-unknown}"
  display_density="${6:-unknown}"

  mkdir -p "${TICKET_ANDROID_SIM_STATUS_DIR}"
  tmp="${status_file}.tmp"
  {
    printf 'result=%s\n' "${result}"
    printf 'message=%s\n' "${message}"
    printf 'tuned_at=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    printf 'adb_target=%s\n' "${TICKET_ANDROID_SIM_ADB_TARGET}"
    printf 'boot_id=%s\n' "${boot_id}"
    printf 'swap_total_kb=%s\n' "${swap_total}"
    printf 'display_size=%s\n' "${display_size}"
    printf 'display_density=%s\n' "${display_density}"
  } >"${tmp}"
  mv "${tmp}" "${status_file}"
}

android_shell() {
  adb_target shell "$@" 2>/dev/null | tr -d '\r' || true
}

current_boot_id() {
  id="$(android_shell 'cat /proc/sys/kernel/random/boot_id 2>/dev/null')"
  if [ -n "${id}" ]; then
    printf '%s\n' "${id}"
    return 0
  fi
  android_shell 'cat /proc/uptime 2>/dev/null' | awk '{print $1}'
}

current_swap_total() {
  android_shell 'su 0 cat /proc/meminfo 2>/dev/null' | awk '/^SwapTotal:/ {print $2; exit}'
}

wait_for_android() {
  adb connect "${TICKET_ANDROID_SIM_ADB_TARGET}" >/dev/null 2>&1 || true
  timeout "${TICKET_ANDROID_SIM_BOOT_TIMEOUT}" adb -s "${TICKET_ANDROID_SIM_ADB_TARGET}" wait-for-device >/dev/null 2>&1 || return 1

  deadline=$(( $(date +%s) + TICKET_ANDROID_SIM_BOOT_TIMEOUT ))
  while :; do
    booted="$(android_shell 'getprop sys.boot_completed')"
    if [ "${booted}" = "1" ]; then
      return 0
    fi
    if [ "$(date +%s)" -ge "${deadline}" ]; then
      return 1
    fi
    sleep 5
  done
}

wait_for_installer() {
  deadline=$(( $(date +%s) + TICKET_ANDROID_SIM_INSTALLER_TIMEOUT ))
  while :; do
    if adb_target shell cmd package list packages android >/dev/null 2>&1; then
      out="$(adb_target shell cmd package install-create -r -S 1 2>&1 | tr -d '\r' || true)"
      session="$(printf '%s\n' "${out}" | sed -n 's/.*\[\([0-9][0-9]*\)\].*/\1/p')"
      if printf '%s\n' "${out}" | grep -F 'Success:' >/dev/null 2>&1; then
        [ -n "${session}" ] && adb_target shell cmd package install-abandon "${session}" >/dev/null 2>&1 || true
        return 0
      fi
    fi
    if [ "$(date +%s)" -ge "${deadline}" ]; then
      return 1
    fi
    sleep 5
  done
}

disable_android_swap() {
  for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
    adb_target shell su 0 'swapoff /dev/block/zram0 2>/dev/null || swapoff -a 2>/dev/null || true; [ -e /sys/block/zram0/reset ] && echo 1 > /sys/block/zram0/reset 2>/dev/null || true; [ -e /sys/block/zram0/disksize ] && echo 0 > /sys/block/zram0/disksize 2>/dev/null || true' >/dev/null 2>&1 || true
    swap_total="$(current_swap_total)"
    if [ "${swap_total:-}" = "0" ]; then
      log "android_swap result=disabled swap_total_kb=0"
      return 0
    fi
    sleep 3
  done
  return 1
}

tune_display_and_background() {
  for _ in 1 2 3 4 5 6 7 8; do
    adb_target shell wm size "${TICKET_ANDROID_SIM_DISPLAY_SIZE}" >/dev/null 2>&1 || true
    adb_target shell wm density "${TICKET_ANDROID_SIM_DISPLAY_DENSITY}" >/dev/null 2>&1 || true
    size="$(android_shell 'wm size')"
    density="$(android_shell 'wm density')"
    if printf '%s\n' "${size}" | grep -F "${TICKET_ANDROID_SIM_DISPLAY_SIZE}" >/dev/null 2>&1 &&
      printf '%s\n' "${density}" | grep -F "${TICKET_ANDROID_SIM_DISPLAY_DENSITY}" >/dev/null 2>&1; then
      break
    fi
    sleep 5
  done
  adb_target shell settings put global window_animation_scale 0 >/dev/null 2>&1 || true
  adb_target shell settings put global transition_animation_scale 0 >/dev/null 2>&1 || true
  adb_target shell settings put global animator_duration_scale 0 >/dev/null 2>&1 || true
  adb_target shell settings put global background_process_limit 2 >/dev/null 2>&1 || true
  adb_target shell settings put global app_process_limit 2 >/dev/null 2>&1 || true
  adb_target shell settings put global cached_apps_freezer enabled >/dev/null 2>&1 || true
  adb_target shell settings put global wifi_scan_always_enabled 0 >/dev/null 2>&1 || true
  adb_target shell settings put global ble_scan_always_enabled 0 >/dev/null 2>&1 || true
  log "avd_optimization display=${TICKET_ANDROID_SIM_DISPLAY_SIZE} density=${TICKET_ANDROID_SIM_DISPLAY_DENSITY} background_process_limit=2 cached_apps_freezer=enabled scans=disabled"
}

disable_nonessential_package() {
  package="$1"
  if ! adb_target shell pm path "${package}" >/dev/null 2>&1; then
    return 0
  fi
  if adb_target shell su 0 cmd package disable-user --user 0 "${package}" >/dev/null 2>&1; then
    log "package_tune package=${package} result=disabled-user"
  else
    adb_target shell am force-stop "${package}" >/dev/null 2>&1 || true
    log "package_tune package=${package} result=force-stopped"
  fi
}

disable_nonessential_packages() {
  packages='
com.google.android.gm
com.google.android.apps.maps
com.google.android.apps.photos
com.google.android.apps.youtube.music
com.google.android.apps.docs
com.google.android.googlequicksearchbox
com.google.android.apps.wellbeing
com.google.android.apps.wallpaper
com.google.android.apps.wallpaper.nexus
com.google.android.apps.customization.pixel
com.google.android.feedback
com.google.android.apps.restore
com.google.android.onetimeinitializer
com.google.android.partnersetup
com.google.android.projection.gearhead
com.google.android.tts
com.google.android.dialer
com.google.android.contacts
com.google.android.calendar
com.google.android.apps.messaging
com.google.android.deskclock
com.google.android.soundpicker
com.google.android.cellbroadcastreceiver
com.google.android.cellbroadcastservice
com.google.android.tag
com.google.android.printservice.recommendation
'
  for package in ${packages}; do
    disable_nonessential_package "${package}"
  done
}

install_if_missing() {
  label="$1"
  package="$2"
  apk="$3"
  if adb_target shell pm path "${package}" >/dev/null 2>&1; then
    log "store_client label=${label} package=${package} result=already-installed"
    return 0
  fi
  if [ ! -s "${apk}" ]; then
    log "store_client label=${label} package=${package} result=missing-apk path=${apk}"
    return 0
  fi
  for attempt in 1 2 3 4 5 6 7 8 9 10 11 12; do
    if adb_target install -r "${apk}" >/tmp/android-sim-install.log 2>&1; then
      log "store_client label=${label} package=${package} result=installed"
      return 0
    fi
    cat /tmp/android-sim-install.log >&2 || true
    if [ "${attempt}" = "12" ]; then
      log "store_client label=${label} package=${package} result=install-failed"
      return 1
    fi
    sleep 10
  done
}

install_or_update() {
  label="$1"
  package="$2"
  apk="$3"
  if [ ! -s "${apk}" ]; then
    log "store_client label=${label} package=${package} result=missing-apk path=${apk}"
    return 0
  fi
  for attempt in 1 2 3 4 5 6 7 8 9 10 11 12; do
    if adb_target install -r "${apk}" >/tmp/android-sim-install.log 2>&1; then
      log "store_client label=${label} package=${package} result=updated"
      return 0
    fi
    cat /tmp/android-sim-install.log >&2 || true
    if grep -F 'INSTALL_FAILED_UPDATE_INCOMPATIBLE' /tmp/android-sim-install.log >/dev/null 2>&1; then
      log "store_client label=${label} package=${package} result=signature-mismatch-uninstall"
      adb_target uninstall "${package}" >/dev/null 2>&1 || true
      if adb_target install -r "${apk}" >/tmp/android-sim-install.log 2>&1; then
        log "store_client label=${label} package=${package} result=reinstalled"
        return 0
      fi
      cat /tmp/android-sim-install.log >&2 || true
      return 1
    fi
    if [ "${attempt}" = "12" ]; then
      log "store_client label=${label} package=${package} result=update-failed"
      return 1
    fi
    sleep 10
  done
}

start_ticket_controller() {
  package="lv.jolkins.pixelorchestrator"
  apk="${TICKET_ANDROID_SIM_APK_DIR}/pixel-orchestrator-debug.apk"
  install_or_update TicketPhoneService "${package}" "${apk}" || true
  if adb_target shell pm path "${package}" >/dev/null 2>&1; then
    adb_target shell pm grant "${package}" android.permission.POST_NOTIFICATIONS >/dev/null 2>&1 || true
    adb_target shell pm grant "${package}" android.permission.WRITE_SECURE_SETTINGS >/dev/null 2>&1 || true
    adb_target shell am start -n "${package}/.app.MainActivity" >/dev/null 2>&1 || true
    sleep 4
    adb_target shell am broadcast \
      -n "${package}/.app.OrchestratorActionReceiver" \
      --es orchestrator_action ticket_start_server >/dev/null 2>&1 || true
    log "ticket_phone_service package=${package} result=start-requested"
  fi
}

run_tuning_once() {
  boot_id="$(current_boot_id)"
  if ! wait_for_installer; then
    write_status failed installer_not_ready "${boot_id}" "$(current_swap_total)" "$(android_shell 'wm size')" "$(android_shell 'wm density')"
    return 1
  fi
  if ! disable_android_swap; then
    write_status failed swap_not_disabled "${boot_id}" "$(current_swap_total)" "$(android_shell 'wm size')" "$(android_shell 'wm density')"
    return 1
  fi
  tune_display_and_background
  disable_nonessential_packages
  install_if_missing Accrescent app.accrescent.client "${TICKET_ANDROID_SIM_APK_DIR}/accrescent.apk" || true
  install_if_missing Aurora com.aurora.store "${TICKET_ANDROID_SIM_APK_DIR}/aurora-store.apk" || true
  start_ticket_controller
  if ! disable_android_swap; then
    write_status failed swap_not_disabled_after_install "${boot_id}" "$(current_swap_total)" "$(android_shell 'wm size')" "$(android_shell 'wm density')"
    return 1
  fi
  write_status ok tuned "${boot_id}" "$(current_swap_total)" "$(android_shell 'wm size')" "$(android_shell 'wm density')"
  return 0
}

last_tuned_boot=""
log "starting target=${TICKET_ANDROID_SIM_ADB_TARGET} status=${status_file}"

while :; do
  if ! wait_for_android; then
    write_status waiting android_not_ready unknown unknown unknown unknown
    sleep "${TICKET_ANDROID_SIM_TUNE_INTERVAL}"
    continue
  fi

  boot_id="$(current_boot_id)"
  swap_total="$(current_swap_total)"
  if [ "${boot_id}" != "${last_tuned_boot}" ] || [ "${swap_total:-unknown}" != "0" ]; then
    if run_tuning_once; then
      last_tuned_boot="${boot_id}"
    else
      log "tuning failed; will retry"
    fi
  fi

  sleep "${TICKET_ANDROID_SIM_TUNE_INTERVAL}"
done
