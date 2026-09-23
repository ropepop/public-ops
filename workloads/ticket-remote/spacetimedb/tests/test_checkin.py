#!/usr/bin/env python3
"""Real database transactions, views and timers; disposable loopback service only."""
import concurrent.futures
import json
import os
from pathlib import Path
import runpy
import shutil
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request

SOURCE = Path(__file__).resolve().parents[1]
HELPERS = runpy.run_path(str(SOURCE.parent / 'scripts/test-action-statistics.py'))
PRIVATE = ('stream_desired_state', 'stream_viewer_focus', 'phone_current_report', 'relay_current_report')
FIXTURE = r'''
use checkin::{ticketremote_train_checkin, ticketremote_train_checkin_account};
#[spacetimedb::reducer]
pub fn fixture_member(ctx: &ReducerContext, email: String, role: String, active: bool) {
    let clock = now(ctx);
    ensure_ticket(ctx, "fixture", "Fixture", &clock);
    upsert_row!(ctx, ticketremote_ticket_member, TicketremoteTicketMember {
        id: member_id("fixture", &email), ticketId: "fixture".into(), email: email.clone(),
        role, active, createdAt: clock.clone(), updatedAt: clock.clone() });
    upsert_member_identity(ctx, "fixture", &email, &clock);
}
#[spacetimedb::reducer]
pub fn fixture_seed(ctx: &ReducerContext) {
    let clock = now(ctx);
    upsert_stream_viewer_focus(ctx, "fixture", "pixel", "private-session", "private@example.test", true, &clock);
    upsert_stream_desired_state(ctx, "fixture", "pixel", true, 7, "fixture", "rev", "fixture", &clock);
    upsert_relay_current_report(ctx, "fixture", "pixel", 7, "live", "0", "0", "{}", &clock);
}
#[spacetimedb::reducer]
pub fn fixture_age(ctx: &ReducerContext, email: String, ageMs: i64) {
    let clock = ctx.timestamp.to_micros_since_unix_epoch() / 1000;
    let mut row = ctx.db.ticketremote_train_checkin().id().find(member_id("fixture", &email)).unwrap();
    row.checkedInAtMs = clock - ageMs;
    row.activeUntilMs = row.checkedInAtMs + 40 * 60_000;
    row.historyUntilMs = row.checkedInAtMs + 120 * 60_000;
    ctx.db.ticketremote_train_checkin().id().update(row);
    // Invoke the real expiry routine; its next boundary uses the real scheduler.
    checkin::expire(ctx, "fixture", &email);
}
#[spacetimedb::reducer]
pub fn fixture_notice_due(ctx: &ReducerContext, email: String) {
    let mut row = ctx.db.ticketremote_train_checkin_account().id().find(member_id("fixture", &email)).unwrap();
    row.noticeUntilMs = ctx.timestamp.to_micros_since_unix_epoch() / 1000;
    ctx.db.ticketremote_train_checkin_account().id().update(row);
}
#[spacetimedb::reducer]
pub fn fixture_age_assert(ctx: &ReducerContext, email: String, ageMs: i64, expectedStatus: String) {
    fixture_age(ctx, email.clone(), ageMs);
    // Assert in this transaction: a one-millisecond boundary may pass before an HTTP read.
    let row = ctx.db.ticketremote_train_checkin().id().find(member_id("fixture", &email));
    assert_eq!(row.map(|v| v.status).unwrap_or_default(), expectedStatus);
}
#[spacetimedb::reducer]
pub fn fixture_notice_boundary(ctx: &ReducerContext, email: String, offsetMs: i64) {
    let id = member_id("fixture", &email);
    let clock = ctx.timestamp.to_micros_since_unix_epoch() / 1000;
    let mut row = ctx.db.ticketremote_train_checkin_account().id().find(&id).unwrap();
    row.noticePageId = "previous".into();
    row.noticeUntilMs = clock + offsetMs;
    ctx.db.ticketremote_train_checkin_account().id().update(row);
    checkin::ticketremote_member_claim_checkin_notice(ctx, "fixture".into(), "boundary".into()).unwrap();
    let row = ctx.db.ticketremote_train_checkin_account().id().find(&id).unwrap();
    assert_eq!(row.noticePageId, if offsetMs > 0 { "previous" } else { "boundary" });
    assert_eq!(row.noticeUntilMs, clock + if offsetMs > 0 { offsetMs } else { 120 * 60_000 });
}
#[spacetimedb::reducer]
pub fn fixture_limit_policy(ctx: &ReducerContext, bypass: bool) {
    let email = client_email_from_auth(ctx, "fixture").unwrap();
    let clock = now(ctx);
    assert_eq!(member_limit_effective_config(ctx, "fixture", &email), (!bypass, bypass, !bypass));
    for kind in ["registration", "control_code"] {
        for sequence in 0..3 {
            let request = format!("limits-{bypass}-{kind}-{sequence}");
            let expected = bypass || sequence < if kind == "registration" { 1 } else { 2 };
            for _ in 0..2 {
                let result = admit_member_limit_event(ctx, "fixture", &email, kind, &request, &clock).unwrap();
                assert_eq!(result.allowed, expected);
            }
        }
        let events: Vec<_> = ctx.db.ticketremote_member_limit_event().ticketEmailKindAt()
            .filter((&"fixture".to_string(), &email, kind)).filter(|row| row.counted == !bypass).collect();
        assert_eq!(events.len(), if bypass { 3 } else if kind == "registration" { 1 } else { 2 });
    }
}
#[spacetimedb::reducer]
pub fn fixture_no_phone_actions(ctx: &ReducerContext) {
    assert_eq!(ctx.db.ticketremote_stream_command().iter().count(), 0);
    assert_eq!(ctx.db.ticketremote_ticket_action_v3().iter().count(), 0);
    assert_eq!(ctx.db.ticketremote_control_code_request().iter().count(), 0);
    assert_eq!(ctx.db.ticketremote_ticket_action_v3_queued_intent().iter().count(), 0);
}
'''

def main():
    with tempfile.TemporaryDirectory(prefix='ticket-checkin-test-') as temp:
        directory = Path(temp)
        module = directory / 'module'
        shutil.copytree(SOURCE / 'src', module / 'src')
        for name in ('Cargo.toml', 'Cargo.lock'):
            shutil.copy2(SOURCE / name, module / name)
        source = (module / 'src/lib.rs').read_text()
        source = HELPERS['replace_function'](source, 'client_email_from_auth', '''
            let binding = ctx.db.ticketremote_member_identity().byIdentity().filter(&ctx.sender()).next().ok_or("auth required")?;
            if binding.ticketId != ticket_id || !is_member(ctx, ticket_id, &binding.email) { return Err("membership_required".into()); }
            Ok(binding.email)
        ''')
        source = HELPERS['replace_function'](source, 'identity_connected', '\n let _ = ctx; Ok(())\n') + FIXTURE
        env = {**os.environ, 'CARGO_TARGET_DIR': str(SOURCE / 'target/checkin-fixture')}
        def build(body, name):
            (module / 'src/lib.rs').write_text(body)
            result = subprocess.run(['spacetime', 'build', '--module-path', str(module)], env=env, cwd=directory, capture_output=True, text=True)
            if result.returncode: raise RuntimeError(result.stdout + result.stderr)
            output = directory / name
            shutil.copy2(Path(env['CARGO_TARGET_DIR']) / 'wasm32-unknown-unknown/release/ticket_remote_spacetimedb.wasm', output)
            return output
        current = build(source, 'private.wasm')
        public_source = source
        for table in PRIVATE:
            public_source = public_source.replace(f'accessor = ticketremote_{table},', f'accessor = ticketremote_{table}, public,').replace(f'accessor = ticketremote_{table})', f'accessor = ticketremote_{table}, public)')
        before = build(public_source, 'compatibility.wasm')
        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0))
            port = probe.getsockname()[1]
        url = f'http://127.0.0.1:{port}'
        with (directory / 'server.log').open('w') as log:
            server = subprocess.Popen(['spacetime', 'start', '--listen-addr', f'127.0.0.1:{port}', '--data-dir', str(directory / 'data'), '--non-interactive'], stdout=log, stderr=log)
            try:
                for _ in range(100):
                    try:
                        urllib.request.urlopen(url + '/v1/ping', timeout=1).close(); break
                    except OSError: time.sleep(.1)
                args = ['--server', url, '--no-config', '--yes']
                def publish(wasm):
                    HELPERS['run'](['spacetime', 'publish', *args, '--bin-path', str(wasm), '--delete-data=never', 'checkin-fixture'], cwd=directory)
                def http(path, data=b'', token=None):
                    headers = {'Content-Type': 'application/json'}
                    if token: headers['Authorization'] = 'Bearer ' + token
                    with urllib.request.urlopen(urllib.request.Request(url + path, data=data, headers=headers), timeout=15) as response:
                        body = response.read()
                        return json.loads(body) if body else None
                def identity(): return http('/v1/identity')['token']
                def call(token, name, *values):
                    return http('/v1/database/checkin-fixture/call/' + name, json.dumps(values).encode(), token)
                def sql(token, table):
                    return http('/v1/database/checkin-fixture/sql', ('SELECT * FROM ' + table).encode(), token)[0]['rows']
                def rejects(action, reason=None):
                    try: action()
                    except urllib.error.HTTPError as error:
                        if reason: assert reason in error.read().decode()
                        return
                    raise AssertionError('unexpected authorized operation')
                def own(token): return sql(token, 'ticketremote_member_checkin')[0]
                def wave(*actions):
                    start = threading.Barrier(len(actions))
                    def run(action):
                        start.wait(timeout=5)
                        return action()
                    with concurrent.futures.ThreadPoolExecutor(len(actions)) as pool:
                        return list(pool.map(run, actions))
                publish(before)
                alice, bob, admin, outsider = [identity() for _ in range(4)]
                for token, email, role in [(alice, 'alice@example.test', 'member'), (bob, 'bob@example.test', 'member'), (admin, 'admin@example.test', 'admin')]:
                    call(token, 'fixture_member', email, role, True)
                call(admin, 'fixture_seed')
                publish(current)
                for token in [alice, bob, outsider]:
                    for table in PRIVATE + ('train_checkin', 'train_checkin_account'):
                        rejects(lambda: sql(token, 'ticketremote_' + table))
                    assert sql(token, 'ticketremote_privileged_viewers') == []
                    assert sql(token, 'ticketremote_service_stream_desired_state') == []
                    assert sql(token, 'ticketremote_privileged_relay_report') == []
                    assert sql(token, 'ticketremote_service_phone_current_report') == []
                assert len(sql(admin, 'ticketremote_privileged_viewers')) == 1
                assert sql(admin, 'ticketremote_service_stream_desired_state')[0][4] == 7
                for table in ('train_checkin', 'train_checkin_account'):
                    rejects(lambda: sql(admin, 'ticketremote_' + table))
                assert len(sql(alice, 'ticketremote_member_stream_state')[0]) == 5
                assert sql(outsider, 'ticketremote_member_checkin_groups') == []
                print('PASS data-preserving privacy cutover and role-filtered views', flush=True)
                checkin = 'ticketremote_member_check_in'
                claim = 'ticketremote_member_claim_checkin_notice'
                call(alice, checkin, 'fixture', 'a1', '', 'towards_riga', 2)
                first = own(alice)
                call(alice, checkin, 'fixture', 'a1', '', 'towards_riga', 2)
                assert own(alice) == first
                call(alice, claim, 'fixture', 'own-only')
                assert own(alice)[7] == ''
                call(bob, claim, 'fixture', 'bob-page')
                assert own(bob)[7] == 'bob-page'
                call(bob, claim, 'fixture', 'bob-device-two')
                assert own(bob)[7] == 'bob-page'
                rejects(lambda: call(outsider, checkin, 'fixture', 'bad', '', 'towards_riga', 1))
                for direction, carriage in [('towards_riga', 0), ('towards_riga', 5), ('invalid', 1)]:
                    rejects(lambda: call(alice, checkin, 'fixture', 'bad', 'a1', direction, carriage), 'checkin_invalid_selection')
                for request in ('', 'x' * 81, 'contains space', 'contains@sign'):
                    rejects(lambda: call(alice, checkin, 'fixture', request, 'a1', 'towards_riga', 1), 'checkin_invalid_request')
                    rejects(lambda: call(alice, claim, 'fixture', request), 'checkin_invalid_request')
                assert own(alice) == first
                print('PASS validation, safe retry, own-only notice exclusion and account cooldown', flush=True)
                call(alice, checkin, 'fixture', 'a2', 'a1', 'away_from_riga', 4)
                rejects(lambda: call(alice, checkin, 'fixture', 'a1', '', 'towards_riga', 2))
                assert len(sql(bob, 'ticketremote_member_checkin_groups')) == 1
                call(alice, 'ticketremote_member_check_out', 'fixture', 'a2')
                call(alice, 'ticketremote_member_check_out', 'fixture', 'a2')
                assert own(alice)[6] == 'checked_out'
                call(admin, claim, 'fixture', 'no-active')
                assert own(admin)[7] == ''
                print('PASS replacement, stale delivery rejection and early check-out history', flush=True)
                call(alice, checkin, 'fixture', 'a3', 'a2', 'towards_riga', 1)
                call(admin, 'fixture_age', 'alice@example.test', 40 * 60000 - 1500)
                deadline = time.monotonic() + 8
                while own(alice)[6] != 'expired':
                    assert time.monotonic() < deadline, '40-minute timer failed'
                    time.sleep(.15)
                assert len(sql(bob, 'ticketremote_member_checkin_groups')) == 1
                call(admin, 'fixture_age', 'alice@example.test', 120 * 60000 - 1500)
                deadline = time.monotonic() + 8
                while sql(bob, 'ticketremote_member_checkin_groups'):
                    assert time.monotonic() < deadline, '120-minute timer failed'
                    time.sleep(.15)
                assert own(alice)[1] == 'a3' and own(alice)[6] == ''
                call(alice, checkin, 'fixture', 'a3', 'a2', 'towards_riga', 1)
                assert sql(bob, 'ticketremote_member_checkin_groups') == []
                print('PASS scheduled expiry and purge without page activity; expired retries stay inert', flush=True)
                call(alice, checkin, 'fixture', 'a4', 'a3', 'towards_riga', 3)
                call(admin, 'fixture_notice_due', 'bob@example.test')
                with concurrent.futures.ThreadPoolExecutor(2) as pool:
                    list(pool.map(lambda page: call(bob, claim, 'fixture', page), ['concurrent-one', 'concurrent-two']))
                assert own(bob)[7] in ('concurrent-one', 'concurrent-two')
                winner = own(bob)[7]
                call(bob, claim, 'fixture', 'late')
                assert own(bob)[7] == winner
                print('PASS concurrent notice claim and account-wide cooldown', flush=True)
                wave(
                    lambda: call(alice, checkin, 'fixture', 'a5', 'a4', 'away_from_riga', 4),
                    lambda: call(bob, checkin, 'fixture', 'b1', '', 'towards_riga', 2),
                    lambda: call(admin, checkin, 'fixture', 'c1', '', 'towards_riga', 2))
                groups = sql(alice, 'ticketremote_member_checkin_groups')
                assert {(row[1], row[2], row[3], row[4]) for row in groups} == {
                    ('away_from_riga', 4, 'active', 1), ('towards_riga', 2, 'active', 2)}
                for token, revision in [(alice, 'a5'), (bob, 'b1'), (admin, 'c1')]:
                    assert own(token)[0:2] == ['self', revision]
                    assert sorted(sql(token, 'ticketremote_member_checkin_groups')) == sorted(groups)
                    assert '@' not in json.dumps(sql(token, 'ticketremote_member_checkin') + groups)
                wave(
                    lambda: call(alice, checkin, 'fixture', 'a5', 'a4', 'away_from_riga', 4),
                    lambda: call(bob, checkin, 'fixture', 'b2', 'b1', 'away_from_riga', 3),
                    lambda: call(admin, 'ticketremote_member_check_out', 'fixture', 'c1'))
                assert {(row[1], row[2], row[3], row[4]) for row in sql(bob, 'ticketremote_member_checkin_groups')} == {
                    ('away_from_riga', 4, 'active', 1), ('away_from_riga', 3, 'active', 1),
                    ('towards_riga', 2, 'checked_out', 1)}
                print('PASS three synchronized identities: submit, replace, replay, checkout and identical anonymous totals', flush=True)
                def competing_replace(request):
                    try:
                        call(alice, checkin, 'fixture', request, 'a5', 'towards_riga', 1)
                        return True
                    except urllib.error.HTTPError as error:
                        assert 'checkin_changed' in error.read().decode()
                        return False
                assert sorted(wave(lambda: competing_replace('a6-one'), lambda: competing_replace('a6-two'))) == [False, True]
                revision = own(alice)[1]
                rejects(lambda: call(alice, 'ticketremote_member_check_out', 'fixture', 'a5'))
                for direction in ('towards_riga', 'away_from_riga'):
                    for carriage in range(1, 5):
                        request = f'choice-{direction}-{carriage}'
                        call(alice, checkin, 'fixture', request, revision, direction, carriage)
                        assert own(alice)[1:4] == [request, direction, carriage]
                        revision = request
                print('PASS all eight direction/carriage choices and malformed input rejection', flush=True)
                for age, expected in [(40 * 60000 - 1, 'active'), (40 * 60000, 'expired'),
                        (40 * 60000 + 1, 'expired'), (120 * 60000 - 1, 'expired'),
                        (120 * 60000, ''), (120 * 60000 + 1, '')]:
                    request = f'boundary-{age}'
                    call(alice, checkin, 'fixture', request, revision, 'towards_riga', 1)
                    call(admin, 'fixture_age_assert', 'alice@example.test', age, expected)
                    revision = request
                for offset in (1, 0, -1):
                    call(admin, 'fixture_notice_boundary', 'admin@example.test', offset)
                print('PASS competing revisions and exact before/at/after 40-minute expiry and 120-minute history/notice boundaries', flush=True)
                for token, email, role in [(alice, 'alice@example.test', 'owner'), (bob, 'bob@example.test', 'admin'), (admin, 'admin@example.test', 'admin')]:
                    call(token, 'fixture_member', email, role, True)
                    call(token, 'ticketremote_member_set_limit_preference', 'fixture', False)
                    call(token, 'fixture_limit_policy', True)
                for token, email in [(bob, 'bob@example.test'), (admin, 'admin@example.test')]:
                    call(token, 'fixture_member', email, 'member', True)
                    rejects(lambda: call(token, 'ticketremote_member_set_limit_preference', 'fixture', False))
                    call(token, 'fixture_limit_policy', False)
                print('PASS three-account quota bypass, duplicate admission and enforced quotas after demotion', flush=True)
                for role in ('owner', 'admin'):
                    call(admin, 'fixture_member', 'admin@example.test', role, True)
                    assert len(sql(admin, 'ticketremote_privileged_viewers')) == 1
                    assert sql(admin, 'ticketremote_service_stream_desired_state')[0][4] == 7
                call(admin, 'fixture_member', 'admin@example.test', 'member', True)
                for table in ('privileged_viewers', 'service_stream_desired_state', 'privileged_relay_report', 'service_phone_current_report'):
                    assert sql(admin, 'ticketremote_' + table) == []
                call(admin, checkin, 'fixture', 'c2', 'c1', 'towards_riga', 4)
                assert own(admin)[1] == 'c2'
                call(alice, 'fixture_member', 'alice@example.test', 'member', False)
                assert sql(alice, 'ticketremote_member_checkin') == []
                assert sql(alice, 'ticketremote_member_checkin_groups') == []
                rejects(lambda: call(alice, checkin, 'fixture', 'revoked', revision, 'towards_riga', 1))
                call(bob, 'fixture_member', 'bob@example.test', 'member', False)
                assert {(row[1], row[2], row[3], row[4]) for row in sql(admin, 'ticketremote_member_checkin_groups')} == {
                    ('towards_riga', 4, 'active', 1)}
                call(admin, 'fixture_no_phone_actions')
                print('PASS owner/admin access, immediate demotion, ordinary check-in access and membership revocation', flush=True)
                print('PASS no check-in, notice, replacement or checkout created a phone command', flush=True)
            finally:
                server.terminate()
                try: server.wait(timeout=10)
                except subprocess.TimeoutExpired: server.kill(); server.wait()

if __name__ == '__main__': main()
