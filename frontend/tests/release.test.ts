import assert from 'node:assert/strict';
import { test } from 'node:test';
import { releasesUrl, verifyUrl, version } from '../src/lib/release.ts';

test('a build without a release tag is dev', () => {
  assert.equal(version, 'dev');
  assert.equal(releasesUrl, 'https://github.com/FileBunny/filebunny/releases');
  assert.match(verifyUrl, /docs\/verify\.md$/);
});
