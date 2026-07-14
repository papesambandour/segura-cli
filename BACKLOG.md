# SEGURA-CLI — Backlog

Managed by Claude. Autonomous session started while the user sleeps.
Rule: **each item is tested & validated before moving to the next.**

## Order of work (per user)
1. Web terminal redesign — AWS Console aesthetic, ergonomic. **← current**
2. L1 intelligent cache (CLI + web) — cache-first + smart background refresh, sane TTLs.

---

## 1. Web redesign (AWS Console aesthetic) — ✅ DONE & VALIDATED
- Rewrote `static/css/style.css` with an AWS design system (navy #232f3e nav, orange #ff9900/#ec7211, blue #0972d3), all selectors preserved.
- Dashboard `index.html`: AWS light console — navy nav w/ orange diamond logo, "Privileged sessions" title + live count, ergonomic filter box, white credential cards (navy avatar, mono username, IP pill, hover Connect).
- `app.js`: added client-side filter (user/device/ip) + live count, kept all connect logic.
- Terminal `terminal.html`: AWS navy toolbar (brand + green conn info + outline/orange buttons), dark terminal, AWS-dark SFTP file manager + file viewer.
- Validated: `go build` + `go vet` clean; all `getElementById` targets still present (no broken DOM refs); `node --check` on both JS files OK; pages serve HTTP 200; **visual confirmed via headless-Chrome screenshots** (dashboard + terminal chrome look AWS-professional).
- Note: the claude-in-chrome extension browser couldn't reach the local server (env limitation) → validated visually with headless Chrome on self-contained previews instead.

## 2. L1 intelligent cache (CLI + web) — ✅ DONE & VALIDATED
Generic stale-while-revalidate cache in `internal/credcache/` (no import cycle):
- **cache-first**: serves cached instantly; **SWR**: `age<fresh` serve fresh, `age<stale` serve stale + one background refresh, else live fetch; falls back to stale on fetch error.
- **CLI** (`webproxy.ListCredentials`): disk-backed (`~/.segura/creds-<host>-<user>.json`, 0600) so a short-lived process benefits across runs; TTL 3 min; `segura list --refresh` forces live.
  - Measured: cold **13 407 ms → warm 23 ms** (~580×). `--refresh` = 9 096 ms (live).
- **Web** (`SessionManager` + `handleCredentials`): in-memory, fresh 1 min / stale 10 min + background refresh; warmed at server start; `GET /api/credentials?refresh=1` (Refresh button) forces live.
  - Measured: dashboard **~13 s → ~25–31 ms** (cache). `?refresh=1` = 2 245 ms (live, client already authed).
- Connect paths (connectUpstream / guacamole / ConnectBrowser) intentionally still fetch **fresh** (need a valid SRToken); only listings are cached. SRToken is `json:"-"` so never persisted to disk or sent to the frontend.
- Validated: 5 `-race` unit tests on the cache (fresh/stale/SWR/disk-persistence/error-fallback) + live latency measurements on CLI and web.

---

## Known issues / to investigate
- **Web terminal — stray `////` that self-heals** (reported by user). Appears on the web terminal **without opening vim and without any user action** (at idle / on connect), then repairs itself. Likely an escape-sequence / guacd rendering artifact on the web path.
  - Need: a screenshot of the artifact (the one the user tried to attach didn't come through) + a `SEGURA_DEBUG_WS=1` capture while it happens, to see the frames. Do NOT guess a fix — capture first (règle #0).

---

## Done this session (context)
- CLI vim `0`-loop fix (daSequenceFilter, down-direction clean-terminal shim) — validated by user.
- Web `0`-bug: root cause was missing pagination in `connectUpstream`/`guacamole.go` (credential on page 2 → immediate disconnect); fixed all 5 dashboard callsites → validated by user.
- scp directory support + `-O` for dir uploads only (SFTP for downloads) — tested on server, all 4 transfer scenarios pass.
- Debug instrumentation: `SEGURA_DEBUG_TTY` (CLI), `SEGURA_DEBUG_WS` (web).

## Uncommitted work (awaiting user go to commit)
Everything below is built, vetted and validated — nothing committed yet:
- scp `%` fix, dir support (`-r` + `-O` for dir uploads), pagination fetch-all (5 callsites), shell completion, CLI/web vim `0`-loop fixes, debug instrumentation (`SEGURA_DEBUG_TTY`/`SEGURA_DEBUG_WS`), README/CLAUDE.md updates.
- **This session (autonomous):** AWS-Console web redesign (`static/*`), L1 intelligent cache (`internal/credcache/` + wiring in `cmd/list.go`, `webproxy/connect.go`, `webserver/{session,handlers,server}.go`), web favicon (`static/favicon.svg` + `favicon-32.png` + `apple-touch-icon.png`, linked in both pages).

## How to view the redesign
A web server is running: open **http://localhost:8080** in your browser (the new AWS dashboard + terminal). If it's not up: `./segura web --port 8080`.
