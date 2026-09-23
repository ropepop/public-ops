#!/usr/bin/env python3
"""Invitation authority against a disposable database; no live phone/auth/provider.

Only the temporary module's JWT payload reader/connection hook is replaced with
synthetic identities. Actual claim validation, reducers, views and transactions
remain production code. No production module or bindings are overwritten.
"""
import concurrent.futures
import hashlib
import json
import os
from pathlib import Path
import runpy
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

SOURCE = Path(__file__).resolve().parents[1]
HELPERS = runpy.run_path(str(SOURCE.parent / 'scripts/test-action-statistics.py'))
TICKET = 'vivi-default'

def main():
    with tempfile.TemporaryDirectory(prefix='ticket-invitation-test-') as temporary:
        directory = Path(temporary)
        module = directory / 'module'
        shutil.copytree(SOURCE / 'src', module / 'src')
        for name in ('Cargo.toml', 'Cargo.lock'):
            shutil.copy2(SOURCE / name, module / name)
        source = (module / 'src/lib.rs').read_text()
        source = HELPERS['replace_function'](source, 'jwt_payload', '''
            let row = ctx.db.fixture_auth().identity().find(ctx.sender()).ok_or("auth required")?;
            serde_json::from_str(&row.payload).map_err(|_| "invalid fixture payload".into())
        ''')
        # Opaque local identities have no external JWT, but claim content still
        # passes the production issuer/audience/subject/role/email checks.
        source = source.replace('if !ctx.sender_auth().has_jwt() {', 'if ctx.db.fixture_auth().identity().find(ctx.sender()).is_none() {')
        source = HELPERS['replace_function'](source, 'identity_connected', '\n let _ = ctx; Ok(())\n')
        source += (SOURCE / 'test-fixtures/invitations.rs').read_text()
        (module / 'src/lib.rs').write_text(source)
        env = {**os.environ, 'CARGO_TARGET_DIR': str(SOURCE / 'target/invitation-fixture')}
        result = subprocess.run(['spacetime', 'build', '--module-path', str(module)], cwd=directory, env=env, capture_output=True, text=True)
        if result.returncode: raise RuntimeError(result.stdout + result.stderr)
        wasm = Path(env['CARGO_TARGET_DIR']) / 'wasm32-unknown-unknown/release/ticket_remote_spacetimedb.wasm'
        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0)); port = probe.getsockname()[1]
        url = f'http://127.0.0.1:{port}'
        with (directory / 'server.log').open('w') as log:
            server = subprocess.Popen(['spacetime', 'start', '--listen-addr', f'127.0.0.1:{port}', '--data-dir', str(directory / 'data'), '--non-interactive'], stdout=log, stderr=log)
            try:
                for _ in range(100):
                    try: urllib.request.urlopen(url + '/v1/ping', timeout=1).close(); break
                    except OSError: time.sleep(.1)
                HELPERS['run'](['spacetime', 'publish', '--server', url, '--no-config', '--yes', '--bin-path', str(wasm), '--delete-data=never', 'invitation-fixture'], cwd=directory)
                def http(path, data=b'', token=None):
                    headers = {'Content-Type':'application/json'}
                    if token: headers['Authorization'] = 'Bearer ' + token
                    with urllib.request.urlopen(urllib.request.Request(url + path, data=data, headers=headers), timeout=15) as response:
                        body = response.read(); return json.loads(body) if body else None
                def identity(): return http('/v1/identity')['token']
                def call(token, name, *args): return http('/v1/database/invitation-fixture/call/' + name, json.dumps(args).encode(), token)
                def sql(token, table):
                    result = http('/v1/database/invitation-fixture/sql', ('SELECT * FROM ' + table).encode(), token)[0]
                    # JSON column schema carries its name under algebraic product elements.
                    names = [field['name']['some'] if isinstance(field['name'], dict) else field['name'] for field in result['schema']['elements']]
                    return [dict(zip(names, row)) for row in result['rows']]
                def rejects(action, reason=None):
                    try: action()
                    except urllib.error.HTTPError as error:
                        body = error.read().decode()
                        if reason: assert reason in body, body
                        return False
                    raise AssertionError('unexpected authorized operation')
                service, owner, guest, other, outsider = [identity() for _ in range(5)]
                call(service, 'fixture_bind', 'service', '', '')
                call(service, 'ticketremote_service_bootstrap', TICKET, 'Fixture', 'owner@example.test', 'pixel', 'http://127.0.0.1:9', 'fixture', 'https://example.test', 'fixture')
                call(owner, 'fixture_bind', 'member', 'owner@example.test', '')
                def create(name, days=3, minutes=15):
                    digest = hashlib.sha256(name.encode()).hexdigest()
                    call(service, 'ticketremote_create_invitation', TICKET, name, digest, name, 'owner@example.test', days * 1440, minutes)
                    return digest
                def row(name): return next(v for v in sql(service, 'ticketremote_service_invitation') if v['id'] == name)
                digest = create('trial')
                create('trial')
                rejects(lambda: create('trial', 1, 5), 'invitation_id_reused')
                for days in (1,3,5):
                    for minutes in (5,15,30):
                        name = f'presets-{days}-{minutes}'; create(name, days, minutes)
                        assert row(name)['streamAllowanceMs'] == minutes * 60_000
                for bad in (0, 525601):
                    rejects(lambda: call(service, 'ticketremote_create_invitation', TICKET, 'bad', 'b'*64, '', 'owner@example.test', bad, 15))
                rejects(lambda: call(owner, 'ticketremote_create_invitation', TICKET, 'denied', 'c'*64, '', 'owner@example.test', 3, 15), 'service role required')
                rejects(lambda: call(service, 'ticketremote_create_invitation', TICKET, 'denied', 'c'*64, '', 'nobody@example.test', 3, 15), 'forbidden')
                assert row('trial')['startedAt'] == '' and row('trial')['streamUsedMs'] == 0
                call(service, 'ticketremote_start_invitation', TICKET, digest, 'first', False)
                call(guest, 'fixture_bind', 'guest', 'trial', 'first')
                for token in (owner, guest, outsider):
                    assert sql(token, 'ticketremote_service_invitation') == []
                    assert sql(token, 'ticketremote_service_member_source') == []
                    for table in ('invitation','guest_identity','invitation_action','member_source'):
                        rejects(lambda: sql(token, 'ticketremote_' + table))
                print('PASS presets, creator permissions, zero-cost welcome and private projections', flush=True)
                call(service, 'ticketremote_reserve_invitation_stream', TICKET, 'trial', 'first', 1, False)
                before = row('trial'); assert before['streamUsedMs'] == 5000
                call(service, 'ticketremote_reserve_invitation_stream', TICKET, 'trial', 'first', 1, False)
                assert row('trial')['leaseUntilMs'] == before['leaseUntilMs']
                rejects(lambda: call(service, 'ticketremote_start_invitation', TICKET, digest, 'second', False), 'invitation_continue_here_required')
                call(service, 'ticketremote_start_invitation', TICKET, digest, 'second', True)
                assert row('trial')['streamUsedMs'] < 5000
                rejects(lambda: call(guest, 'fixture_reserve_action', 'stale', 'register_current', False), 'invitation_session_changed')
                call(other, 'fixture_bind', 'guest', 'trial', 'second')
                assert sql(guest, 'ticketremote_member_checkin') == []
                assert sql(guest, 'ticketremote_member_checkin_groups') == []
                rejects(lambda: call(other, 'ticketremote_member_upsert_member', TICKET, 'intruder@example.test', 'admin'), 'verified email required')
                rejects(lambda: call(other, 'ticketremote_revoke_invitation', TICKET, 'trial', 'owner@example.test'), 'service role required')
                call(other, 'ticketremote_member_check_in', TICKET, 'check1', '', 'towards_riga', 2)
                assert sql(other, 'ticketremote_member_checkin')[0]['carriage'] == 2
                assert sql(other, 'ticketremote_owner_vivi_credentials') == []
                assert sql(other, 'ticketremote_privileged_viewers') == []
                print('PASS durable idempotent metering, explicit transfer, stale-session and privileged-operation rejection', flush=True)
                rejects(lambda: call(other, 'fixture_reserve_action', 'rollback', 'register_current', True), 'fixture_rollback')
                assert row('trial')['activationsReserved'] == 0
                call(other, 'fixture_reserve_action', 'failure', 'register_current', False)
                call(other, 'fixture_reserve_action', 'failure', 'register_current', False)
                assert row('trial')['activationsReserved'] == 1
                call(service, 'fixture_settle_action', 'failure', False, False)
                assert row('trial')['activationsReserved'] == 1
                call(service, 'fixture_settle_action', 'failure', False, True)
                call(service, 'fixture_settle_action', 'failure', False, True)
                assert row('trial')['activationsReserved'] == 0
                for index in range(5):
                    call(other, 'fixture_reserve_action', f'success-{index}', 'register_current', False)
                    call(service, 'fixture_settle_action', f'success-{index}', True, False)
                    call(service, 'fixture_settle_action', f'success-{index}', True, False)
                    assert row('trial')['activationsUsed'] == index+1
                rejects(lambda: call(other, 'fixture_reserve_action', 'sixth', 'control_code', False), 'invitation_registration_required')
                call(service, 'ticketremote_reserve_invitation_stream', TICKET, 'trial', 'second', 2, True)
                call(service, 'ticketremote_release_invitation_stream', TICKET, 'trial', 'second', 2)
                call(service, 'fixture_no_guest_members')
                print('PASS rollback, duplicate reservation/success, ambiguous retention, failure refund, fifth action and bounded result lease', flush=True)
                codes = create('code-trial'); call(service, 'ticketremote_start_invitation', TICKET, codes, 'code-session', False)
                call(guest, 'fixture_bind', 'guest', 'code-trial', 'code-session')
                # Real command admission sees missing phone proof and refunds its
                # reservation in the same committed rejection transaction.
                call(guest, 'fixture_command', 'rejected-slider', 'register_current')
                assert row('code-trial')['activationsReserved'] == 0
                rejects(lambda: call(guest, 'ticketremote_member_command', 2, TICKET, 'pixel', 'forbidden-owner', 'vivi_full_reset', 'pc-fixture:1', '2026-01-01T00:00:00Z', '{"credentialRevision":"revision"}'), 'invitation_operation_forbidden')
                for index in range(5):
                    call(guest, 'fixture_reserve_action', f'code-{index}', 'control_code', False)
                    call(service, 'fixture_settle_action', f'code-{index}', True, False)
                assert row('code-trial')['controlCodesUsed'] == 5
                rejects(lambda: call(guest, 'fixture_command', 'after-codes', 'open_latest_unactivated'), 'invitation_registration_required')
                print('PASS production command admission, owner-action rejection and five-code exhaustion', flush=True)
                expired = create('expired'); call(service, 'fixture_age_invitation', 'expired')
                rejects(lambda: call(service, 'ticketremote_start_invitation', TICKET, expired, 'old', False), 'invitation_registration_required')
                call(service, 'ticketremote_redeem_invitation', TICKET, expired, 'new@example.test')
                call(service, 'ticketremote_redeem_invitation', TICKET, expired, 'new@example.test')
                sources = sql(service, 'ticketremote_service_member_source')
                assert next(v for v in sources if v['email'] == 'new@example.test')['invitationId'] == 'expired'
                rejects(lambda: call(service, 'ticketremote_redeem_invitation', TICKET, expired, 'other@example.test'), 'invitation_invalid')
                call(service, 'fixture_deactivate', 'new@example.test')
                fresh = create('no-resurrection')
                rejects(lambda: call(service, 'ticketremote_redeem_invitation', TICKET, fresh, 'new@example.test'), 'invitation_member_removed')
                call(service, 'ticketremote_redeem_invitation', TICKET, fresh, 'owner@example.test')
                assert row('no-resurrection')['redeemedAt'] == ''
                call(service, 'fixture_manual_member', 'manual@example.test')
                assert next(v for v in sql(service, 'ticketremote_service_member_source') if v['email'] == 'manual@example.test')['source'] == 'manual'
                print('PASS expired registration, one member, stable provenance, existing-member no-op and removed-member rejection', flush=True)
                race = create('race')
                def redeem(email):
                    try: call(service, 'ticketremote_redeem_invitation', TICKET, race, email); return True
                    except urllib.error.HTTPError as error:
                        assert 'invitation_invalid' in error.read().decode(); return False
                with concurrent.futures.ThreadPoolExecutor(2) as pool:
                    assert sorted(pool.map(redeem, ['race-one@example.test','race-two@example.test'])) == [False,True]
                call(service, 'ticketremote_redeem_invitation', TICKET, digest, 'registered@example.test')
                rejects(lambda: call(other, 'ticketremote_member_check_in', TICKET, 'check2', 'check1', 'towards_riga', 3), 'invitation_session_changed')
                call(service, 'ticketremote_revoke_invitation', TICKET, 'trial', 'owner@example.test')
                assert next(v for v in sql(service, 'ticketremote_service_member_account') if v['email'] == 'registered@example.test')['active']
                revoked = create('revoked'); call(service, 'ticketremote_revoke_invitation', TICKET, 'revoked', 'owner@example.test')
                rejects(lambda: call(service, 'ticketremote_redeem_invitation', TICKET, revoked, 'never@example.test'), 'invitation_invalid')
                print('PASS concurrent redemption, guest invalidation, revocation and retained registered membership', flush=True)
                before_restart = row('trial')
                server.terminate(); server.wait(timeout=10)
                server = subprocess.Popen(['spacetime', 'start', '--listen-addr', f'127.0.0.1:{port}', '--data-dir', str(directory / 'data'), '--non-interactive'], stdout=log, stderr=log)
                for _ in range(100):
                    try: urllib.request.urlopen(url + '/v1/ping', timeout=1).close(); break
                    except OSError: time.sleep(.1)
                assert row('trial') == before_restart
                assert row('code-trial')['controlCodesUsed'] == 5
                rejects(lambda: call(guest, 'fixture_command', 'after-restart', 'open_latest_unactivated'), 'invitation_registration_required')
                print('PASS durable usage, redemption, revocation and guest fencing survive database restart', flush=True)
            finally:
                server.terminate()
                try: server.wait(timeout=10)
                except subprocess.TimeoutExpired: server.kill(); server.wait()

if __name__ == '__main__': main()
