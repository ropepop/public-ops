const ACTIVATION_TARGETS = new Set(['open_latest_and_register', 'register_current']);
export function ticketActionV3ClientId(scope, now = Date.now(), entropy = Math.random().toString(36).slice(2, 10)) {
  const cleanScope = String(scope || 'action').toLowerCase().replace(/[^a-z0-9_-]+/g, '_').slice(0, 32) || 'action';
  const cleanEntropy = String(entropy || '').toLowerCase().replace(/[^a-z0-9_-]+/g, '').slice(0, 24) || 'client';
  return `ticket_action_v3_${cleanScope}_${Math.max(0, Math.trunc(Number(now) || 0))}_${cleanEntropy}`;
}

export function ticketActionV3RequestArgs(input) {
  const target = String(input && input.target || '');
  const actionId = String(input && input.actionId || '');
  return {
    actionId,
    target,
    source: String(input && input.source || ''),
    reason: String(input && input.reason || ''),
    attemptId: ACTIVATION_TARGETS.has(target) ? actionId : '',
    expectedInteractionRevision: target === 'register_current'
      ? String(input && input.expectedInteractionRevision || '')
      : '',
    scheduleId: String(input && input.scheduleId || '')
  };
}

export function adminRedetectTicketActionV3Args(actionId) {
  return ticketActionV3RequestArgs({
    actionId,
    target: 'redetect_latest',
    source: 'ticket_remote_admin',
    reason: 'ticket_action_requested'
  });
}

export function adminScheduleTicketActionV3Args(input) {
  const scheduledAtMillis = Math.trunc(Number(input && input.scheduledAtMillis));
  if (!Number.isFinite(scheduledAtMillis) || scheduledAtMillis <= 0) {
    throw new Error('invalid scheduled time');
  }
  return {
    scheduleId: String(input && input.scheduleId || ''),
    scheduledAtMicros: BigInt(scheduledAtMillis) * 1000n,
    phoneLocalTime: String(input && input.phoneLocalTime || ''),
    phoneTimeZone: String(input && input.phoneTimeZone || ''),
    target: 'redetect_latest'
  };
}

export function ticketActionV3ZonedLocalMillis(dateValue, timeValue, timeZone) {
  const match = `${dateValue}T${timeValue}`.match(/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/);
  if (!match) throw new Error('Izvēlies derīgu datumu un laiku.');
  const desired = match.slice(1).map(Number);
  const desiredUtc = Date.UTC(desired[0], desired[1] - 1, desired[2], desired[3], desired[4]);
  const formatter = new Intl.DateTimeFormat('en-CA', {
    timeZone, year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', hourCycle: 'h23'
  });
  const expectedLocal = `${dateValue}T${timeValue}`;
  const candidates = [];
  for (let offsetMinutes = -14 * 60; offsetMinutes <= 14 * 60; offsetMinutes += 15) {
    const candidate = desiredUtc - offsetMinutes * 60_000;
    const parts = Object.fromEntries(formatter.formatToParts(new Date(candidate)).map((part) => [part.type, part.value]));
    const renderedLocal = `${parts.year}-${parts.month}-${parts.day}T${parts.hour}:${parts.minute}`;
    if (renderedLocal === expectedLocal) candidates.push(candidate);
  }
  if (candidates.length === 0) {
    throw new Error('Šis vietējais laiks nepastāv laika joslas maiņas dēļ.');
  }
  return Math.min(...candidates);
}

function ticketActionV3CreatedOrder(action) {
  const value = String(action && action.createdAt || '').trim();
  const millis = Date.parse(value);
  if (!Number.isFinite(millis)) return null;
  const fractional = value.match(/T\d{2}:\d{2}:\d{2}(?:\.(\d+))?(?:Z|[+-]\d{2}:\d{2})$/i)?.[1] || '';
  const subMillis = Number(`${fractional.slice(3, 9)}000000`.slice(0, 6));
  return { millis, subMillis };
}

export function compareTicketActionV3Authority(left, right) {
  const leftCreated = ticketActionV3CreatedOrder(left);
  const rightCreated = ticketActionV3CreatedOrder(right);
  if (leftCreated && rightCreated) {
    if (leftCreated.millis !== rightCreated.millis) return rightCreated.millis - leftCreated.millis;
    if (leftCreated.subMillis !== rightCreated.subMillis) return rightCreated.subMillis - leftCreated.subMillis;
  } else if (leftCreated || rightCreated) {
    return leftCreated ? -1 : 1;
  }
  const actionIdOrder = String(right && right.actionId || '').localeCompare(String(left && left.actionId || ''));
  if (actionIdOrder) return actionIdOrder;
  return String(right && right.id || '').localeCompare(String(left && left.id || ''));
}

export function ticketActionV3ActionsByAuthority(actions) {
  return (Array.isArray(actions) ? [...actions] : []).sort(compareTicketActionV3Authority);
}

export function ticketActionV3ActivationTerminalMessage(action) {
  if (!action || !ACTIVATION_TARGETS.has(String(action.target || ''))) return '';
  if (!['failed', 'needs_attention'].includes(String(action.status || ''))) return '';
  switch (String(action.phase || '')) {
    case 'not_dispatched':
      if (action.reason === 'ticket_action_accessibility_unavailable') {
        return 'Tālruņa vadība nav pieejama. Vilkšana netika nosūtīta; īpašniekam jāpārbauda tālrunis.';
      }
      return 'To pašu atvērto biļeti neizdevās apstiprināt; nekas netika pavilkts.';
    case 'retry_not_dispatched':
      return 'Pirmā vilkšana biļeti nemainīja. Atkārtotās vilkšanas gatavību nevarēja droši apstiprināt, tāpēc otrā vilkšana netika nosūtīta.';
    case 'no_transition':
      return 'Abi atļautie vilkšanas mēģinājumi tika pabeigti, bet ViVi joprojām rāda nereģistrētu biļeti. Citas vilkšanas netika nosūtītas.';
    case 'outcome_unknown':
      return 'Vilkšanas rezultātu nevarēja droši apstiprināt. Pirms jauna mēģinājuma pārbaudi biļeti vēlreiz.';
    default:
      return '';
  }
}

export function ticketActionV3IsExpectedEmptyRedetect(action) {
  const streamEpoch = Number(action && action.streamEpoch);
  const frameSequence = Number(action && action.frameSequence);
  return Boolean(action &&
    String(action.target || '') === 'redetect_latest' &&
    String(action.status || '') === 'failed' &&
    String(action.phase || '') === 'failed' &&
    String(action.currentView || '') === 'unknown' &&
    String(action.reason || '') === 'ticket_action_latest_not_detected' &&
    Number.isFinite(streamEpoch) && streamEpoch > 0 &&
    Number.isFinite(frameSequence) && frameSequence > 0);
}

export function adminRedetectTicketActionV3TerminalMessage(action) {
  if (!action || String(action.target || '') !== 'redetect_latest') return '';
  const status = String(action.status || '');
  if (!['succeeded', 'failed', 'needs_attention'].includes(status)) return '';
  if (ticketActionV3IsExpectedEmptyRedetect(action)) return 'No tickets found.';
  if (status === 'succeeded') return 'Latest ticket redetection completed.';
  if (status === 'needs_attention') {
    return 'The phone view needs manual attention; the action was not repeated.';
  }
  return 'Ticket redetection stopped safely without an unproven action.';
}

export function ticketCurrentSwitchView(current, now = Date.now()) {
  if (!current || !Number.isFinite(now) || !(Date.parse(current.expiresAt) > now)) return '';
  return ['latest_unactivated', 'recent_activated'].includes(current.currentView) ? current.currentView : '';
}

export function ticketActionV3SmartSwitchForView(currentView) {
  if (String(currentView || '') === 'latest_unactivated') {
    return {
      label: 'Skatīt pēdējo reģistrēto biļeti',
      target: 'show_recent_activated'
    };
  }
  if (String(currentView || '') === 'recent_activated') {
    return {
      label: 'Atgriezties pie nereģistrētās biļetes',
      target: 'return_to_latest_unactivated'
    };
  }
  return {
    label: 'Skatīt pēdējo reģistrēto biļeti',
    target: ''
  };
}

export function ticketMemberLimitBlocks(limits, kind) {
  if (!limits) return true;
  if (limits.effectiveLimited === false) return false;
  if (kind === 'registration') return limits.registrationAllowed !== true;
  if (kind === 'control_code') return limits.controlCodeAllowed !== true;
  return true;
}

export function ticketMemberLimitCountdown(targetAt, now = Date.now(), translate = (text, values = {}) => text.replace(/\{(\w+)\}/g, (match, key) => values[key] ?? match)) {
  const target = Date.parse(String(targetAt || ''));
  if (!Number.isFinite(target)) return '';
  const remainingSeconds = Math.max(0, Math.ceil((target - Number(now)) / 1000));
  if (remainingSeconds <= 0) return translate('gaida SpaceTime atjauninājumu');
  if (remainingSeconds < 60) return translate('pēc {n} s', { n: remainingSeconds });
  const minutes = Math.floor(remainingSeconds / 60);
  const seconds = remainingSeconds % 60;
  return translate(seconds ? 'pēc {n} min {s} s' : 'pēc {n} min', { n: minutes, s: seconds });
}


export function ticketSliderRegionV3Layout(region, canvasRect, stageRect) {
  if (!region || !canvasRect || !stageRect) return null;
  const left = Number(canvasRect.left) - Number(stageRect.left) +
    Number(region.leftBasisPoints) / 10000 * Number(canvasRect.width);
  const top = Number(canvasRect.top) - Number(stageRect.top) +
    Number(region.topBasisPoints) / 10000 * Number(canvasRect.height);
  const width = (Number(region.rightBasisPoints) - Number(region.leftBasisPoints)) / 10000 * Number(canvasRect.width);
  const height = (Number(region.bottomBasisPoints) - Number(region.topBasisPoints)) / 10000 * Number(canvasRect.height);
  if (![left, top, width, height].every(Number.isFinite) || width <= 0 || height <= 0) return null;
  return { left, top, width, height };
}
