// Helpers for recording audio in the browser (components/AudioCapture).
// The recording is made with MediaRecorder, in the format the browser
// has (Opus in Ogg or WebM, or AAC in MP4 on Safari). It never leaves the
// page unencrypted and is not written to any browser storage; playing it
// back goes through the Web Audio API, so there is no object URL and the
// CSP needs no media-src.

import { neutralName } from './format.ts';

/** The longest recording, in seconds: three minutes. */
export const maxSeconds = 180;

/** About 1 MB for three minutes: plenty for speech. */
export const audioBitsPerSecond = 48_000;

export type AudioFormat = { mimeType: string; ext: string };

/**
 * The formats to try, best first. Ogg/Opus (Firefox) gives a file with a
 * duration every player shows; WebM/Opus (Chrome) has no duration in its
 * header; MP4/AAC is what Safari records.
 */
export const audioFormats: AudioFormat[] = [
  { mimeType: 'audio/ogg;codecs=opus', ext: 'ogg' },
  { mimeType: 'audio/webm;codecs=opus', ext: 'webm' },
  { mimeType: 'audio/mp4;codecs=mp4a.40.2', ext: 'm4a' },
  { mimeType: 'audio/mp4', ext: 'm4a' },
  { mimeType: 'audio/webm', ext: 'webm' },
];

/** The first format the browser can record, or null. */
export function pickFormat(isSupported: (mimeType: string) => boolean): AudioFormat | null {
  return audioFormats.find((f) => isSupported(f.mimeType)) ?? null;
}

/**
 * The file extension for the type the recorder reports; browsers may add
 * parameters to the type they were given, or pick one themselves.
 */
export function extension(mimeType: string): string {
  const t = mimeType.toLowerCase();
  if (t.startsWith('audio/ogg')) return 'ogg';
  if (t.startsWith('audio/mp4') || t.startsWith('video/mp4')) return 'm4a';
  return 'webm';
}

/**
 * The file name inside the OpenPGP message, e.g. "audio-04821.ogg": see
 * neutralName.
 */
export function audioName(ext: string): string {
  return neutralName('audio', ext);
}

/** The constraints for getUserMedia: one microphone channel, no video. */
export function audioConstraints(): MediaStreamConstraints {
  return {
    audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true, autoGainControl: true },
    video: false,
  };
}

/** "0:42", "3:00": minutes and seconds, rounded down. */
export function formatDuration(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
}

/** The summary of step 1, e.g. "Audio, 0:42, 310 KiB". */
export function audioSummary(seconds: number, size: string): string {
  return `Audio, ${formatDuration(seconds)}, ${size}`;
}

/**
 * How loud the microphone is right now, 0 to 1, from the analyser's
 * time-domain samples (128 is silence): the peak, so speech shows clearly.
 */
export function level(samples: Uint8Array): number {
  let peak = 0;
  for (const v of samples) peak = Math.max(peak, Math.abs(v - 128));
  return Math.min(1, peak / 128);
}

/** A short message for a failed getUserMedia, by DOMException name. */
export function micErrorText(name: string | undefined): string {
  switch (name) {
    case 'NotAllowedError':
    case 'SecurityError':
      return 'The microphone is blocked. Allow it for this site in your browser, or use another method.';
    case 'NotFoundError':
    case 'OverconstrainedError':
      return 'No microphone found. Choose File to share a recording you already have.';
    case 'NotReadableError':
    case 'AbortError':
      return 'The microphone could not be started. Close other apps that use it and try again.';
    default:
      return 'The microphone could not be started. Choose File instead.';
  }
}

/** Why recording is not possible here at all, or '' if it is. */
export function micUnavailableText(secureContext: boolean, hasGetUserMedia: boolean, hasRecorder: boolean): string {
  if (!secureContext) return 'Recording only works over HTTPS. Choose File instead.';
  if (!hasGetUserMedia || !hasRecorder) return 'This browser cannot record audio here. Choose File instead.';
  return '';
}
