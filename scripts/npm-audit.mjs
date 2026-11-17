#!/usr/bin/env node
// npm audit with accepted findings, like .trivyignore for the images.
//
// Run in frontend/. Fails on every advisory of moderate severity or more,
// in runtime and build dependencies alike, unless .npmauditignore (in the
// repository root) lists its GHSA ID with an expiry date that has not
// passed:
//
//   # Why it is accepted, and when to remove it.
//   GHSA-xxxx-xxxx-xxxx exp:2026-12-15
//
// After the date the advisory fails the audit again, so an accepted risk
// is looked at again instead of staying forever.

import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';

const levels = ['info', 'low', 'moderate', 'high', 'critical'];
const minLevel = levels.indexOf('moderate');
const today = new Date().toISOString().slice(0, 10);

const ignoreFile = new URL('../.npmauditignore', import.meta.url);
const accepted = new Map();
for (const [i, raw] of readFileSync(ignoreFile, 'utf8').split('\n').entries()) {
  const line = raw.replace(/#.*/, '').trim();
  if (line === '') continue;
  const m = /^(GHSA(?:-[23456789cfghjmpqrvwx]{4}){3}) exp:(\d{4}-\d{2}-\d{2})$/.exec(line);
  if (!m) {
    console.error(`.npmauditignore:${i + 1}: expected "GHSA-xxxx-xxxx-xxxx exp:YYYY-MM-DD"`);
    process.exit(2);
  }
  accepted.set(m[1], m[2]);
}

// npm audit exits non-zero when it finds anything; the JSON is what counts.
let out;
try {
  out = execFileSync('npm', ['audit', '--json'], { encoding: 'utf8', maxBuffer: 64 << 20 });
} catch (err) {
  out = err.stdout;
}
const report = JSON.parse(out);
if (report.error) {
  console.error(`npm audit failed: ${report.error.summary ?? JSON.stringify(report.error)}`);
  process.exit(2);
}

// Each advisory once, with the packages that pull it in.
const advisories = new Map();
for (const [pkg, v] of Object.entries(report.vulnerabilities ?? {})) {
  for (const via of v.via) {
    if (typeof via !== 'object') continue;
    const id = /GHSA(?:-[a-z0-9]{4}){3}/.exec(via.url ?? '')?.[0] ?? `npm:${via.source}`;
    const a = advisories.get(id) ?? { id, severity: via.severity, title: via.title, packages: new Set() };
    a.packages.add(pkg);
    advisories.set(id, a);
  }
}

let failed = false;
for (const a of advisories.values()) {
  if (levels.indexOf(a.severity) < minLevel) continue;
  const exp = accepted.get(a.id);
  const what = `${a.id} (${a.severity}) ${a.title} [${[...a.packages].join(', ')}]`;
  if (exp && today <= exp) {
    console.log(`accepted until ${exp}: ${what}`);
  } else {
    console.log(`${exp ? `expired ${exp}` : 'FOUND'}: ${what}`);
    failed = true;
  }
}
for (const id of accepted.keys()) {
  if (!advisories.has(id)) console.log(`no longer reported, remove from .npmauditignore: ${id}`);
}
if (!failed) console.log('npm audit: nothing that is not accepted');
process.exit(failed ? 1 : 0);
