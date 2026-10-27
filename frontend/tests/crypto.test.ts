import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  decrypt,
  encrypt,
  formatFingerprint,
  messageInfo,
  readPrivateKey,
  readPublicKeys,
  safeFileName,
} from '../src/lib/crypto.ts';
import { collect, randomBytes, streamOf, testKey } from './helpers.ts';

const alice = '90E6 2DD2 7AF9 17D6 0D24 641B 896E 0850 A8FC 4BFF';

test('password: text round trip', async () => {
  const text = 'Hello, wörld 🐇\nline two\r\nline three\n';
  const ct = await collect(await encrypt({ kind: 'text', text }, { password: 'correct horse' }));
  assert.deepEqual(await messageInfo(ct), { password: true, key: false });
  assert.deepEqual(await decrypt(ct, { password: 'correct horse' }), { kind: 'text', text });
  await assert.rejects(decrypt(ct, { password: 'wrong' }));
});

test('password: streamed file round trip keeps the name', async () => {
  const data = randomBytes(3 * 1024 * 1024 + 17);
  const ct = await collect(
    await encrypt({ kind: 'file', name: 'report final.pdf', data: streamOf(data) }, { password: 'pw' }),
  );
  const out = await decrypt(ct, { password: 'pw' });
  assert.equal(out.kind, 'file');
  assert.ok(out.kind === 'file' && out.name === 'report final.pdf');
  assert.ok(out.kind === 'file' && Buffer.from(out.data).equals(Buffer.from(data)));
});

test('public keys: every recipient can decrypt, key IDs are hidden', async () => {
  const keys = [...(await readPublicKeys(testKey('alice.pub.asc'))), ...(await readPublicKeys(testKey('bob.pub.asc')))];
  const ct = await collect(
    await encrypt({ kind: 'text', text: 'for both' }, { publicKeys: keys.map((k) => k.key) }),
  );
  assert.deepEqual(await messageInfo(ct), { password: false, key: true });
  // Wildcard key IDs: nothing in the ciphertext names the recipients.
  const hex = Buffer.from(ct).toString('hex').toUpperCase();
  for (const k of keys) {
    for (const sub of k.key.getKeys()) assert.ok(!hex.includes(sub.getKeyID().toHex().toUpperCase()));
  }

  const alicePriv = await readPrivateKey(testKey('alice.sec.asc'), '');
  const bobPriv = await readPrivateKey(testKey('bob.sec.asc'), 'bob-test-passphrase');
  for (const priv of [alicePriv, bobPriv]) {
    assert.deepEqual(await decrypt(ct, { privateKeys: [priv] }), { kind: 'text', text: 'for both' });
  }
});

test('readPublicKeys shows fingerprint and refuses private keys', async () => {
  const [info] = await readPublicKeys(testKey('alice.pub.asc'));
  assert.equal(info?.fingerprint, alice);
  assert.equal(info?.canEncrypt, true);
  assert.match(info?.userIDs[0] ?? '', /alice@example\.org/);
  await assert.rejects(readPublicKeys(testKey('alice.sec.asc')), /private key/);
  await assert.rejects(readPublicKeys('not a key'));
});

test('key files are read as armored or binary bytes', async () => {
  const armored = new TextEncoder().encode(testKey('alice.sec.asc'));
  const key = await readPrivateKey(armored, '');
  assert.equal(formatFingerprint(key.getFingerprint()), alice);
  assert.ok(await readPrivateKey(key.write(), ''));
  const [info] = await readPublicKeys(new TextEncoder().encode(testKey('alice.pub.asc')));
  assert.equal(info?.fingerprint, alice);
  const [binary] = await readPublicKeys(info!.key.write());
  assert.equal(binary?.fingerprint, alice);
});

test('readPrivateKey needs the right passphrase', async () => {
  await assert.rejects(readPrivateKey(testKey('bob.sec.asc'), 'wrong'));
});

test('tampered ciphertext is rejected', async () => {
  const ct = await collect(await encrypt({ kind: 'text', text: 'integrity' }, { password: 'pw' }));
  const bad = ct.slice();
  bad[bad.length - 30]! ^= 0x01;
  await assert.rejects(decrypt(bad, { password: 'pw' }));
});

test('long file names are shortened to 255 bytes, keeping the extension', async () => {
  const name = 'ä'.repeat(200) + '.tar.gz';
  const ct = await collect(await encrypt({ kind: 'file', name, data: new Uint8Array([1]) }, { password: 'pw' }));
  const out = await decrypt(ct, { password: 'pw' });
  assert.ok(out.kind === 'file');
  assert.ok(new TextEncoder().encode(out.name).length <= 255);
  assert.ok(out.name.endsWith('.gz'));
});

test('safeFileName', () => {
  assert.equal(safeFileName('../../etc/passwd'), 'passwd');
  assert.equal(safeFileName('C:\\Users\\x\\a.txt'), 'a.txt');
  assert.equal(safeFileName('a\u0000b\nc.txt'), 'abc.txt');
  assert.equal(safeFileName(''), 'download');
  assert.equal(safeFileName('..'), 'download');
  assert.equal(safeFileName(null), 'download');
  assert.equal(safeFileName('<img src=x onerror=alert(1)>.png'), '<img src=x onerror=alert(1)>.png');
});

test('formatFingerprint', () => {
  assert.equal(formatFingerprint('90e62dd27af917d6'), '90E6 2DD2 7AF9 17D6');
});
