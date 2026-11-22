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
token, bits, want = c["challenge"], c["bits"], c.get("proofs", 1)
if not token:
    print("")
    sys.exit(0)
# Find `want` counters (in ascending order), each giving `bits` zero bits.
found = []
n = 0
while len(found) < want:
    digest = hashlib.sha256(f"{token}.{n}".encode()).digest()
    if int.from_bytes(digest, "big") >> (256 - bits) == 0:
        found.append(str(n))
    n += 1
print(".".join([token] + found))
