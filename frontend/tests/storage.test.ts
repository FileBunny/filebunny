import assert from 'node:assert/strict';
import { test } from 'node:test';
import { storageByName, storageLabel, storages } from '../src/lib/storage.ts';

test('storage catalogue', () => {
  assert.equal(storages[0].name, 'local');
  assert.equal(storageByName(undefined)?.name, 'local');
  assert.equal(storageByName('filebin')?.direct, true);
  assert.equal(storageByName('nope'), undefined);
  assert.equal(storageLabel('filebin'), 'Filebin');
  assert.equal(storageLabel('other'), 'other');
});
