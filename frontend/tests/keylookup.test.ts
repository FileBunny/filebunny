import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as openpgp from 'openpgp';
import { readPublicKeys } from '../src/lib/crypto.ts';
import { checkKeyWithWkd, lookupKeys, wkdAvailable, type Deps } from '../src/lib/keylookup.ts';
import { testKey } from './helpers.ts';

const alice = '90E6 2DD2 7AF9 17D6 0D24 641B 896E 0850 A8FC 4BFF';
const aliceKey = testKey('alice.pub.asc');
const bobKey = testKey('bob.pub.asc');
const api = 'https://filebunny.test';

type Route = (url: string, init: RequestInit) => Response | Promise<Response>;

/** A fetch that records every call and answers per host. */
function mockFetch(routes: { keys?: Route; wkd?: Route; features?: Route }) {
  const calls: { url: string; init: RequestInit }[] = [];
  const fetch = (async (input: string | URL | Request, init: RequestInit = {}) => {
    const url = String(input);
    calls.push({ url, init });
    const route = url.startsWith('https://keys.openpgp.org/')
      ? routes.keys
      : url === `${api}/api/v1/wkd`
        ? routes.wkd
        : url === `${api}/api/v1/features`
          ? routes.features
          : undefined;
    if (!route) throw new Error(`unexpected request to ${url}`);
    return route(url, init);
  }) as typeof globalThis.fetch;
  return { calls, deps: { fetch, apiBase: api } satisfies Deps };
}

const ok = (body: string | Uint8Array) => () => new Response(body as BodyInit, { status: 200 });
const status = (code: number) => () => new Response(code === 404 ? 'Not found' : 'error', { status: code });

async function otherKey(email: string, opts: Partial<openpgp.GenerateKeyOptions> = {}): Promise<string> {
  const { publicKey } = await openpgp.generateKey({
    userIDs: [{ name: 'Someone Else', email }],
    type: 'curve25519',
    format: 'armored',
    ...opts,
  });
  return publicKey;
}

test('keys.openpgp.org: found, with safe fetch options', async () => {
  const { calls, deps } = mockFetch({ keys: ok(aliceKey) });
  const r = await lookupKeys(' alice@example.org ', { wkd: false }, deps);
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, 'https://keys.openpgp.org/vks/v1/by-email/alice%40example.org');
  assert.equal(calls[0].init.credentials, 'omit');
  assert.equal(calls[0].init.referrerPolicy, 'no-referrer');
  assert.equal(calls[0].init.cache, 'no-store');
  assert.equal(calls[0].init.redirect, 'error');
  assert.ok(calls[0].init.signal instanceof AbortSignal);
  assert.equal(r.results[0].status, 'found');
  assert.equal(r.candidates.length, 1);
  assert.equal(r.candidates[0].info.fingerprint, alice);
  assert.deepEqual(r.candidates[0].sources, ['keys.openpgp.org']);
  assert.equal(r.conflict, false);
});

test('keys.openpgp.org: not found', async () => {
  const { deps } = mockFetch({ keys: status(404) });
  const r = await lookupKeys('nobody@example.org', { wkd: false }, deps);
  assert.equal(r.results[0].status, 'not-found');
  assert.deepEqual(r.candidates, []);
});

test('keys.openpgp.org: network error and server error fail without throwing', async () => {
  for (const keys of [
    () => Promise.reject(new TypeError('Failed to fetch')),
    status(500),
  ]) {
    const { deps } = mockFetch({ keys });
    const r = await lookupKeys('alice@example.org', { wkd: false }, deps);
    assert.equal(r.results[0].status, 'failed');
    assert.ok(r.results[0].message);
  }
});

test('timeout aborts the request', async () => {
  const { deps } = mockFetch({
    keys: (_u, init) =>
      new Promise((_resolve, reject) => init.signal!.addEventListener('abort', () => reject(init.signal!.reason))),
  });
  const r = await lookupKeys('alice@example.org', { wkd: false }, { ...deps, timeoutMs: 50 });
  assert.equal(r.results[0].status, 'failed');
  assert.equal(r.results[0].message, 'timed out');
});

test('oversized response is cut off', async () => {
  let pulled = 0;
  const endless = new ReadableStream<Uint8Array>({
    pull(c) {
      pulled++;
      c.enqueue(new Uint8Array(4096));
    },
  });
  const { deps } = mockFetch({ keys: () => new Response(endless, { status: 200 }) });
  const r = await lookupKeys('alice@example.org', { wkd: false }, { ...deps, maxBytes: 10_000 });
  assert.equal(r.results[0].status, 'failed');
  assert.equal(r.results[0].message, 'the response is too large');
  assert.ok(pulled < 10, `read ${pulled} chunks`);

  const big = mockFetch({
    keys: () => new Response('x', { status: 200, headers: { 'Content-Length': String(2 << 20) } }),
  });
  const r2 = await lookupKeys('alice@example.org', { wkd: false }, big.deps);
  assert.equal(r2.results[0].message, 'the response is too large');
});

test('garbage and private keys are not accepted', async () => {
  for (const body of ['<html>hello</html>', testKey('alice.sec.asc')]) {
    const { deps } = mockFetch({ keys: ok(body) });
    const r = await lookupKeys('alice@example.org', { wkd: false }, deps);
    assert.equal(r.results[0].status, 'failed');
    assert.deepEqual(r.candidates, []);
  }
});

test('keys without a user ID for exactly this address are dropped', async () => {
  const { deps } = mockFetch({ keys: ok(aliceKey) });
  for (const email of ['bob@example.org', 'alice@example.org.evil', 'xalice@example.org']) {
    const r = await lookupKeys(email, { wkd: false }, deps);
    assert.equal(r.results[0].status, 'not-found');
    assert.deepEqual(r.results[0].dropped, [{ fingerprint: alice, reason: 'no valid user ID with this address' }]);
    assert.deepEqual(r.candidates, []);
  }
  // Case does not matter.
  const r = await lookupKeys('Alice@Example.ORG', { wkd: false }, deps);
  assert.equal(r.candidates.length, 1);
});

test('expired and revoked keys are dropped', async () => {
  const expired = await otherKey('carol@example.org', { keyExpirationTime: 1, date: new Date(Date.now() - 60_000) });
  const { privateKey } = await openpgp.generateKey({
    userIDs: [{ email: 'carol@example.org' }],
    type: 'curve25519',
    format: 'object',
  });
  const { publicKey: revoked } = await openpgp.revokeKey({ key: privateKey, format: 'armored' });
  for (const key of [expired, revoked]) {
    const { deps } = mockFetch({ keys: ok(key) });
    const r = await lookupKeys('carol@example.org', { wkd: false }, deps);
    assert.equal(r.results[0].status, 'not-found');
    assert.equal(r.results[0].dropped.length, 1);
    assert.deepEqual(r.candidates, []);
  }
});

test('several keys from one source: conflict, nothing chosen', async () => {
  const second = await otherKey('alice@example.org');
  const { deps } = mockFetch({ keys: ok(aliceKey + '\n' + second) });
  const r = await lookupKeys('alice@example.org', { wkd: false }, deps);
  assert.equal(r.candidates.length, 2);
  assert.equal(r.conflict, true);

  // The same, as one armored block (how keyservers usually send it).
  const both = openpgp.armor(
    openpgp.enums.armor.publicKey,
    new Uint8Array([
      ...(await readPublicKeys(aliceKey))[0].key.write(),
      ...(await readPublicKeys(second))[0].key.write(),
    ]),
  );
  const one = mockFetch({ keys: ok(both) });
  assert.equal((await lookupKeys('alice@example.org', { wkd: false }, one.deps)).candidates.length, 2);
});

test('WKD only when asked, by POST with the address in the body', async () => {
  const off = mockFetch({ keys: ok(aliceKey) });
  await lookupKeys('alice@example.org', { wkd: false }, off.deps);
  assert.ok(off.calls.every((c) => !c.url.includes('/api/v1/wkd')));

  const binary = (await readPublicKeys(aliceKey))[0].key.write();
  const on = mockFetch({ keys: ok(aliceKey), wkd: ok(binary) });
  const r = await lookupKeys('alice@example.org', { wkd: true }, on.deps);
  const call = on.calls.find((c) => c.url.endsWith('/api/v1/wkd'))!;
  assert.equal(call.init.method, 'POST');
  assert.equal(call.init.body, JSON.stringify({ email: 'alice@example.org' }));
  assert.equal(call.init.credentials, 'omit');
  // Both sources agree: one candidate, both sources, no conflict.
  assert.equal(r.candidates.length, 1);
  assert.deepEqual(r.candidates[0].sources, ['keys.openpgp.org', 'wkd']);
  assert.equal(r.conflict, false);
});

test('sources disagree: conflict with each key and its source', async () => {
  const mallory = await otherKey('alice@example.org');
  const { deps } = mockFetch({ keys: ok(mallory), wkd: ok(aliceKey) });
  const r = await lookupKeys('alice@example.org', { wkd: true }, deps);
  assert.equal(r.conflict, true);
  assert.equal(r.candidates.length, 2);
  const fromWkd = r.candidates.find((c) => c.sources.includes('wkd'))!;
  assert.equal(fromWkd.info.fingerprint, alice);
  assert.deepEqual(fromWkd.sources, ['wkd']);
});

test('WKD errors: not found, invalid, busy, upstream', async () => {
  const cases: [number, string][] = [
    [404, 'not-found'],
    [400, 'failed'],
    [502, 'failed'],
    [503, 'failed'],
  ];
  for (const [code, want] of cases) {
    const { deps } = mockFetch({ keys: status(404), wkd: status(code) });
    const r = await lookupKeys('alice@example.org', { wkd: true }, deps);
    assert.equal(r.results[1].source, 'wkd');
    assert.equal(r.results[1].status, want, `status ${code}`);
  }
});

test('invalid input is refused before any request', async () => {
  const { calls, deps } = mockFetch({});
  for (const email of ['', 'alice', 'alice@', 'a b@example.org', 'alice@example']) {
    await assert.rejects(lookupKeys(email, { wkd: true }, deps));
  }
  assert.equal(calls.length, 0);
});

test('checkKeyWithWkd: match, different, none, failed', async () => {
  const [info] = await readPublicKeys(bobKey);
  const mallory = await otherKey('bob@example.org');
  const cases: [Route, string][] = [
    [ok(bobKey), 'match'],
    [ok(mallory), 'different'],
    [status(404), 'none'],
    [status(502), 'failed'],
    [() => Promise.reject(new TypeError('Failed to fetch')), 'failed'],
    [ok('not a key'), 'failed'],
  ];
  for (const [wkd, want] of cases) {
    const { calls, deps } = mockFetch({ wkd });
    const checks = await checkKeyWithWkd(info, deps);
    assert.equal(checks.length, 1);
    assert.equal(checks[0].email, 'bob@example.org');
    assert.equal(checks[0].status, want);
    assert.equal(calls[0].init.body, JSON.stringify({ email: 'bob@example.org' }));
  }
});

test('checkKeyWithWkd checks every address on the key', async () => {
  const { privateKey } = await openpgp.generateKey({
    userIDs: [{ email: 'one@example.org' }, { email: 'two@example.net' }],
    type: 'curve25519',
    format: 'object',
  });
  const [info] = await readPublicKeys(privateKey.toPublic().armor());
  const { calls, deps } = mockFetch({ wkd: status(404) });
  const checks = await checkKeyWithWkd(info, deps);
  assert.deepEqual(checks.map((c) => c.email).sort(), ['one@example.org', 'two@example.net']);
  assert.equal(calls.length, 2);
});

test('wkdAvailable follows the backend', async () => {
  assert.equal(await wkdAvailable(mockFetch({ features: ok('{"wkd":true}') }).deps), true);
  assert.equal(await wkdAvailable(mockFetch({ features: ok('{"wkd":false}') }).deps), false);
  assert.equal(await wkdAvailable(mockFetch({ features: status(404) }).deps), false);
  assert.equal(await wkdAvailable(mockFetch({ features: () => Promise.reject(new TypeError('x')) }).deps), false);
});
