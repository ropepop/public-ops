export async function prepareOwnerViviAccessBeforeSubscribe({ ownerViviAuth, prepare, subscribe, ready }) {
  if (ownerViviAuth) await prepare();
  const result = subscribe();
  ready();
  return result;
}

export function ownerViviCredentialSnapshot(credentialState, credentials) {
  const configured = credentialState && credentialState.configured === true;
  const revision = String(credentialState && credentialState.revision || credentials && credentials.revision || '');
  const credentialRevision = String(credentials && credentials.revision || '');
  const credentialsMatchCurrentRevision = Boolean(credentials) && Boolean(revision) && credentialRevision === revision;
  const ready = configured ? credentialsMatchCurrentRevision : !credentials;
  return { configured, revision, ready, credentialsMatchCurrentRevision };
}

export const OWNER_VIVI_PRIVATE_VIEW_GRACE_MS = 5000;

export function ownerViviPrivateViewGapDisposition({ snapshotReady }) {
  return snapshotReady === true ? 'none' : 'grace';
}

export function ownerViviStateUpdateAllowed({ connection, snapshotApplied, disposed, accessClosed }) {
  return connection === 'live' && snapshotApplied === true && disposed !== true && accessClosed !== true;
}

export function ownerViviConnectionEventAllowed({ eventGeneration, currentGeneration, eventConnection, currentConnection }) {
  return eventGeneration === currentGeneration && eventConnection === currentConnection;
}

export function ownerViviStatusRequiresHardRevoke(status) {
  return status === 'owner_vivi_access_failed';
}

export function resetOwnerViviCredentialCopies(model) {
  Object.assign(model, {
    email: '',
    password: '',
    authoritativeEmail: '',
    authoritativePassword: '',
    dirty: false,
    confirm: ''
  });
}

export function resetOwnerViviConnectionAuthority(model) {
  resetOwnerViviCredentialCopies(model);
  Object.assign(model, {
    ownerViewObserved: false,
    ownerCredentialReady: false
  });
}

export function createOwnerViviPrivateViewFence({
  onExpired,
  setTimeoutFn = globalThis.setTimeout,
  clearTimeoutFn = globalThis.clearTimeout,
  graceMs = OWNER_VIVI_PRIVATE_VIEW_GRACE_MS
}) {
  let timer = null;
  return {
    arm() {
      if (timer !== null) return;
      timer = setTimeoutFn(() => {
        timer = null;
        onExpired();
      }, graceMs);
    },
    clear() {
      if (timer === null) return;
      clearTimeoutFn(timer);
      timer = null;
    },
    active() {
      return timer !== null;
    }
  };
}

export function ownerViviPageRestoreRequiresReload({ persisted, disposed }) {
  return persisted === true && disposed === true;
}

export function resetOwnerViviSensitiveModel(model, { connection, message }) {
  Object.assign(model, {
    connection,
    loaded: false,
    configured: false,
    email: '',
    password: '',
    authoritativeEmail: '',
    authoritativePassword: '',
    ownerViewObserved: false,
    ownerCredentialReady: false,
    revision: '',
    updatedAt: '',
    dirty: false,
    operation: '',
    operationBaselineRevision: '',
    operationTargetRevision: '',
    operationRequestId: '',
    focusedRequestId: '',
    message,
    confirm: '',
    attempts: [],
    attempt: null
  });
}
