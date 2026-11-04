#!/usr/bin/env python3
"""Solves a FileBunny proof-of-work challenge, for using the API with curl.

    proof=$(curl -s https://filebunny.eu/api/v1/challenge?action=upload | python3 pow.py)
    curl -H "FileBunny-PoW: $proof" --data-binary @message.gpg https://filebunny.eu/api/v1/files

Reads the challenge JSON on stdin and prints the header value (empty when
the server has proof of work off). See docs/api.md.
"""
import hashlib
import json
import sys

c = json.load(sys.stdin)
token, bits = c["challenge"], c["bits"]
if not token:
    print("")
    sys.exit(0)
n = 0
while True:
    proof = f"{token}.{n}"
    if int.from_bytes(hashlib.sha256(proof.encode()).digest(), "big") >> (256 - bits) == 0:
        print(proof)
        break
    n += 1
