# FileBunny

Encrypt in your browser with OpenPGP, share anonymously. The server only ever
sees ciphertext: no accounts, no trackers, no IP logging.

- Standard OpenPGP (OpenPGP.js), decryptable with `gpg`
- Password mode and public-key mode
- Expiring share links, upload links for receiving encrypted files
- Self-hostable with Docker Compose and Caddy
- Reproducible builds

Status: early development (phase 1, foundation).

## Development

Requirements: Go (the toolchain pinned in `backend/go.mod` is downloaded
automatically), Node.js 24+, Docker.

```sh
make test        # backend + frontend checks
make build       # backend binary + frontend bundle
make vuln        # govulncheck
make repro       # verify that backend and frontend builds are reproducible
docker compose up -d --build && scripts/smoke-test.sh
```

See [CLAUDE.md](CLAUDE.md) for conventions.

## Documentation

- [Architecture and threat model](docs/architecture.md): what is stored,
  what FileBunny does and does not protect against
- [API](docs/api.md)
- [Self-hosting](docs/self-hosting.md)
- [Security review](docs/security-review.md)
- [Reporting a vulnerability](SECURITY.md)

## License

[AGPL-3.0](LICENSE)
