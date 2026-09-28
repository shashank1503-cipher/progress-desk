# progress-desk setup

- **Go 1.23+** (`go version`). The server's only dependency is `golang.org/x/net` v0.38.0 (for `websocket`).
- If it's already in the module cache (`ls $(go env GOMODCACHE)/golang.org/x/net@v0.38.0`), builds work offline because `go.sum` is bundled.
  If not, run `go get golang.org/x/net@v0.38.0` in the desk directory. That's the only network step.
- **Python 3** and **git** for `gen_review.py`.
- The page loads Mermaid (jsDelivr), highlight.js (cdnjs) and fonts (Google Fonts). Offline, it still works: diagrams show their source and code isn't highlighted.

Verify: `go test ./...` in the desk directory. It runs the real routes and checks that the page loads, that a same-origin socket gets a snapshot and can post, and that a foreign Origin or a non-loopback Host is refused.
