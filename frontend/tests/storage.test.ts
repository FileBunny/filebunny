import assert from 'node:assert/strict';
import { test } from 'node:test';
import { storageByName, storageChoices, storageLabel, storages, throughServer } from '../src/lib/storage.ts';

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
  assert.deepEqual(names(storageChoices(null, false)), ['local', 'filebin']);
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
});
