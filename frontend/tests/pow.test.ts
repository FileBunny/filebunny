import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { test } from 'node:test';
import { leadingZeroBits, sha256 } from '../src/lib/sha256.ts';
import { solve } from '../src/lib/pow.ts';

const hex = (b: Uint8Array) => Buffer.from(b).toString('hex');

test('sha256 matches node for every length around the block boundaries', () => {
  for (let len = 0; len < 200; len++) {
    const data = new Uint8Array(len).map((_, i) => (i * 31 + len) & 0xff);
    assert.equal(hex(sha256(data)), createHash('sha256').update(data).digest('hex'), `length ${len}`);
  }
  assert.equal(hex(sha256(new TextEncoder().encode('abc'))), 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad');
});

test('leading zero bits', () => {
  assert.equal(leadingZeroBits(new Uint8Array(32)), 256);
  assert.equal(leadingZeroBits(new Uint8Array([0x80])), 0);
  assert.equal(leadingZeroBits(new Uint8Array([0x01])), 7);
  assert.equal(leadingZeroBits(new Uint8Array([0, 0x10])), 11);
});

test('solve finds a proof the server accepts', async () => {
  const proof = await solve({ challenge: 'abc-DEF_123', bits: 12 });
  assert.match(proof, /^abc-DEF_123\.\d+$/);
  const digest = createHash('sha256').update(proof).digest();
  assert.ok(leadingZeroBits(new Uint8Array(digest)) >= 12);
});

test('solve can be cancelled', async () => {
  const c = new AbortController();
  c.abort();
  await assert.rejects(solve({ challenge: 'x', bits: 40 }, c.signal));
});
