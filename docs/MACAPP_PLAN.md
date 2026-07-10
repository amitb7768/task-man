# taskman macOS app — build contract (Tauri v2 thin client)

Grounding: `docs/research-macapp.md`. Purely ADDITIVE — a new `src-tauri/` sibling to
`ui/`. ZERO changes to `ui/`, `server/`, or the SPA/API (confirmed same-origin, relative
`/api`, cookie behaves normally in WebView). Toolchain: Rust 1.97 installed; Node/CLT/
macOS ready.

## Decisions (closed)

| # | Decision | Choice |
|---|----------|--------|
| 1 | App type | Tauri v2 macOS app; window loads the SPA from the Go server (thin client). |
| 2 | Server target | Default `http://localhost:8484`; user-editable, persisted. Same `.app` works for teammates pointed at a LAN IP. |
| 3 | Native scope v1 | Window only + a native settings/connection shell. Menu-bar quick-add = documented fast-follow (NOT built). |
| 4 | Signing | Unsigned (internal). Right-click → Open. No notarization/Apple Developer setup. |
| 5 | Backend lifecycle | App does NOT start the server/Mongo. Assumes it's running; shows a friendly retry screen if unreachable. |

## Architecture — two loads in one window

The window has a bundled **local shell** (settings + connection-error UI) AND loads the
**remote SPA**. Same window, different navigations:

1. Window's initial `url` = the bundled shell (`shell/index.html`, a Tauri asset — has
   `invoke` access).
2. Shell JS on load calls `check_and_go`: health-check the stored server URL →
   if reachable, Rust navigates the window to `{serverUrl}/` (the remote SPA, no Tauri
   injection needed there); if not, shell shows the connection-error/settings UI.
3. Native menu item **Settings…** navigates the window BACK to the shell (settings mode)
   from anywhere — works because the menu is native, independent of what the webview shows.

This keeps the React SPA on a plain remote http origin with zero Tauri coupling, and
means the settings/error UI can exist even when the server is down.

## Rust side (`src-tauri/src/`)

- Persisted config: a plain JSON file (`{ "serverUrl": "..." }`) in the app config dir
  (`app.path().app_config_dir()`), default `http://localhost:8484`. Two small read/write
  fns; no store plugin needed.
- Health-check: `std::net::TcpStream::connect_timeout` to the URL's host:port (~1.5s
  timeout). Port open = reachable enough to navigate; refused/timeout = unreachable.
  (No HTTP client dep — TCP connect is sufficient for a thin client; the SPA/login
  surfaces any deeper failure.)
- `#[tauri::command]`s (callable ONLY from the local shell — see capabilities):
  `get_server_url() -> String`, `set_server_url(url) -> Result<(), String>` (validate
  scheme http/https + host:port, persist, then check+navigate; return Err to display in
  shell), `retry() -> ...` (re-check + navigate or report down), `open_settings()` (nav
  window to shell settings mode).
- Native app menu: Settings… (→ shell), Reload, plus the standard Quit/Edit items.
- Navigation helper wraps `webview.navigate(...)` for both the internal shell URL and the
  external server URL.

## Capabilities / security (Opus review will target this)

- `capabilities/`: grant the custom commands ONLY to the local shell window/origin.
  The REMOTE origin (the server's SPA) MUST NOT be able to `invoke` any native command —
  scope the capability to the local asset context, not `*`/remote. Defense-in-depth: a
  compromised or hostile server can't drive the native layer.
- Otherwise `core:default` only; the SPA needs no Tauri permissions.

## Config / bundle

- `tauri.conf.json`: `productName` "taskman", identifier `com.pbhealth.taskman`,
  `build.frontendDist` → the bundled `shell/` (static HTML, no beforeBuildCommand),
  window `{ title: "taskman", width 1280, height 860, minWidth 1000, minHeight 700,
  url: <shell index> }`. Remember window size/position via `tauri-plugin-window-state`
  (standard, minimal).
- `Info.plist` (merged by Tauri): `NSAppTransportSecurity → NSAllowsLocalNetworking = true`
  — REQUIRED so plain-http works for localhost AND LAN IPs (the settings-override case).
  Without it, teammates on `http://10.x.x.x:8484` are blocked by ATS.
- Icons: generate from `ui/dist/favicon.svg` if trivial (`cargo tauri icon`), else Tauri
  defaults. Non-blocking.

## Shell UI (`src-tauri/shell/index.html`)

One self-contained static HTML file (inline CSS/JS, no build step), styled to match the
taskman look (reuse the token palette values — dark-friendly). States:
- **Connecting** — brief spinner while `check_and_go` runs.
- **Connection error** — "Can't reach taskman at {url}", the URL field, Retry.
- **Settings** — server URL input + Save (+ Cancel/back to app if currently connected).
Must degrade gracefully if `window.__TAURI__` is absent (opened outside Tauri) — show a
note rather than throwing, so the file is sanity-checkable in a plain browser.

## Build & run

- `cargo install tauri-cli --version '^2'` (build agent's first step) → `cargo tauri`.
- Makefile (additive targets): `mac-dev` → `cd src-tauri && cargo tauri dev`;
  `mac-build` → `cargo tauri build` (→ `src-tauri/target/release/bundle/macos/taskman.app`).
- Running the app needs the Go server up (`make up`). Document in README (short section).

## Verification bar

- `cargo tauri build` succeeds and emits `taskman.app`. `cargo build` clean, config valid.
- `shell/index.html` opens standalone without throwing (Tauri-absent guard works).
- Live window load / ATS-over-http / cookie-login can only be confirmed by the USER
  opening the `.app` against the running server — the build agent CANNOT observe a window
  (no display; browser automation policy-blocked). Do NOT block on `tauri dev`.
- `ui/` and `server/` untouched (grep/mtime).

## Non-goals (v1)

Menu-bar quick-add (fast-follow), notifications, code signing/notarization, auto-starting
the backend, bundling Mongo/Go (that's the rejected self-contained path), Windows/Linux.
