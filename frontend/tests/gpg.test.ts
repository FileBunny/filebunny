// Interoperability with GnuPG: what FileBunny encrypts must open with gpg,
// and what gpg encrypts must open in FileBunny. Needs gpg on the PATH; set
// FILEBUNNY_REQUIRE_GPG=1 (as CI does) to fail instead of skipping without it.
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { after, before, describe, test } from 'node:test';
import { decrypt, encrypt, readPrivateKey, readPublicKeys } from '../src/lib/crypto.ts';
import { collect, randomBytes, streamOf, testKey } from './helpers.ts';

const hasGpg = spawnSync('gpg', ['--version']).status === 0;
if (!hasGpg && process.env.FILEBUNNY_REQUIRE_GPG) {
  throw new Error('gpg not found, but FILEBUNNY_REQUIRE_GPG is set');
}

describe('gpg interoperability', { skip: !hasGpg && 'gpg not installed' }, () => {
  let home: string;

  const gpg = (args: string[], input?: Uint8Array | string): Buffer =>
    execFileSync('gpg', ['--homedir', home, '--batch', '--yes', '--pinentry-mode', 'loopback', ...args], {
      input,
      stdio: ['pipe', 'pipe', 'pipe'],
      maxBuffer: 64 * 1024 * 1024,
    });

  // The literal data packet as gpg sees it: format and embedded file name.
  const literal = (ct: Uint8Array, passphrase: string): string => {
    const out = execFileSync(
      'gpg',
      ['--homedir', home, '--batch', '--pinentry-mode', 'loopback', '--passphrase', passphrase, '--list-packets'],
      { input: ct, stdio: ['pipe', 'pipe', 'pipe'] },
    ).toString();
    return out.match(/:literal data packet:\n\s+(.*)/)?.[1] ?? '';
  };

  before(() => {
    home = mkdtempSync(join(tmpdir(), 'filebunny-gpg-'));
    for (const k of ['alice.sec.asc', 'bob.sec.asc']) {
      gpg(['--passphrase', 'bob-test-passphrase', '--import'], testKey(k));
    }
  });
  after(() => {
    spawnSync('gpgconf', ['--homedir', home, '--kill', 'all']);
    rmSync(home, { recursive: true, force: true });
  });

  describe('FileBunny → gpg', () => {
    test('password, text', async () => {
      const text = 'plain text\nwith ünïcode 🐇\n';
      const ct = await collect(await encrypt({ kind: 'text', text }, { password: 'pw 1' }));
      assert.equal(gpg(['--passphrase', 'pw 1', '--decrypt'], ct).toString(), text);
      assert.match(literal(ct, 'pw 1'), /^mode u .*created 0, name=""/);
    });

    test('password, streamed binary file with name', async () => {
      const data = randomBytes(5 * 1024 * 1024 + 3);
      const ct = await collect(
        await encrypt({ kind: 'file', name: 'photo.jpg', data: streamOf(data) }, { password: 'pw' }),
      );
      assert.ok(gpg(['--passphrase', 'pw', '--decrypt'], ct).equals(Buffer.from(data)));
      assert.match(literal(ct, 'pw'), /^mode b .*name="photo\.jpg"/);
    });

    test('public keys of both recipients, hidden key IDs', async () => {
      const keys = [
        ...(await readPublicKeys(testKey('alice.pub.asc'))),
        ...(await readPublicKeys(testKey('bob.pub.asc'))),
      ].map((k) => k.key);
      const ct = await collect(await encrypt({ kind: 'text', text: 'to both' }, { publicKeys: keys }));
      // gpg tries each secret key; Alice's needs no passphrase.
      assert.equal(gpg(['--decrypt'], ct).toString(), 'to both');
      assert.equal(gpg(['--passphrase', 'bob-test-passphrase', '--try-secret-key', 'bob@example.org', '--decrypt'], ct).toString(), 'to both');
    });

    test('SEIPDv1 with MDC, AES-256, iterated S2K, no compression', async () => {
      const ct = await collect(await encrypt({ kind: 'text', text: 'x' }, { password: 'pw' }));
      const packets = execFileSync(
        'gpg',
        ['--homedir', home, '--batch', '--pinentry-mode', 'loopback', '--passphrase', 'pw', '--list-packets'],
        { input: ct, stdio: ['pipe', 'pipe', 'pipe'] },
      ).toString();
      assert.match(packets, /:symkey enc packet: version 4, cipher 9, aead 0, s2k 3/);
      assert.match(packets, /:encrypted data packet:\n\s+length: .*\n\s+mdc_method: 2/);
      assert.doesNotMatch(packets, /compressed packet|aead encrypted/i);
    });
  });

  describe('gpg → FileBunny', () => {
    for (const armor of [false, true]) {
      const flags = armor ? ['--armor'] : [];

      test(`password${armor ? ', armored' : ''}`, async () => {
        const ct = gpg([...flags, '--passphrase', 'pw', '--symmetric'], 'from gpg\n');
        assert.deepEqual(await decrypt(ct, { password: 'pw' }), { kind: 'text', text: 'from gpg\n' });
      });

      test(`public key${armor ? ', armored' : ''}, with file name`, async () => {
        const dir = mkdtempSync(join(tmpdir(), 'filebunny-file-'));
        try {
          const data = randomBytes(100_000);
          writeFileSync(join(dir, 'data.bin'), data);
          gpg([...flags, '--trust-model', 'always', '-r', 'bob@example.org', '-o', join(dir, 'out'), '--encrypt', join(dir, 'data.bin')]);
          const key = await readPrivateKey(testKey('bob.sec.asc'), 'bob-test-passphrase');
          const out = await decrypt(readFileSync(join(dir, 'out')), { privateKeys: [key] });
          assert.ok(out.kind === 'file' && out.name === 'data.bin');
          assert.ok(out.kind === 'file' && Buffer.from(out.data).equals(Buffer.from(data)));
        } finally {
          rmSync(dir, { recursive: true, force: true });
        }
      });
    }

    test('hidden recipient (--throw-keyids)', async () => {
      const ct = gpg(['--trust-model', 'always', '--throw-keyids', '-r', 'alice@example.org', '--encrypt'], 'hidden');
      const key = await readPrivateKey(testKey('alice.sec.asc'), '');
      assert.deepEqual(await decrypt(ct, { privateKeys: [key] }), { kind: 'text', text: 'hidden' });
    });
  });
});
