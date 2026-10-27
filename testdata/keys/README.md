# Test keys — DO NOT USE

These OpenPGP keys exist only for automated tests (OpenPGP.js and `gpg`
interoperability). Their private keys are public in this repository, so
they protect nothing. Never encrypt real data to them.

| Key | Algorithms | Private key passphrase |
|---|---|---|
| `alice@example.org` | Ed25519 + Curve25519 (v4) | none |
| `bob@example.org` | RSA 3072 | `bob-test-passphrase` |

Fingerprints:

- Alice: `90E62DD27AF917D60D24641B896E0850A8FC4BFF`
- Bob: `BF3DD0FA469C69DA5780CCDCC664330D4C8B614B`

Regenerate with `gpg --quick-gen-key` (no expiry) and export with
`gpg --armor --export` / `--export-secret-keys` if they ever need replacing.
