import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  expiryChoices, hoursLabel, storageByName, storageChoices, storageLabel, storages, throughServer, uploadLinkChoices,
} from '../src/lib/storage.ts';

test('storage catalog', () => {
  assert.equal(storages[0].name, 'local');
  assert.equal(storageByName(undefined)?.name, 'local');
  assert.equal(storageByName('filebin')?.direct, true);
  assert.equal(storageByName('nope'), undefined);
  assert.equal(storageLabel('filebin'), 'Filebin');
  assert.equal(storageLabel('other'), 'other');
});

test('choices follow what the server offers', () => {
  const names = (list: { name: string }[]) => list.map((s) => s.name);
  // Not known yet: everything, the server still refuses what it lacks.
  assert.equal(throughServer('filebin', null), true);
  assert.deepEqual(names(storageChoices(null, false)), ['local', 'filebin', 'uguu']);
  // Adapter off: Filebin only directly, never for upload links.
  const none = new Set<string>();
  assert.equal(throughServer('local', none), true);
  assert.equal(throughServer('filebin', none), false);
  assert.deepEqual(names(storageChoices(none, true)), ['local', 'filebin']);
  assert.deepEqual(names(storageChoices(none, false)), ['local']);
  // Adapter on.
  const on = new Set(['filebin']);
  assert.equal(throughServer('filebin', on), true);
  assert.deepEqual(names(storageChoices(on, false)), ['local', 'filebin']);
  // Uguu only through the server, never directly.
  assert.deepEqual(names(storageChoices(new Set(['uguu']), true)), ['local', 'filebin', 'uguu']);
  assert.deepEqual(names(storageChoices(none, true)).includes('uguu'), false);
});

test('upload links never store at Uguu', () => {
  const names = (list: { name: string }[]) => list.map((s) => s.name);
  assert.deepEqual(names(uploadLinkChoices(null)), ['local', 'filebin']);
  assert.deepEqual(names(uploadLinkChoices(new Set(['uguu']))), ['local']);
});

test('expiry choices stop at the maximum of the storage', () => {
  const values = (name: string) => expiryChoices(storageByName(name)!).map((o) => `${o.value}=${o.label}`);
  assert.deepEqual(values('local'), ['1h=1 hour', '24h=1 day', '72h=3 days', '168h=7 days']);
  assert.deepEqual(values('filebin'), ['1h=1 hour', '24h=1 day', '72h=3 days', '144h=6 days']);
  assert.deepEqual(values('uguu'), ['1h=1 hour', '2h=2 hours']);
  assert.equal(hoursLabel(2), '2 hours');
  assert.equal(hoursLabel(24), '1 day');
});
