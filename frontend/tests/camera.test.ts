import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  cameraConstraints,
  cameraErrorText,
  cameraUnavailableText,
  mirrorPreview,
  nextCamera,
  photoName,
  photoSummary,
} from '../src/lib/camera.ts';

test('photo name is neutral', () => {
  assert.equal(photoName, 'photo.jpg');
});

test('constraints: video only, back camera by default, chosen camera exact', () => {
  assert.deepEqual(cameraConstraints('medium', {}), {
    video: { width: { ideal: 1920 }, height: { ideal: 1080 }, facingMode: { ideal: 'environment' } },
    audio: false,
  });
  assert.deepEqual(cameraConstraints('small', { deviceId: 'cam2' }), {
    video: { width: { ideal: 1280 }, height: { ideal: 720 }, deviceId: { exact: 'cam2' } },
    audio: false,
  });
  const largest = cameraConstraints('largest', { facing: 'user' }).video as MediaTrackConstraints;
  assert.deepEqual(largest.width, { ideal: 4096 });
  assert.deepEqual(largest.facingMode, { ideal: 'user' });
});

test('next camera, round robin', () => {
  assert.equal(nextCamera([], undefined), undefined);
  assert.equal(nextCamera(['a'], 'a'), undefined);
  // Before access is granted, browsers may list cameras with an empty ID.
  assert.equal(nextCamera(['', ''], undefined), undefined);
  assert.equal(nextCamera(['a', 'b'], 'a'), 'b');
  assert.equal(nextCamera(['a', 'b'], 'b'), 'a');
  assert.equal(nextCamera(['a', 'b', 'c'], undefined), 'a');
  assert.equal(nextCamera(['a', 'b', 'c'], 'gone'), 'a');
});

test('mirror only the preview of a camera facing you', () => {
  assert.equal(mirrorPreview('environment'), false);
  assert.equal(mirrorPreview('user'), true);
  assert.equal(mirrorPreview(undefined), true);
});

test('error texts', () => {
  assert.match(cameraErrorText('NotAllowedError'), /blocked/);
  assert.match(cameraErrorText('NotFoundError'), /No camera/);
  assert.match(cameraErrorText('NotReadableError'), /could not be started/);
  assert.match(cameraErrorText(undefined), /Choose File/);
  assert.match(cameraUnavailableText(false, true), /HTTPS/);
  assert.match(cameraUnavailableText(true, false), /cannot use the camera/);
  assert.equal(cameraUnavailableText(true, true), '');
});

test('summary', () => {
  assert.equal(photoSummary(1920, 1080, '412 KiB'), 'Photo, 1920×1080, 412 KiB');
});
