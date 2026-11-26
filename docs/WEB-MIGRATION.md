# Web migration: progress and handoff

Status log for moving Ragnarok of Building to the browser. Read this first when picking the work up (person or Claude session). Branch: **`web`**. Last updated 2026-11-26 (step 3).

## Goal

Run **the same Gio app**, compiled to WebAssembly, as a static site on **GitHub Pages**. The old HTMX server (`legacy-web` branch) is not revived. Originally a Cloudflare Worker was to proxy Divine Pride icons and paste.rs; **dropped** (see step 4). The desktop program must keep working as it does today.

## Branches

| Branch | What it is |
|---|---|
| `main` | Desktop program; also opens on Linux/macOS since `7c77e9b`. |
| `desktop` | Snapshot of the desktop program before the web work (`7c77e9b`). |
| `web` | This migration. Merge into `main` when the web version ships. |
| `legacy-web` | The retired HTMX server version. Not used. |

## Where things stand

| Step | Status | Commit |
|---|---|---|
| 1. Refactors with no web code (store, files as bytes, icons as bytes, `gamedata.Read`) | ✅ done | `7d51675` |
| 2. `cmd/web` + `web/index.html`, icons off, `make web-serve` | ✅ done, runs in Chrome (see "Verified") | `dee9a57` |
| 3. `yield()` so the optimizer doesn't freeze the page | ✅ done | this commit |
| 4a. Share links (`#b=`), no paste on web | ✅ done | this commit |
| 4b. Icons | ⏳ **needs a decision** (see step 4) | |
| 5. GitHub Pages workflow + docs + README link | ⏳ | |

## Testing in a browser without the Chrome extension

Headless Chrome's `--virtual-time-budget` does not wait for CPU work, so screenshots come out on the loading screen. What worked: start `google-chrome --headless=new --remote-debugging-port=9333 --enable-unsafe-swiftshader --use-angle=swiftshader`, and drive it with a ~40-line Node 24 script (built-in `WebSocket`) over the DevTools protocol: `Page.navigate`, wait ~15 s, `Input.dispatchMouseEvent`/`dispatchKeyEvent`/`insertText`, `Runtime.evaluate`, `Page.captureScreenshot`. To measure responsiveness, inject a `requestAnimationFrame` counter that records the longest gap between frames. Window coordinates at 1440×900: Otimizador button (85,212), Calcular (601,219), FOR field (228,196).

## How to run it

```bash
git switch web
make web          # builds dist/web: app.wasm, wasm_exec.js, index.html, icon.png, catalog.json.gz
make web-serve    # builds, then serves dist/web on http://localhost:8080
make vet          # vets desktop + GOOS=js GOARCH=wasm for internal/... and cmd/web
make run          # the desktop program, unchanged
```

## What was done

### Step 1 — `7d51675` "Read and write files by contents, through a store"
- `internal/store`: `Store` interface (`Read(key)`, `Write(key, b)`). `store.Dir(dir)` is the desktop version (temp file + rename, same files as before).
- `ui.App`: `StateFile`/`SavedFile` replaced by `Store`; keys are `ui.StateKey` = `data/ui.json` and `view.SavedKey` = `builds.json`. `NewApp(v, th, store)`, `UseSavedTheme(store)`.
- `App.OpenFile` now returns `(name, data []byte, err)`; new `App.SaveFile(name, data) (where, err)` for the builds export. Desktop versions in `cmd/app/files.go` (read capped at 16 MB + 1).
- `importReplayFile` / `importSavedFile` take `[]byte`.
- Icons: `Icons.Get` and `view.IconCache.Get` return PNG bytes; the disk cache is unchanged.
- `gamedata.Read(io.Reader)`; `gamedata.Load(path)` still works and calls it.

### Step 2 — web entry point
- `internal/store/browser.go` (`//go:build js`): `store.Browser()` on `localStorage`, keys `rob:<key>`; errors instead of panics when storage is blocked or full.
- `cmd/web/main.go`: same flow as `cmd/app/native.go`: startup screen → download `catalog.json.gz` (MB progress) → `gamedata.Read` → `ui.NewApp(store.Browser())`. Removes the page's `#loading` on the first frame. `DestroyEvent` saves but never exits.
- `cmd/web/browser.go`: `pageURL` (relative to the page, so a Pages subfolder works), `fetch` with progress, `openFile` (hidden `<input type=file>`, honours `cancel`, reads at most 16 MB + 1), `saveFile` (Blob + `<a download>`), `acceptOf` (`"Replays (*.rrf)|*.rrf"` → `.rrf`).
- `gamedata.Read` accepts gzipped **or** plain JSON, in case a server unzips the `.gz` on the way (`internal/gamedata/read_test.go`).
- `web/index.html`: loading screen with download bar for `app.wasm`, error message on failure, `#giowindow` host, and a capture-phase `keydown` listener that `preventDefault`s **Alt+letter** so the browser doesn't take the program's shortcuts (Gio itself doesn't prevent them).
- `tools/webserve`: tiny static server (`application/wasm`, `Cache-Control: no-store`).
- `Makefile`: `web`, `web-serve`; `vet` also vets the wasm build.

### Step 3 — page stays responsive during searches
- `internal/view/yield.go` (no-op off the browser) and `yield_js.go`: after each ~30 ms of work, wait for one `requestAnimationFrame` (or 50 ms if the tab is hidden and frames stop). A plain `time.Sleep(1ms)` is not enough: Gio paints only on animation frames, and a 1 ms sleep usually ends before one.
- Called once per candidate in the six worker pools: `optimize.go`, `swap.go`, `best.go`, `bestcard.go`, `traits.go` (`evalAll`), `optextra.go` (`runPar`). The UI already runs all of these in goroutines, never inside a frame.
- Measured in headless Chrome: optimizer (Dano, default options) finishes in < 5 s, page paints ~40 fps meanwhile, longest gap 260 ms; with "Considerar mudar os talentos" ~47 fps, longest gap 359 ms. Results match the desktop's shape (full list, traits suggestion).

### Step 4a — share links without paste.rs
- **No Worker.** Checked 2026-11-26: `static.divine-pride.net` now answers **403 to every request**, including the desktop program's own (`User-Agent: ragnarok-of-building`) — desktop icons are broken too. Divine Pride is refusing on purpose; a Worker that poses as a browser to get around that is not something we do. paste.rs sends no CORS headers (preflight 404).
- Share on web = the page's own link: `https://<site>/#b=<code>` (`cmd/web` `shareURL`). The fragment never reaches a server; a typical code is ~180 chars.
- `App.ShareURL` (nil on desktop) adds "Link (abre no navegador)" to the Share window, default on web. `App.NoPaste` (web) hides the paste option and answers pasted paste.rs links with a clear message.
- `cmd/web` `openLinkedBuild`: opens `#b=` (and old `?b=`) at load and on `hashchange`, through `App.OpenShared` (runs on the UI goroutine), then `history.replaceState` drops the code so a reload keeps the player's edits.
- `codeInLink` (ui and view) accepts `#b=` as well as `?b=`.
- Verified in headless Chrome: replay build's code → fresh storage → open `/#b=<code>` → the replay build (FOR 120, +9 Faca, 613 dmg) shows and the address bar is back to `/`. Share window shows the Link option. **Not verified:** the copy to clipboard (headless has no clipboard).

### Review fixes (code review of the PR `edudebortoli/ragnarok-of-building` main ← `Loonguinho:web`)
- **Open errors were silent**: `App.openFile` now shows a failed read in the chat; a cancelled picker returns `ui.ErrNoFile` and stays quiet (desktop and web hosts return it).
- **Picker could stick forever** (no `cancel` event in Safari < 16.4): the web `openFile` also treats "page got focus back, no file chosen 1 s later" as a cancel; listeners are removed before release. Verified: after a `cancel` event, and after a `focus` event, the hidden input is gone and the next click opens a new picker.
- **Last change lost on tab close**: `cmd/web` `saveOnHide` calls `SaveNow` on `visibilitychange` (hidden) and `pagehide`. Verified: edit a stat, navigate away immediately, come back — the edit is there.
- **yield**: all searches waiting at once share one frame (`nextFrame` channel closed by the frame), so two concurrent searches can't steal each other's frame and all of them stop so the page can draw. Re-measured: ~47 fps, longest gap 239 ms.
- Save error no longer names `builds.json` ("Não consegui gravar os builds."; EN/ES updated); a builds backup over 16 MB gets the size message instead of a JSON error.
- Replay import verified in the browser through the real picker (CDP `Page.setInterceptFileChooserDialog` + `DOM.setFileInputFiles` with `internal/replay/testdata/synthetic.rrf`).

**Desktop is discontinued** (decision 2026-11-26): the `desktop` branch is kept as a snapshot; test and verify only the web build from now on.

## Verified (2026-11-26, headless Chrome on Linux, driven over the DevTools protocol)
- `go vet` + `go test ./...` pass; vet passes for Linux, `GOOS=windows`, and `GOOS=js GOARCH=wasm`.
- Desktop program still starts (Linux, `go run ./cmd/app`).
- Web: `app.wasm` loads, startup screen shows "Baixando os dados do jogo… 4.2 MB" then "Preparando…", the full simulator draws (~10 s on swiftshader).
- Typing a stat works (FOR → 130, damage 159 → 427).
- **Alt+D** reaches the program (opens Alvo/Dano).
- State persists across reload (`rob:data/ui.json` in localStorage; FOR 130 and the open window come back).

## Not yet tested (do these by hand in a real browser)
1. **Alt shortcuts in a real Chrome/Firefox window**: synthetic keys from DevTools never trigger browser shortcuts, so check that Alt+D doesn't jump to the address bar and Alt+E/Alt+F don't open menus (Windows/Linux).
2. **Import replay** in a real browser (headless already passed): the picker shows only `.rrf`; Cancel does nothing and a second click still works — try Safari too, it is the browser the focus fallback is for.
3. **A file over 16 MB** → "Arquivo grande demais (máx. 16 MB)."
4. **Builds**: Salvar, rename, delete; reload and they're still there.
5. **Export builds** → a `builds-ragnarok-of-building-<date>.json` download; **Import** it back.
6. **Theme and language** persist after reload (Configurações).
7. **Firefox** as well as Chrome (WebGL, file picker `cancel` event).
8. **Private window**: the app should still open (storage errors are ignored, nothing saved).
9. **Optimizer** on a slow machine/phone: the page should keep repainting (hover, scroll) while it runs; note the 30 s timeout may give partial results more often than on the desktop (one core instead of all).

## Known limits / decisions
- **Size**: `app.wasm` is 27 MB (6.8 MB gzipped) + 4.3 MB catalog ≈ 11 MB first visit if the host gzips. **Check GitHub Pages actually gzips `.wasm`**; if not, fall back to Cloudflare Pages or ship a pre-gzipped file decompressed with `DecompressionStream`.
- **One thread**: Go in wasm has no preemption, so CPU work freezes drawing unless it calls `yield()`. Catalog decode takes ~1–2 s (a status line is drawn first). Searches yield (step 3); other one-off loops (`Compare`, `CompareSets`, `BestSetEnchants`) do not yet — add `yield()` there if they feel stuck.
- **Searches use one core** in the browser (`runtime.NumCPU()` = 1), so they are slower than the desktop; the optimizer's 30 s timeout may cut more searches short.
- **Icons off** on web until the Worker exists (`a.Icons` is nil; the UI handles nil).
- **Fonts**: no Tahoma in the browser; Go fonts are used (as on Linux).
- **localStorage** is ~5 MB per site; builds are small, but a write error is shown if full.
- Memory: catalog ~64 MB live, ~115 MB peak while loading.

## Next steps (detail)

### Step 4b — icons (decision pending)
Divine Pride is closed to us, so icons must come from somewhere we may use. Options:
1. **Extract from the LATAM client** (recommended): item icons and item art are in `data.grf` under `data\texture\유저인터페이스\item\` and `...\collection\` (BMP, keyed by the item's resource name, which the catalog import already reads). Add a `cmd/import` step that writes them as PNGs, ship them with the site (one sprite sheet or a folder), serve with `view.NewWebIcons`. Needs the client, so it runs on the Windows PC. Skill icons are there too; monster pictures are sprites (`.spr`), harder — could stay text-only. Same legal footing as the catalog the repo already ships (game data © Gravity, README says so).
2. **Ask Divine Pride** for permission / an API for images.
3. **No icons** on web (works today; the UI copes).
The `Icons.Get` hook already takes bytes, so any source plugs in.

### Step 5 — publish
- `.github/workflows/web.yml` on `v*` tags + manual dispatch: checkout, setup-go, `go test ./...`, `make web`, `actions/upload-pages-artifact` (`dist/web`), `actions/deploy-pages`; permissions `pages: write`, `id-token: write`.
- One-time: repo Settings → Pages → source "GitHub Actions".
- README: "▶️ Abrir no navegador" link next to the Windows download. `docs/DESENVOLVIMENTO.md`: web build, Worker, limits (single thread, data per browser, no client import).
