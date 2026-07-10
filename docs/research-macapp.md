# macOS Desktop Wrapper — Tauri v2 Thin-Client Research

Scope: wrap the taskman React SPA in a Tauri v2 macOS app as a **thin client** — the
webview loads the already-running Go+Mongo server at `http://localhost:8484` (which
already serves the SPA + `/api` same-origin). No bundled frontend, no server changes.
Report only; nothing scaffolded or installed.

## 1. Prerequisites checklist

| Tool | Status | Command (if needed) |
|---|---|---|
| Rust (`rustc`/`cargo`) | **NEEDS-INSTALL** — not on PATH at all | `curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \| sh` |
| `rustup` | **NEEDS-INSTALL** — installed by the command above | (same) |
| Node.js | INSTALLED — v24.10.0 | — |
| npm | INSTALLED — 11.6.0 | — |
| Xcode CLT | INSTALLED — `/Library/Developer/CommandLineTools`, `com.apple.pkg.CLTools_Executables` present | — |
| macOS | 26.3.1 (build 25D771280a) — well above Tauri's 10.15 floor | — |
| Tauri CLI | **NEEDS-INSTALL** — `cargo tauri --version` fails (no cargo); `npm ls -g @tauri-apps/cli` empty; `npx tauri` errors "could not determine executable" | after Rust: `cd ui && npm install -D @tauri-apps/cli@^2` |

**Blocking gap:** Rust is completely absent (not even `rustup`) — this is the one hard
prerequisite; nothing else here works until `rustc`/`cargo` exist. Node, npm, Xcode CLT,
and macOS version are already satisfied.

## 2. Tauri v2 approach — confirmed

The external-URL window is the standard v2 pattern, not a hack.

- **Window URL** — `app.windows[].url` in `tauri.conf.json` takes a plain string:
  ```json
  { "app": { "windows": [
    { "label": "main", "title": "taskman", "url": "http://localhost:8484",
      "width": 1280, "height": 860, "minWidth": 900, "minHeight": 600 }
  ]}}
  ```
  `WebviewUrl::External(url)` is a Rust-side enum used only when building a window at
  runtime; for static config it's just a URL string. **v1→v2:** v1's
  `tauri.windows[]` + `package`/`tauri` split and `allowlist` permissions are gone in
  v2 — windows moved to `app.windows[]`, permissions moved to `capabilities/` (below).
  Stale v1 tutorials mentioning `allowlist` don't apply. `build.frontendDist` is
  schema-optional/URL-capable but scaffolds commonly still expect a real `dist` dir to
  exist for the build step — **verify the minimum accepted value when scaffolding.**

- **Capabilities** — v2's `src-tauri/capabilities/*.json` replaces `allowlist`:
  `{ "identifier": "default", "windows": ["main"], "permissions": ["core:default"] }`.
  Since `ui/src` never imports `@tauri-apps/api` or calls `invoke()` (plain
  `fetch('/api/...')`, zero Tauri awareness), **no permissions beyond the scaffolded
  `core:default` are needed** — confirms the "almost nothing" assumption.

- **Bundle/window config** — `identifier` (top-level, reverse-DNS, e.g.
  `com.pbhealth.taskman`) is the required bundle field; title/width/height/minWidth/
  minHeight as above. Window size is NOT remembered across launches by core v2 by
  default (needs `tauri-plugin-window-state`) — skip for v1, cosmetic only.

- **Unsigned run** — no Apple Developer account needed to build+run locally. A `.app`
  built and run on the same machine is not Gatekeeper-quarantined (quarantine xattr is
  set by network downloads — Safari/Mail/AirDrop — not local `cargo`/`tauri build`), so
  for this single-user case it's likely a non-issue. It matters only if the `.app` is
  handed to another machine: right-click→Open (older macOS) or System Settings →
  Privacy & Security → "Open Anyway" (recent macOS tightened this further — verify live
  if distributing later). Full signing+notarization needs a paid Apple Developer
  membership ($99/yr) — confirmed unnecessary here, don't set up.

- **`http://localhost` in WKWebView — the crux.** Apple's ATS exempts the literal
  single-label hostname `localhost` (no dot) as "local networking" by default — this
  matches the plan exactly (`http://localhost:8484`, not the IP-literal
  `127.0.0.1`, which is reported to behave differently). Confidence is high but not
  100%: a stale 2022 Tauri-1.0.4 GitHub issue reported an ATS block even for
  `localhost`, and the v2 config schema (checked directly) has **no** first-class
  `exceptionDomain`/ATS field. **Fallback:** a hand-written `src-tauri/Info.plist`
  fragment (`NSAppTransportSecurity > NSExceptionDomains > localhost >
  NSExceptionAllowsInsecureHTTPLoads: true`) — Tauri v2 merges a custom
  `src-tauri/Info.plist` into the generated one at build time. **Verify with the first
  `tauri dev` run before building further on this assumption.**

## 3. Codebase fit — confirmed, zero frontend change

- `ui/src/api.ts:196` — the single `request()` helper does `fetch(\`/api${path}\`, ...)`
  — relative path, no absolute origin, no `credentials` override anywhere in the file
  (grepped). Default `fetch` credentials mode is `"same-origin"`, so cookies flow
  automatically once Tauri loads the document itself from `http://localhost:8484/` —
  same-origin, same as a normal browser tab. No CORS headers exist server-side
  (grepped `server/*.go`) and none are needed for this path.
- `server/main.go:36-42` — `spaHandler("ui/dist")` mounts at `/`, falling back to
  `index.html` for SPA routes; confirmed `ui/dist/` exists and the server currently
  answers `GET /` with `200 OK` on :8484.
- `ui/dist/index.html` uses root-relative asset paths (`/assets/...`, `/favicon.svg`);
  `vite.config.ts` sets no `base` override (defaults to `/`) — matches serving from the
  Go root exactly as today. Loading `http://localhost:8484/` in the webview yields the
  identical page a browser gets.
- Cookie: `server/auth.go:24,115-128` — `taskman_session`, `HttpOnly: true`,
  `SameSite: Lax`, deliberately **not** `Secure` (`auth_pure_test.go:158` asserts this,
  comment: "plain-HTTP LAN tool"). The app already assumes plain HTTP/no TLS, so there's
  no mixed-content or secure-cookie concern to introduce.

**Verdict: zero frontend/server changes needed.** The wrapper is purely additive — a new
`src-tauri/` directory alongside the existing repo.

## 4. Risks / caveats

- **Server-must-be-running UX**: the wrapper has no awareness of whether `make up` /
  `go run ./server` is up; if down, the webview just shows a connection error, not a
  friendly message. Defer — bigger scope than this wrapper.
- **http, not https**: intentional per the codebase's existing stance (§3), not a
  regression from the wrapper.
- **ATS/localhost**: the single biggest unverified assumption (§2) — cheap to test (one
  `tauri dev` run) but not yet proven live in this environment.
- **Signing**: fine for local single-user use; revisit only if distributing the `.app`
  off this machine.
- **`build.frontendDist`**: schema-optional/URL-capable, but confirm the CLI actually
  accepts a bare URL or minimal placeholder dir at scaffold time.

## 5. Recommended minimal file layout

```
taskman/
  ui/                      # unchanged
  server/                  # unchanged
  src-tauri/
    Cargo.toml
    tauri.conf.json        # app.windows[].url = "http://localhost:8484"
    build.rs
    capabilities/
      default.json         # core:default only
    src/
      main.rs              # generated default, no custom commands
    icons/                 # generated via `tauri icon`
```

No changes to `ui/` or `server/`; `src-tauri/` is a self-contained sibling operated on
independently of the existing `make up` / `go run ./server` workflow.
