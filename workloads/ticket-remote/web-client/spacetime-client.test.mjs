import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import { build } from 'esbuild';

test('real client keeps viewing samples and heartbeat failures on their original connection', async () => {
  const bundle = await build({ entryPoints: [new URL('./src/index.ts', import.meta.url).pathname],
    bundle: true, write: false, format: 'iife', platform: 'browser', plugins: [{
      name: 'local-connection-fixture', setup(builder) {
        builder.onLoad({ filter: /src\/generated\/index.ts$/ }, () => ({
          contents: 'export const DbConnection = { builder: () => globalThis.fixtureBuilder() };', loader: 'js' }));
        builder.onLoad({ filter: /src\/csp-safe-codecs.ts$/ }, () => ({
          contents: 'export function installCspSafeSpacetimeCodecs() {}', loader: 'js' }));
      }
    }] });
  const connections = [], statuses = [], calls = [];
  const context = vm.createContext({ URL, setTimeout, clearTimeout, performance, console,
    fixtureBuilder() {
      const handlers = {}, subscription = {};
      const connection = { db: {}, handlers, subscription, disconnect() {},
        reducers: {
          ticketremoteMemberRecordActivityTick: async args => { calls.push(['activity', connection, args]); },
          ticketremoteMemberSetStreamFocus: args => {
            calls.push(['heartbeat', connection, args]);
            return new Promise((_, reject) => { connection.rejectHeartbeat = reject; });
          }
        },
        subscriptionBuilder() {
          const builder = { onError(fn) { subscription.error=fn;return builder; },
            onApplied(fn) { subscription.applied=fn;return builder; },
            subscribe() { return { unsubscribe() {} }; } };
          return builder;
        }
      };
      const builder = { withUri() { return builder; }, withDatabaseName() { return builder; }, withToken() { return builder; },
        onConnect(fn) { handlers.connect=fn;return builder; }, onDisconnect(fn) { handlers.disconnect=fn;return builder; },
        onConnectError(fn) { handlers.error=fn;return builder; }, build() { connections.push(connection);return connection; } };
      return builder;
    }
  });
  context.window=context;
  vm.runInContext(bundle.outputFiles[0].text, context);
  const client=context.TicketSpacetime.create({ host:'https://fixture.invalid',database:'fixture',ticketId:'fixture',
    email:'fixture@example.test',sessionId:'fixture',accountScopeId:'a'.repeat(64),automaticReconnect:false },
    { onStatus: (...args) => statuses.push(args) });
  client.connect();
  await assert.rejects(client.recordActivityTick(), /not ready/);
  const first=connections[0];first.handlers.connect(first);await Promise.resolve();
  assert.equal(calls.length,0,'a rejected sample must not wait for the connection');
  await client.recordActivityTick();client.heartbeat();
  assert.equal(calls.filter(([kind])=>kind==='activity').length,1);
  client.connect();const second=connections[1];second.handlers.connect(second);await Promise.resolve();
  client.heartbeat();
  assert.equal(calls.filter(([kind])=>kind==='heartbeat').length,2,'new connection refreshes focus immediately');
  first.rejectHeartbeat(Error('old failure'));await Promise.resolve();await Promise.resolve();
  assert.equal(statuses.filter(([status])=>status==='heartbeat_failed').length,0);
  first.subscription.error({event:Error('obsolete query')});
  assert.equal(statuses.filter(([status])=>status==='subscription_failed').length,0);
  second.subscription.error({event:Error('invalid query')});
  assert.deepEqual(statuses.at(-1),['subscription_failed','Error: invalid query']);
  second.rejectHeartbeat(Error('current failure'));await Promise.resolve();await Promise.resolve();
  assert.equal(statuses.at(-1)[0],'heartbeat_failed');
  client.close();
  await assert.rejects(client.recordActivityTick(), /not ready/);
});
