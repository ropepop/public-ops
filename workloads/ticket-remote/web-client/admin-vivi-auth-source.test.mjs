import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import {
  newViviOperationId,
  selectViviReauthAttempt,
  viviReauthAttemptsBusy,
  viviReauthMode,
  viviReauthStatusText
} from './admin-vivi-auth-source.js';

test('ViVi operation IDs are securely generated and scoped', () => {
  const cryptoRef = { randomUUID: () => '12345678-1234-1234-1234-123456789abc' };
  assert.equal(
    newViviOperationId('vivi-reauth', cryptoRef),
    'vivi-reauth-12345678-1234-1234-1234-123456789abc'
  );
  assert.equal(
    newViviOperationId('vivi-full-reset', cryptoRef),
    'vivi-full-reset-12345678-1234-1234-1234-123456789abc'
  );
  assert.equal(
    newViviOperationId('vivi-logout-login', cryptoRef),
    'vivi-logout-login-12345678-1234-1234-1234-123456789abc'
  );
  assert.equal(
    newViviOperationId('vivi-logout-redetect-login', cryptoRef),
    'vivi-logout-redetect-login-12345678-1234-1234-1234-123456789abc'
  );
  assert.throws(() => newViviOperationId('../unsafe', cryptoRef), /safe operation prefix/);
});

test('ViVi re-authentication status uses authoritative outcome fields', () => {
  assert.match(viviReauthStatusText({ status: 'queued' }), /queued/);
  assert.match(viviReauthStatusText({ status: 'pending' }), /waiting for the phone/);
  assert.match(viviReauthStatusText({ status: 'running', phase: 'opening_account_controls' }), /opening ViVi account controls/);
  assert.match(viviReauthStatusText({ status: 'running', phase: 'resetting_vivi' }), /emergency app-data reset/);
  assert.match(viviReauthStatusText({ status: 'running', phase: 'redetecting_latest_ticket' }), /newest unused ticket because the original ticket is unavailable/);
  assert.match(viviReauthStatusText({ requestId: 'vivi-logout-login-01', status: 'succeeded' }), /non-destructive in-app sign-out flow/);
  assert.match(viviReauthStatusText({ requestId: 'vivi-logout-redetect-login-01', status: 'succeeded', reason: 'saved_credentials_original_ticket_restored' }), /restored the originally open ticket/);
  assert.match(viviReauthStatusText({ requestId: 'vivi-logout-redetect-login-01', status: 'succeeded', reason: 'saved_credentials_latest_ticket_redetected' }), /opened the newest unused ticket/);
  assert.match(viviReauthStatusText({ requestId: 'vivi-logout-redetect-login-01', status: 'succeeded', reason: 'saved_credentials_latest_ticket_redetected' }), /not activated/);
  assert.match(viviReauthStatusText({ requestId: 'vivi-logout-redetect-login-01', status: 'succeeded', reason: 'saved_credentials_no_ticket_proven' }), /no unused ticket is currently available/);
  assert.match(viviReauthStatusText({ requestId: 'vivi-logout-redetect-login-01', status: 'succeeded', reason: 'saved_credentials_sign_in_proven' }), /unrecognized re-authentication result/);
  assert.match(viviReauthStatusText({ requestId: 'vivi-logout-redetect-login-01', status: 'succeeded', reason: 'saved_credentials_sign_in_proven' }), /needing verification/);
  assert.match(viviReauthStatusText({ requestId: 'vivi-full-reset-01', status: 'succeeded' }), /emergency full reset/);
  assert.match(viviReauthStatusText({ requestId: 'vivi-reauth-01', status: 'succeeded' }), /legacy compatibility flow/);
  assert.match(viviReauthStatusText({ status: 'needs_attention', reason: 'additional_verification_required' }), /additional verification/);
  assert.match(viviReauthStatusText({ status: 'needs_attention', reason: 'logout_action_uncertain' }), /stopped instead of repeating/);
  assert.doesNotMatch(viviReauthStatusText({ status: 'running', phase: 'password=secret' }), /password=secret/);
  assert.doesNotMatch(viviReauthStatusText({ status: 'needs_attention', reason: 'password=secret' }), /password=secret/);
});

test('ViVi attempt focus and busy state use the complete safe attempt list', () => {
  const completed = { requestId: 'vivi-logout-login-old', status: 'succeeded' };
  const running = { requestId: 'vivi-logout-login-running', status: 'running' };
  const queued = { requestId: 'vivi-logout-login-focused', status: 'queued' };
  const attempts = [running, completed, queued];

  assert.equal(selectViviReauthAttempt(attempts, queued.requestId), queued);
  assert.equal(selectViviReauthAttempt(attempts, 'missing'), running);
  assert.equal(viviReauthAttemptsBusy(attempts), true);
  assert.equal(viviReauthAttemptsBusy([completed]), false);
  assert.equal(viviReauthMode(queued), 'logout-login');
  assert.equal(viviReauthMode({ requestId: 'vivi-logout-redetect-login-01' }), 'logout-redetect-login');
  assert.equal(viviReauthMode({ requestId: 'vivi-full-reset-01' }), 'full-reset');
  assert.equal(viviReauthMode({ requestId: 'vivi-reauth-01' }), 'legacy');
  assert.equal(viviReauthMode({ requestId: 'vivi-logout-login-' }), 'unknown');
  assert.equal(viviReauthMode({ requestId: 'vivi-logout-redetect-login-' }), 'unknown');
  assert.equal(viviReauthMode({ requestId: 'vivi-full-reset-' }), 'unknown');
  assert.equal(viviReauthMode({ requestId: 'vivi-reauth-' }), 'unknown');
  assert.equal(viviReauthMode({ requestId: 'unknown-01' }), 'unknown');
});

test('Spacetime client keeps version 3 as the default and selects version 4 on the same reducer only for fallback re-detection', () => {
  const clientSource = readFileSync(fileURLToPath(new URL('./src/index.ts', import.meta.url)), 'utf8');
  const method = clientSource
    .split('requestViviReauthLogoutLogin(')[1]
    ?.split('requestViviReauthFullReset(')[0] || '';

  assert.match(method, /redetectAfterLogin = false/);
  assert.match(method, /this\.callReducer\("ownerRequestViviReauthLogoutLogin"/);
  assert.match(method, /version: redetectAfterLogin \? 4 : 3/);
  assert.doesNotMatch(method, /redetectAfterLogin:/);
});
