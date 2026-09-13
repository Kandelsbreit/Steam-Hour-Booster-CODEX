'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { EventEmitter } = require('node:events');
const { Engine, parseIds } = require('../src/engine');
class Client extends EventEmitter {
  constructor() { super(); this.games = []; this.playingState = { blocked: false }; this.steamID = '76561198000000000'; }
  logOn(details) { this.details = details; }
  logOff() { this.off = true; this.emit('disconnected', 0); }
  gamesPlayed(ids, force) { assert.notEqual(force, true); this.games.push([...ids]); }
  getUserOwnedApps() { return Promise.resolve({ apps: [{ appid: 440, name: 'TF2', playtime_forever: 120 }] }); }
}
function fixture() {
  let now = 100000;
  const tokens = new Map(), clients = [];
  const store = { config: { version: 1, accounts: [] }, save() {}, hasToken: id => tokens.has(id),
    saveToken: (id, token) => tokens.set(id, token), getToken: id => tokens.get(id), forget: id => tokens.delete(id) };
  const engine = new Engine(store, () => { const c = new Client(); clients.push(c); return c; }, () => now);
  const id = engine.add('test_account');
  engine.update(id, { appids: Array.from({ length: 65 }, (_, i) => i + 1), batchSize: 32, rotationMinutes: 1, autoStart: true });
  const advance = ms => { now += ms; engine.tick(); };
  const login = () => { engine.start(id, 'LOCAL_TEST_PASSWORD'); const c = clients.at(-1); c.emit('refreshToken', 'LOCAL_TEST_TOKEN'); c.emit('loggedOn'); advance(5000); return c; };
  return { engine, store, tokens, clients, id, advance, login };
}
test('input rejects invalid appids and removes duplicate ids', () => {
  assert.deepEqual(parseIds('440, 570; 440\n730'), [440, 570, 730]);
  for (const value of ['-1', '1.5', '1e3', '4294967296', 'hello']) assert.throws(() => parseIds(value));
});
test('at most three unique accounts', () => {
  const f = fixture(); assert.throws(() => f.engine.add('TEST_ACCOUNT'));
  f.engine.add('second'); f.engine.add('third'); assert.throws(() => f.engine.add('fourth'));
});
test('rotation 32/32/1 wraps and never forces another session', () => {
  const f = fixture(), c = f.login(); assert.equal(c.games.at(-1).length, 32);
  for (let i = 0; i < 60; i++) f.advance(1000);
  assert.deepEqual(c.games.at(-1), Array.from({ length: 32 }, (_, i) => i + 33));
  f.engine.next(f.id); assert.deepEqual(c.games.at(-1), [65]);
  f.engine.next(f.id); assert.equal(c.games.at(-1)[0], 1);
  assert.equal(f.engine.runtime(f.id).activeMs, 60000);
});
test('remote game pauses, excludes paused time, resumes after cooldown', () => {
  const f = fixture(), c = f.login(); f.advance(1000);
  c.emit('playingState', true, 730); c.playingState.blocked = true;
  assert.deepEqual(c.games.at(-1), []);
  for (let i = 0; i < 10; i++) f.advance(1000);
  assert.equal(f.engine.runtime(f.id).activeMs, 1000);
  c.emit('playingState', false, 0); c.playingState.blocked = false;
  f.advance(14000); assert.deepEqual(c.games.at(-1), []);
  f.advance(1000); assert.equal(c.games.at(-1).length, 32);
});
test('already blocked on initial login never submits a game batch', () => {
  const f = fixture(); f.engine.start(f.id, 'password'); const c = f.clients[0];
  c.emit('loggedOn'); c.playingState.blocked = true; f.advance(5000);
  assert.equal(c.games.length, 0); assert.equal(f.engine.runtime(f.id).blocked, true);
});
test('refresh token persists; restarting uses token without password', () => {
  const f = fixture(); f.login(); f.engine.stop(f.id); f.engine.start(f.id);
  assert.equal(f.clients.at(-1).details.refreshToken, 'LOCAL_TEST_TOKEN');
  assert.equal(f.clients.at(-1).details.password, undefined);
  const exposed = JSON.stringify(f.engine.snapshot()) + JSON.stringify(f.store.config);
  assert.ok(!exposed.includes('LOCAL_TEST_TOKEN')); assert.ok(!exposed.includes('LOCAL_TEST_PASSWORD'));
});
test('temporary error retries with backoff, stop cancels, stale client cannot save token', () => {
  const f = fixture(), c = f.login(); c.emit('error', { eresult: 3 });
  f.advance(29000); assert.equal(f.clients.length, 1); f.advance(1000); assert.equal(f.clients.length, 2);
  f.clients[1].emit('error', { eresult: 3 }); f.advance(59000); assert.equal(f.clients.length, 2);
  f.engine.stop(f.id); f.advance(10000); assert.equal(f.clients.length, 2);
  c.emit('refreshToken', 'STALE'); assert.equal(f.tokens.get(f.id), 'LOCAL_TEST_TOKEN');
});
test('another-session error retries conservatively and still yields to a remote game', () => {
  for (const code of [6, 34, 50]) {
    const f = fixture(), c = f.login(); c.emit('error', { eresult: code }); f.advance(119000);
    assert.equal(f.clients.length, 1); assert.equal(f.engine.runtime(f.id).desired, true);
    f.advance(1000); assert.equal(f.clients.length, 2);
    const second = f.clients[1]; assert.equal(second.details.refreshToken, 'LOCAL_TEST_TOKEN');
    second.emit('loggedOn'); second.playingState.blocked = true; second.emit('playingState', true, 730);
    f.advance(5000); assert.deepEqual(second.games, []);
    second.playingState.blocked = false; second.emit('playingState', false, 0);
    f.advance(14000); assert.deepEqual(second.games, []); f.advance(1000); assert.equal(second.games.at(-1).length, 32);
  }
});

test('stop cancels conflict retry and repeated conflicts back off to fifteen minutes', () => {
  const f = fixture(); const c = f.login(); c.emit('disconnected', 34);
  f.advance(120000); f.clients.at(-1).emit('error', { eresult: 34 });
  f.advance(239000); assert.equal(f.clients.length, 2); f.advance(1000); assert.equal(f.clients.length, 3);
  f.engine.stop(f.id); f.advance(9999999); assert.equal(f.clients.length, 3);
});
test('rejected token never retries indefinitely and is not silently deleted', () => {
  const f = fixture(), c = f.login(); c.emit('error', { eresult: 5 }); f.advance(1000000);
  assert.equal(f.clients.length, 1); assert.ok(f.tokens.has(f.id)); assert.equal(f.engine.runtime(f.id).desired, false);
});
test('wrong guard code enforces 30 second wait and valid code remains off disk', () => {
  const f = fixture(); f.engine.start(f.id, 'password'); let received;
  f.clients[0].emit('steamGuard', null, code => { received = code; }, true);
  assert.throws(() => f.engine.guard(f.id, 'ABCDE')); f.advance(30000);
  f.engine.guard(f.id, 'abcde'); assert.equal(received, 'ABCDE');
  assert.ok(!JSON.stringify(f.store.config).includes('ABCDE'));
});
test('sleep gap not counted and independent accounts do not share clients or tokens', () => {
  const f = fixture(), c = f.login(); f.advance(1000); f.advance(3600000);
  assert.equal(f.engine.runtime(f.id).activeMs, 1000);
  const second = f.engine.add('second_account'); f.engine.start(second, 'secondpass');
  assert.notEqual(c, f.clients[1]); f.clients[1].emit('refreshToken', 'SECOND');
  f.engine.stop(second); assert.equal(f.engine.runtime(f.id).online, true);
  assert.equal(f.tokens.get(f.id), 'LOCAL_TEST_TOKEN'); assert.equal(f.tokens.get(second), 'SECOND');
});
test('remove during pending library request does not report into a deleted account', async () => {
  const f = fixture(); f.engine.start(f.id, 'password'); let reject;
  f.clients[0].getUserOwnedApps = () => new Promise((_, r) => { reject = r; });
  f.clients[0].emit('loggedOn'); f.engine.remove(f.id); reject(new Error('offline'));
  await new Promise(resolve => setImmediate(resolve)); assert.equal(f.store.config.accounts.length, 0);
});
