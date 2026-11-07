import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fingerprintUri, qrModules } from '../src/lib/qr.ts';

test('fingerprint QR content', () => {
  assert.equal(
    fingerprintUri('90E6 2DD2 7AF9 17D6 0D24  641B 896E 0850 A8FC 4BFF'),
    'OPENPGP4FPR:90E62DD27AF917D60D24641B896E0850A8FC4BFF',
  );
  assert.equal(fingerprintUri('a'.repeat(64)), `OPENPGP4FPR:${'A'.repeat(64)}`);
  assert.throws(() => fingerprintUri('not a fingerprint'));
});

test('QR modules: square, with the three finder patterns', () => {
  const m = qrModules(fingerprintUri('90E62DD27AF917D60D24641B896E0850A8FC4BFF'));
  const n = m.length;
  assert.ok(n >= 21 && m.every((row) => row.length === n));
  // Finder patterns: a dark 7×7 square ring in three corners.
  for (const [r, c] of [[0, 0], [0, n - 7], [n - 7, 0]] as const) {
    assert.ok(m[r]![c] && m[r]![c + 6] && m[r + 6]![c] && m[r + 6]![c + 6], `corner ${r},${c}`);
    assert.ok(!m[r + 1]![c + 1], `ring ${r},${c}`);
  }
});
