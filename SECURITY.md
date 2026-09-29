# Security

batlog runs as your user, never needs sudo, and makes no network calls. Its
data (battery samples and per-app energy) stays in
`~/Library/Application Support/batlog/`.

If you find a vulnerability, report it privately through
[GitHub's private vulnerability reporting](https://github.com/jufianto/batlog/security/advisories/new)
rather than in a public issue.

Only the latest commit on `master` is supported until the first release.
