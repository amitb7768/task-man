# Deploying taskman on a LAN + sharing the app

taskman is designed for one machine on your internal network to act as the **server**;
teammates reach it from a browser or the macOS app. There is no TLS and no cloud — keep
it on a trusted LAN, not the public internet.

- [1. Deploy the server on a LAN machine](#1-deploy-the-server-on-a-lan-machine)
- [2. Keep it running (macOS launchd)](#2-keep-it-running-macos-launchd)
- [3. Back up the data](#3-back-up-the-data)
- [4. Sharing the macOS app](#sharing-the-macos-app)
- [5. Publishing to GitHub (a different account)](#5-publishing-to-github-a-different-account)

---

## 1. Deploy the server on a LAN machine

On the machine that will host it (Go 1.25+, Node 20+, Docker installed):

```sh
# get the code (after it's on GitHub — see section 5)
git clone <your-repo-url> taskman && cd taskman

make mongo                     # start MongoDB (Docker, restart: unless-stopped)
make ui                        # build the SPA into ui/dist
ADMIN_EMAIL=you@company.com \
  go build -o taskman-bin ./server && ./taskman-bin   # or: make run
```

The server listens on `:8484` on **all interfaces**, so it's already reachable across
the LAN — no extra binding needed.

**Find the server's LAN address:**

```sh
ipconfig getifaddr en0        # macOS, e.g. 192.168.1.8   (Wi-Fi is often en0/en1)
hostname -I                    # Linux
```

Teammates then use `http://<that-ip>:8484` (browser) or set the same address in the
macOS app's settings.

**Open the firewall** if needed: macOS may prompt to allow incoming connections the
first time `taskman-bin` runs — allow it. (System Settings → Network → Firewall →
Options, allow `taskman-bin`.) Make sure TCP **8484** is reachable on the LAN.

**Bootstrapping the admin:** the first run with `ADMIN_EMAIL` set creates the admin.
If you didn't set `ADMIN_PASSWORD`, grab the generated one from the server output. See
the README's "First run" section.

---

## 2. Keep it running (macOS launchd)

`make run` dies when you close the terminal or log out. To keep the server up across
reboots, install a LaunchAgent. Mongo already restarts on its own (Docker
`restart: unless-stopped`), as long as Docker Desktop is set to start at login.

Create `~/Library/LaunchAgents/com.pbhealth.taskman.plist` (edit the two paths and the
email):

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.pbhealth.taskman</string>
  <key>ProgramArguments</key>
  <array><string>/Users/YOU/taskman/taskman-bin</string></array>
  <key>WorkingDirectory</key><string>/Users/YOU/taskman</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>ADMIN_EMAIL</key><string>you@company.com</string>
    <key>PORT</key><string>8484</string>
  </dict>
  <key>KeepAlive</key><true/>
  <key>RunAtLoad</key><true/>
  <key>StandardOutPath</key><string>/Users/YOU/taskman/server.log</string>
  <key>StandardErrorPath</key><string>/Users/YOU/taskman/server.log</string>
</dict>
</plist>
```

Build the binary first (`go build -o taskman-bin ./server`), then:

```sh
launchctl load  ~/Library/LaunchAgents/com.pbhealth.taskman.plist    # start + enable
launchctl unload ~/Library/LaunchAgents/com.pbhealth.taskman.plist   # stop
```

`WorkingDirectory` must be the repo root so the server finds `ui/dist`. On Linux, use a
systemd unit with the same idea (`WorkingDirectory`, `Environment=`, `Restart=always`).

---

## 3. Back up the data

All data lives in the Docker volume `taskman-mongo`. Dump it periodically:

```sh
docker exec taskman-mongo-1 mongodump --db taskman --archive > taskman-$(date +%F).archive
# restore:
docker exec -i taskman-mongo-1 mongorestore --archive < taskman-YYYY-MM-DD.archive
```

---

## Sharing the macOS app

The app is a thin client — it needs the server (section 1) running to be useful.

**Build it** (on an Apple Silicon Mac; see the arch note below):

```sh
make up          # or at least: server running + Mongo up
make mac-build    # -> src-tauri/target/release/bundle/dmg/taskman_<ver>_aarch64.dmg
                  #    and  src-tauri/target/release/bundle/macos/taskman.app
```

**Hand it to a teammate:** send them the `.dmg` (AirDrop / shared drive / Slack). They
drag `taskman.app` to Applications.

**Get past Gatekeeper** — the build is **unsigned**, so macOS will refuse it on first
open. Either:
- Right-click the app → **Open** → **Open** (only needed once), **or**
- Remove the quarantine flag after copying:
  ```sh
  xattr -dr com.apple.quarantine /Applications/taskman.app
  ```

**Point it at the server:** on first launch the app tries `http://localhost:8484`. On a
teammate's machine that's wrong — the app shows a "can't reach" screen; enter the
server's LAN address (e.g. `http://192.168.1.8:8484`) in the settings field and save.
Plain `http` to a private LAN address is allowed (the app ships an ATS exception for
localhost + private ranges).

**Arch note:** `make mac-build` produces an **aarch64 (Apple Silicon)** build. If a
teammate has an Intel Mac, build a universal/x86 target for them
(`cargo tauri build --target universal-apple-darwin`, which needs the extra Rust
targets installed).

**Proper distribution** (no right-click dance, no quarantine step) requires signing +
notarization with an Apple Developer account ($99/yr) — out of scope for an internal
tool, but that's the upgrade path if you outgrow the manual step.

---

## 5. Publishing to GitHub (a different account)

Two independent things are involved: **who the commits are attributed to** (git identity)
and **which GitHub account you push with** (authentication). They don't have to match.

### One-time repo hygiene (already done in this repo)
- `.gitignore` excludes build output (`ui/dist`, `src-tauri/target`, `node_modules`,
  binaries), secrets (`.env`), and the raw design-tool handoff bundles.
- No credentials are committed (the server reads them from env; the seed password was
  scrubbed from the docs).

### Set the commit identity (repo-local, independent of the GitHub account)
```sh
cd taskman
git config user.name  "Amit Bind"
git config user.email "amit.bind@pbhealth.com"
```

### Make the first commit
```sh
git add -A
git commit -m "Initial commit: taskman (server, UI, macOS app, docs)"
git branch -M main
```

### Create the repo on the target account + push
The tricky part is pushing with an account **different** from whatever git/gh already has
cached on this machine. Pick one:

**Option A — GitHub CLI (cleanest for multiple accounts):**
```sh
gh auth login          # choose GitHub.com → HTTPS → log in as the TARGET account
                        # (run this yourself; it's interactive — in Claude Code prefix with `! `)
gh auth switch          # if the CLI has several accounts, switch to the target one
gh repo create <target-account>/taskman --private --source=. --remote=origin --push
```
That one `gh repo create` makes the repo on the target account **and** pushes, using that
account's credentials.

**Option B — HTTPS + a Personal Access Token:**
1. On the target account: Settings → Developer settings → **Personal access tokens** →
   generate one with the `repo` scope.
2. Create an empty repo named `taskman` on that account via the website (don't add a
   README — this repo already has one).
3. Push, using the target username + the PAT as the password when prompted:
   ```sh
   git remote add origin https://<target-account>@github.com/<target-account>/taskman.git
   git push -u origin main
   ```
   Putting `<target-account>@` in the URL tells the credential prompt which account to
   use, so the machine's *other* cached GitHub credential doesn't get picked silently.

**Option C — SSH with a per-account key:** if you keep separate SSH keys, add a host
alias in `~/.ssh/config` (e.g. `Host github-personal` → its `IdentityFile`) and use
`git@github-personal:<target-account>/taskman.git` as the remote.

### Verify
```sh
git remote -v
git log --oneline -1
```
and confirm the repo shows up under the intended account on github.com.
