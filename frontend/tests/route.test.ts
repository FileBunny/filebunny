import assert from 'node:assert/strict';
import { test } from 'node:test';
import { generatePassword } from '../src/lib/format.ts';
import { deleteLink, manageLink, parseRoute, shareLink, uploadLink } from '../src/lib/route.ts';

const id = 'bXqU3xr4yS2NwJ0qkJ8W4A';
const token = 'OQaeFws2C8_yPR0PH6IIJP0vrUBbycKlesYhrODkODM';

test('routes', () => {
  assert.deepEqual(parseRoute(''), { page: 'send' });
  assert.deepEqual(parseRoute('#/'), { page: 'send' });
  assert.deepEqual(parseRoute(`#/d/${id}`), { page: 'download', id });
  assert.deepEqual(parseRoute(`#/delete/${id}/${token}`), { page: 'delete', id, token });
  for (const bad of [`#/d/${id}x`, '#/d/../../etc', `#/delete/${id}`, '#/other', `#/d/${id}/`]) {
    assert.deepEqual(parseRoute(bad), { page: 'not-found' }, bad);
  }
});

test('links round trip', () => {
  const base = 'https://filebunny.eu';
  assert.equal(shareLink(base, id), `https://filebunny.eu/#/d/${id}`);
  assert.deepEqual(parseRoute(new URL(shareLink(base, id)).hash), { page: 'download', id });
  assert.deepEqual(parseRoute(new URL(deleteLink(base, id, token)).hash), { page: 'delete', id, token });
});

test('generated passwords', () => {
  const seen = new Set<string>();
  for (let i = 0; i < 100; i++) {
    const p = generatePassword();
    assert.match(p, /^[a-km-z2-9]{5}(-[a-km-z2-9]{5}){3}$/);
    seen.add(p);
  }
  assert.equal(seen.size, 100);
});

test('upload link routes', () => {
  const fp = '90e62dd27af917d60d24641b896e0850a8fc4bff';
  const base = 'https://filebunny.eu';
  assert.deepEqual(parseRoute('#/receive'), { page: 'receive' });
  assert.deepEqual(parseRoute('#/key'), { page: 'key' });
  const up = uploadLink(base, id, '90E6 2DD2 7AF9 17D6 0D24  641B 896E 0850 A8FC 4BFF');
  assert.equal(up, `${base}/#/u/${id}/${fp}`);
  assert.deepEqual(parseRoute(new URL(up).hash), { page: 'inbox-upload', id, fingerprint: fp });
  assert.deepEqual(parseRoute(new URL(manageLink(base, id, token)).hash), { page: 'inbox-manage', id, token });
  for (const bad of [`#/u/${id}`, `#/u/${id}/${fp.slice(1)}`, `#/u/${id}/${fp.toUpperCase()}`, `#/m/${id}/short`, `#/u/${id}/${fp}/`]) {
    assert.deepEqual(parseRoute(bad), { page: 'not-found' }, bad);
  }
});
