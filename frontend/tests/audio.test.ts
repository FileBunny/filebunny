import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  audioConstraints,
  audioName,
  audioSummary,
  extension,
  formatDuration,
  level,
  maxSeconds,
  micErrorText,
  micUnavailableText,
  pickFormat,
} from '../src/lib/audio.ts';

test('limit: three minutes', () => {
  assert.equal(maxSeconds, 180);
});

test('audio name: neutral, with a random five-digit number', () => {
  const names = new Set<string>();
  for (let i = 0; i < 200; i++) {
    const name = audioName('ogg');
    assert.match(name, /^audio-\d{5}\.ogg$/);
    names.add(name);
  }
  assert.ok(names.size > 190, 'names repeat too often');
  assert.match(audioName('m4a'), /^audio-\d{5}\.m4a$/);
});

test('constraints: one microphone channel, no video', () => {
  const c = audioConstraints();
  assert.equal(c.video, false);
  assert.equal((c.audio as MediaTrackConstraints).channelCount, 1);
});

test('format: Ogg/Opus first, then WebM, then MP4 (Safari)', () => {
  const firefox = new Set(['audio/ogg;codecs=opus', 'audio/webm;codecs=opus', 'audio/webm']);
  const chrome = new Set(['audio/webm;codecs=opus', 'audio/webm', 'audio/mp4']);
  const safari = new Set(['audio/mp4']);
  assert.deepEqual(pickFormat((t) => firefox.has(t)), { mimeType: 'audio/ogg;codecs=opus', ext: 'ogg' });
  assert.deepEqual(pickFormat((t) => chrome.has(t)), { mimeType: 'audio/webm;codecs=opus', ext: 'webm' });
  assert.deepEqual(pickFormat((t) => safari.has(t)), { mimeType: 'audio/mp4', ext: 'm4a' });
  assert.equal(pickFormat(() => false), null);
});

test('extension from the type the recorder reports', () => {
  assert.equal(extension('audio/ogg; codecs=opus'), 'ogg');
  assert.equal(extension('audio/webm;codecs=opus'), 'webm');
  assert.equal(extension('audio/mp4;codecs=mp4a.40.2'), 'm4a');
  assert.equal(extension('video/mp4'), 'm4a');
  assert.equal(extension(''), 'webm');
});

test('duration and summary', () => {
  assert.equal(formatDuration(0), '0:00');
  assert.equal(formatDuration(42.9), '0:42');
  assert.equal(formatDuration(180), '3:00');
  assert.equal(formatDuration(-1), '0:00');
  assert.equal(audioSummary(42.2, '310 KiB'), 'Audio, 0:42, 310 KiB');
});

test('level: silence is 0, full scale is 1', () => {
  assert.equal(level(new Uint8Array([128, 128, 128])), 0);
  assert.equal(level(new Uint8Array([128, 0, 255])), 1);
  assert.equal(level(new Uint8Array([128, 192])), 0.5);
});

test('error texts', () => {
  assert.match(micErrorText('NotAllowedError'), /blocked/);
  assert.match(micErrorText('NotFoundError'), /No microphone/);
  assert.match(micErrorText('NotReadableError'), /could not be started/);
  assert.match(micErrorText(undefined), /Choose File/);
  assert.match(micUnavailableText(false, true, true), /HTTPS/);
  assert.match(micUnavailableText(true, false, true), /cannot record/);
  assert.match(micUnavailableText(true, true, false), /cannot record/);
  assert.equal(micUnavailableText(true, true, true), '');
});
