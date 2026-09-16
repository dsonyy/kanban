# AGENTS.md

## Always

- The default language of this project is English: code, comments, UI text, commit messages and new documentation. Earlier phase notes in `docs/phases/` are in Polish and stay as they are.
- Reproduce a bug with a failing end-to-end test before fixing it, and check the test fails without the fix.
- Change state files only through the store (`store.write`, `store.update`). They check the disk for concurrent hand edits before writing.
- Run `go test ./...` before committing.

## Never

- Never edit `web/dist/` or `web/node_modules/`. Both are generated.
- Never store data models as JSON. State is YAML and Markdown on disk. The only JSON file is `~/kk/hooks/claude.json`, because Claude Code requires it.
- Never write to harness configuration such as `~/.claude.json` or `~/.codex/config.toml`. Running agents rewrite those files.

## What is this project

kk is a kanban board for running coding agents. Each column runs a sequence of steps (shell commands, agents, human approvals, jumps to other columns) when a task enters it. Tasks run in their own tmux sessions and git worktrees. Everything that needs a human lands in one feed. The board lives in plain files under `~/kk`, so people and agents can edit it directly.

Specification and plans: `~/brain/projects/agent-kanban/`. Per-phase plans and results: `docs/phases/`.

## Stack and architecture

- `app/` - the Go server and CLI in one binary. Running `kk` with no arguments starts the server; with arguments it is a client that talks to the running server over a unix socket. CLI arguments map 1:1 to REST paths (`grammar.go`).
- `web/` - the browser client. Go templates in `web/templates/` render the pages. Scripts and styles are a Vite + TypeScript project (`web/src/`, `web/public/`), built into `web/dist/` and embedded into the binary through `web/embed.go`. In dev mode the pages load scripts from the Vite dev server instead.
- `web/src/editor.ts` is the only module that knows the markdown editor library (Milkdown Crepe); the rest of the client uses its `mountMarkdownEditor` interface.
- State on disk: `~/kk/projects/<project>/{project.yaml,board.yaml,items/N.{yaml,md,log.yaml}}`. The server keeps a mirror of state files in memory (`mirror.go`), synced by a file watcher and a rescan every 30 s.
- Runner (`runner.go`): a reconcile loop every 500 ms that drives tmux (`tmux -L kk`) to match task state. It survives server restarts.
- Harness integrations (`harness.go`): Claude Code and Codex, with hooks that call `kk hook <event>`.
- History: `~/kk` is a git repository with auto-commit every 2 s and undo.

## Vocabulary

- **Task / item** - a card. `N.md` is its content, `N.yaml` its state, `N.log.yaml` its append-only event log.
- **Column** - a board column with a list of steps in `board.yaml`.
- **Step** - `shell`, `agent`, `human`, `goto` or `setup`.
- **Harness** - how an agent step runs: `raw` (a shell command), `claude` or `codex`.
- **Attention / ACTION REQUIRED** - the `attention` field of a task: something waits for a human. It feeds the feed tab and ntfy pushes.
- **Mirror** - the in-memory copy of state files. **Writer** - the single write path that refuses to overwrite a newer file on disk.
- **Settled** - a task in `done`, `waiting` or `failed` that the runner skips until something changes it.

## Testing

- `go test ./...` runs end-to-end tests against the real binary, a real unix socket and an isolated tmux server. About 3.5 minutes. Requires `tmux` and `git`.
- Agent tests use fake `claude` and `codex` scripts (`app/harness_test.go`) that call the real hook CLI and write transcripts in the real formats.
- `docs/phases/break-log.md` records attempts to break the app and the tests they produced.

## Commands

- Build everything: `just build` (runs `npm run build` in `web/`, then `go build -o kk ./app`)
- Develop: `just dev` (Vite with hot reload, server with `KK_DEV=1`, opens Chrome)
- Type-check the client: `just typecheck`
- Run the server: `./kk` (prints the web URL with a token)
- Use a separate data directory: `KK_HOME=/tmp/k KK_ADDR=127.0.0.1:7421 KK_TMUX=kk-dev ./kk`
- Tests: `go test ./...`
- One test: `go test ./app -run TestWorkflow`

## Conventions

- Go standard library first. Dependencies: `go.yaml.in/yaml/v3`, `fsnotify`, `creack/pty`, `coder/websocket`, `goldmark`.
- No comments that restate code. Comments explain non-obvious reasons only.
- UI: rectangles, black and greys, no rounded corners, colors only as CSS variables in `web/static/app.css`.
- Buttons are always dark, never filled white. The main action of a group gets `.primary`: white border instead of grey. A box that asks for a decision uses `.box-warn` (dark orange background and border), with its buttons right-aligned.
- Two fonts: `--font-ui` (sans-serif) for all interface text, `--font-mono` only for paths, code, commands, ids and file names (class `mono`).
- Icons are flat single-color SVGs in `web/public/icons/`, drawn with `<i class="icon icon-NAME">` and colored by `currentColor` through a CSS mask.
- Every button and input label gets a keyboard mnemonic automatically (`app.js`): its first free meaningful letter is underlined and pressing it activates the control. Do not hard-code mnemonics.
- Sentence case in UI text and docs.
