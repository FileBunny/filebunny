# Security policy

FileBunny is built so that the server never sees plaintext, keys or
passwords. If you find a way around that, or any other vulnerability,
please tell us privately first.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting:
**Security → Report a vulnerability** on
<https://github.com/FileBunny/filebunny>.

Please include what you found, how to reproduce it and what an attacker
could gain. Do not test against other people's files, and do not run
denial-of-service tests against filebunny.eu.

We aim to acknowledge reports within a week. FileBunny is a small
volunteer project, so fixes may take longer; we will keep you informed and
credit you unless you prefer otherwise.

## Supported versions

Only the latest release, and the code running on filebunny.eu, receive
security fixes. The project is in early development; there are no releases
yet.

## Scope

In scope: this repository (backend, frontend, Caddy and Docker
configuration, CI) and the service at filebunny.eu.

Especially interesting:
- anything that lets the server or a third party read plaintext, keys or
  passwords;
- client IP addresses, file IDs or tokens ending up in logs or storage;
- reading, deleting or enumerating other people's uploads;
- differences between the code in this repository and what filebunny.eu
  serves.

Out of scope: weaknesses in OpenPGP itself, attacks that need a compromised
device of the sender or recipient, and missing rate limiting (planned).

## What FileBunny does and does not protect

See [docs/architecture.md](docs/architecture.md#threat-model) and the
latest [security review](docs/security-review.md).
