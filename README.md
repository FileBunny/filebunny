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
make repro       # verify that the backend build is reproducible
docker compose up --build
```

See [CLAUDE.md](CLAUDE.md) for conventions.

## License

[AGPL-3.0](LICENSE)
