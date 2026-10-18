# Assessment evidence and synthetic reproductions

These files accompany [the report](../security-assessment-2026-09-06.md). They do not change application behavior. The A01/A02/A03/A04/A06/A07 reproduction assertions now check the implemented fixes. Unaddressed findings still assert vulnerable baseline behavior, so a passing runner does not mean every assessment finding is fixed. See [hardening notes](../security-hardening/README.md) for implementation, validation, and rollout. Historical logs below remain unchanged.

`repro/*.go.txt` are inert source artifacts, excluded from ordinary Go package tests. The passkey test is adapted from the existing full register/login/delete test with user verification disabled. All passwords, tokens, files and users in the tests are synthetic.

Run from this repository with Python 3 and Go available:

```bash
python3 docs/security-assessment-2026-09-06/run_reproductions.py
python3 docs/security-assessment-2026-09-06/run_reproductions.py --race
python3 docs/security-assessment-2026-09-06/run_reproductions.py --fuzz
```

The runner copies tracked source plus current new source/tests under `internal` and `cmd` into a fresh temporary directory, overlays only the reproduction tests, and runs there. It never copies `.env`, runtime databases, archives or real keys. It retains that directory for inspection and prints its path. HTTP tests need permission to bind loopback; dependencies may need network access. Race tests require a working C compiler. The historical concurrency reproduction failed under the race detector; the updated session regression now passes.

Evidence:

- `baseline.json`: commit and hashes of pre-existing working-tree changes.
- `baseline-test.txt`, `vet.txt`, `race.txt`: unmodified baseline results, Go 1.23.12. Empty vet output means no diagnostics.
- `runner-check.txt`: delivered runner verified end to end; all 13 synthetic checks reproduced.
- `reproductions.txt`: successful baseline behavior reproductions; the session race case needs the separate race run.
- `fuzz.txt`: 10-second lexical path-confinement fuzz run.
- `govulncheck.txt`: complete v1.7.0 scan, using Go 1.23.12 for application analysis. Scanner built with automatically downloaded Go 1.26.8. Symbol/package/module alerts are distinct; see report for reachability limits.
- `vuln-install.txt`: initial scanner setup failure because the original container forced the older local toolchain; subsequently resolved.
- `routes.json`: direct guard inventory for 76 routes; not a standalone authorization proof.
- `secret-scan.json`: redacted candidate metadata from a custom scan, with false-positive classifications. No secret values.

The full baseline suite was run in a disposable Go container with no host networking, Docker socket mount or production mounts. An initial writable-bind attempt failed; successful execution used a read-only source mount and a copy inside the container. The local reproduction runner used an extracted temporary Go toolchain. These environment failures are not application failures.
