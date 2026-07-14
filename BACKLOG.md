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

## Resolved
- **Web terminal — runaway `////` (stuck key)** — ✅ ROOT CAUSE FOUND & FIXED.
  - **Proof (ws-debug.log):** `/` = keysym 47 was sent **264×** (press+release pairs) over **~147 s** at ~1/s — nobody holds a key that long → the key was *stuck*. User confirmed: pressing `/` opened the **browser's quick-find** (e.g. Firefox `/`), stealing focus; the `/` **key-up went to the browser chrome, not Guacamole**, so Guacamole thought `/` was still held and repeated it forever.
  - **Fix (`terminal.js`, like the CLI — at the source):** (1) `keyboard.reset()` on `window blur` + `visibilitychange(hidden)` → releases every key the moment focus leaves the terminal, clearing any stuck key (covers `/` and any "other key" that steals focus). (2) `preventDefault` on `/`, `'`, `Backspace` (when not in an input / no modifier) so the browser can't hijack them and steal focus in the first place; Guacamole still sends them to the terminal.
  - **Validated:** JS valid; terminal renders + works (screenshot, no regression); blur/visibility handlers run with **no exception**; the only console error is a pre-existing benign `module is not defined` in guacamole-common.min.js (CJS build). Real-world confirmation (press `/`, types normally + no runaway) is for the user to tick off.
  - Also fixed the protocol NAK (`Receiving argument values unsupported`) by adding `guac.onargv` — matches native; separate from the `////` root cause but a real cleanup.

## Known issues / to investigate
- *(none open)*
  - **Investigation done (this session):** reproduced the full web session headlessly (headless Chrome CAN reach localhost, the extension browser cannot). Captured: `SEGURA_DEBUG_WS` frames + 1 idle screenshot (40s) + 10 burst screenshots across the connect/render window + a zoom on the right edge.
  - **Findings:** terminal renders **cleanly every time** (AWS design fine, bash prompt fine) — **the `////` did NOT reproduce**. guacd renders the terminal as PNG tiles server-side; the frontend sends **no stray key events** (only ping/nop/ack). The only protocol oddity is `ack ... "Receiving argument values unsupported",256` = frontend `guacamole-common-js` 1.5.0 doesn't set `client.onargv`, so it rejects guacd's arg-value stream — **benign** (metadata, not the display). Right-edge element = just the scrollbar.
  - **Fix applied (protocol difference from native):** the one measured difference between segura (broken) and native (works) was that segura's frontend NAKed guacd's `argv` streams (`Receiving argument values unsupported`, status 256) because `terminal.js` had no `guac.onargv` handler. Added one that accepts + drains the streams. Verified in a fresh capture: the NAK count went **6 → 0**; the browser now `ack,1.1,OK` the argv streams like native.
  - **Honest status:** this removes the only protocol-level anomaly, but the `////` is a *pixel* artifact (guacd renders the terminal as PNG tiles) so it never appeared in the protocol log, and it did **not** reproduce in ~12 headless captures. So I can't prove this fixes the visual `////`.
  - **To confirm / if still present:** user reproduces the scenario and checks whether `////` is gone; if not, a **screenshot of the artifact** is the key missing piece (protocol logs can't show a client-side render glitch). Possible next lead if it persists: the bundled `guacamole-common-js` (1.5.0) render vs the version native uses.

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
