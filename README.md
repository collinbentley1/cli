# ot

**Opinionated tooling for agentic engineering.**

Every agent worktree gets its own database, ports, secrets scope, and a live
MCP tunnel.

`ot` is a dev-runtime CLI for repos where multiple coding agents (and humans)
work in parallel git worktrees. Check out N branches into N worktrees, run
`ot up backend` in each, and every worktree gets:

- **Its own Postgres.** A dedicated Docker container per worktree
  (`app-postgres-<instance-id>`), with a generated password, created and
  migrated on first `ot up`.
- **Its own port block.** A deterministic, collision-checked block of local
  ports (backend, auth, frontends, CRM, docs, fixture site) so stacks
  never fight over 8005.
- **Its own secrets scope.** Commands re-exec under the secrets provider
  (Infisical ships as the default) so the mapped secret folder is injected
  into the child process environment — agents never see plaintext env files.
- **Its own public MCP tunnel.** The backend's MCP endpoint is published at
  `https://<machine>.ts.net/_ot/<instance-id>/mcp` via Tailscale Funnel,
  path-mounted per worktree. N agents on N branches are each testable from a
  real ChatGPT/Claude client concurrently.

The isolation profile is written to `.ot/local-dev-profile.json` inside each
worktree and reapplied after secret injection, so worktree-local values win
over shared secrets. Set `OT_WORKTREE_ISOLATION=0` to opt out, or
`OT_WORKTREE_REASSIGN_PORTS=1` once to allocate a fresh port block.

## Status

Extracted from a system that ran inside OTseek's monorepo,
where it hosted the agent-team workflow that shipped a multi-tenant MCP-apps
platform. This standalone cut is a
pre-release extraction candidate: the code is lifted intact, org-specific
names are genericized, and the three external integrations are behind
pluggable provider config. Service commands still encode the source repo's
conventions (see "Repo conventions" below). Not yet released or versioned.

## Try it from a fresh clone

The vendored install path below is the real workflow, but you can exercise
the CLI without installing anything or touching Infisical, Porter, or
Tailscale. The only prerequisite is Go (`go.mod` says 1.25.5; this
walkthrough was run with go 1.26.3 on macOS/arm64). Every command and output
below was run verbatim, in order, from a fresh clone.

### Build and poke the binary

```bash
git clone https://github.com/collinbentley1/cli.git
cd cli
go build -o ot-bin ./ot
./ot-bin version
```

```
ot version dev (darwin/arm64) [unstamped]
Built without a version stamp (plain `go build`); ot/scripts/install.sh stamps the git SHA.
```

(`[unstamped]` is expected from a plain `go build`: only `install.sh` stamps
the git SHA into the binary, and there is nothing to compare against without
it. Nothing is wrong.)

`./ot-bin --help` prints the command tree (trimmed here):

```
ot — per-worktree dev runtime (isolated DB, ports, secrets scope, MCP tunnel)

Usage:
  ot [command]

Available Commands:
  bootstrap          Install and configure ot dependencies
  check              Run pre-commit hooks for a target
  ...
  up                 Start dev stack or individual services
  upgrade            Upgrade dependencies and rebuild ot CLI
  version            Print ot CLI version
```

Read-only commands like `version` need none of the provider CLIs — the
machine this ran on has no Tailscale installed, and the clone's own
`ot/ot.yaml` (which selects the full infisical/porter/tailscale set) loads
fine.

### A minimal sandbox repo

`ot` runs against a git repo containing `ot/ot.yaml`. With all three
providers set to `none`, no external service or account is needed:

```bash
cd ..
mkdir ot-sandbox && cd ot-sandbox
git init -q
mkdir ot
cat > ot/ot.yaml <<'EOF'
version: 1
providers:
  secrets: none
  db_tunnel: none
  mcp_tunnel: none
EOF
git add ot/ot.yaml && git commit -qm "ot config"
```

The commands below run the binary built above via `../cli/ot-bin`.

**Config is validated up front.** Break the version field and every command
fails fast:

```bash
sed -i '' 's/^version: 1/version: 2/' ot/ot.yaml
../cli/ot-bin version
# unsupported ot/ot.yaml version: 2 (expected 1)        exit 1
sed -i '' 's/^version: 2/version: 1/' ot/ot.yaml        # restore
```

**Provider names are validated too, and environment overrides beat the
yaml:**

```bash
OT_SECRETS_PROVIDER=vault ../cli/ot-bin version
# unknown provider "vault" for OT_SECRETS_PROVIDER (valid: infisical, none)   exit 1
```

**Argument typos fail instantly**, before any bootstrap or network work:

```bash
../cli/ot-bin check bogus
# unknown check target: bogus                           exit 1
```

**Worktree isolation.** In a primary checkout isolation is off (no isolation
profile is written). In a linked git worktree, the first run allocates an
instance id
and a collision-checked port block:

```bash
git worktree add -q ../ot-sandbox-wt
cd ../ot-sandbox-wt
../cli/ot-bin version          # first run writes .ot/local-dev-profile.json
grep -E '"(instance_id|offset|backend)"' .ot/local-dev-profile.json
```

```
  "instance_id": "4e87f6b168",
  "offset": 164,
    "backend": 23285,
```

A second worktree of the same repo gets a disjoint block:

```bash
cd ../ot-sandbox
git worktree add -q ../ot-sandbox-wt2
cd ../ot-sandbox-wt2
../cli/ot-bin version >/dev/null
grep -E '"(instance_id|offset|backend)"' .ot/local-dev-profile.json
```

```
  "instance_id": "95b7dd92fb",
  "offset": 640,
    "backend": 32805,
```

The full profile also records per-worktree container names and generated
local DB passwords (elided here). `OT_WORKTREE_REASSIGN_PORTS=1` forces
reallocation; allocation is deterministic by path, so with no competing
block registered it hands the same offset back.

**Providers set to `none` refuse rather than half-run:**

```bash
cd ../ot-sandbox
OT_SKIP_SELF_CHECK=1 ../cli/ot-bin up db
# db tunnel provider is disabled (db_tunnel resolves to none — check OT_DB_TUNNEL_PROVIDER and providers.db_tunnel in ot/ot.yaml); enable porter or run your own tunnel
#                                                       exit 1
```

Two caveats on that last command. `OT_SKIP_SELF_CHECK=1` stops the unstamped
binary from self-installing (rebuild plus symlink into
`$(brew --prefix)/bin`) before the command runs. And `up` runs a runtime
preflight that checks for tmux, overmind, and porter, installing missing
ones via Homebrew — on the machine this walkthrough ran on they were already
present, so the preflight was check-only.

Cleanup:

```bash
cd ..
rm -rf cli ot-sandbox ot-sandbox-wt ot-sandbox-wt2
```

## Quickstart (vendored)

`ot` is designed to be vendored into the repo it manages, the way it ran in
its source monorepo: the `ot/` directory and `go.mod` live at your repo root,
and the CLI rebuilds itself from source when its inputs change.

```bash
# 1. Copy ot/ and go.mod into your repo root, then:
./ot/scripts/install.sh   # macOS/arm64: installs Homebrew + Go if needed, builds ot,
                          # symlinks it into $(brew --prefix)/bin, runs bootstrap

# 2. Configure providers and secret scopes in ot/ot.yaml (see below).

# 3. Start things.
ot up                # full dev stack (backend + worker + auth + frontend)
ot up backend        # backend web + worker + per-worktree MCP tunnel
ot logs backend      # stream logs
ot check backend     # run the backend pre-commit hook set
ot down              # stop everything
```

To build just the binary in this repo: `go build -o ot-bin ./ot`.

Host requirements: macOS (Apple Silicon) for the full bootstrap path, or a
Linux cloud-agent environment with a pared-down toolchain install. OpenAI
Codex cloud tasks and Anthropic Claude Code on the web are detected via
their environment (`CODEX_ENV_*`, `CLAUDE_CODE_OAUTH_TOKEN`); set
`OT_CLOUD_AGENT=1` or `OT_CLOUD_AGENT_HOST=codex|claude` to force or correct
detection. `ot bootstrap` installs the rest: tmux, overmind, uv, node via
nvm, Docker, and the provider CLIs.

## Providers

Porter, Infisical, and Tailscale are pluggable behind config. The interfaces
live in `ot/providers`; the three shipped implementations are the defaults.

```yaml
# ot/ot.yaml
version: 1
providers:
  secrets: infisical    # or "none"
  db_tunnel: porter     # or "none"
  mcp_tunnel: tailscale # or "none"
```

Environment overrides (useful per-shell or in CI): `OT_SECRETS_PROVIDER`,
`OT_DB_TUNNEL_PROVIDER`, `OT_MCP_TUNNEL_PROVIDER`. Unknown names fail fast at
startup. `none` disables the slot: secrets wrapping becomes a no-op, `ot up
db` refuses with a clear error, and the MCP tunnel is skipped.

### Secrets: Infisical (default)

Commands re-exec under `infisical run` with the secret path mapped for that
command, so secrets exist only in the child process environment.

- Select your project with a repo-root `.infisical.json`:

  ```json
  { "workspaceId": "<your-infisical-project-id>" }
  ```

- Map command targets to secret folders in `ot/ot.yaml` under
  `infisical.paths` (`up`, `check`, `hook`). One path per command.
- Interactive use: `infisical login` (ot launches it for you, file-locked so
  concurrent worktrees don't both open a browser).
- Agents/CI: set `INFISICAL_UNIVERSAL_AUTH_CLIENT_ID` and
  `INFISICAL_UNIVERSAL_AUTH_CLIENT_SECRET` (machine identity), or a static
  `INFISICAL_TOKEN`. Self-hosted: `INFISICAL_API_URL`. Multi-project:
  `INFISICAL_PROJECT_ID`.
- Long-running services run under `infisical run --watch`, so secret changes
  restart them.

### DB tunnel: Porter (default)

`ot up db` opens authenticated tunnels to remote datastores through the
Porter CLI (`porter datastore connect`), writing connection strings to
`.ot/connections/`.

```yaml
porter:
  ports:
    app: 8150          # local port for the tunnel
  envs:
    nonprod:
      app_datastore: nonprod-primary-app-pgsql  # your Porter datastore name
```

Auth is handled with `porter auth login` on first use (re-run automatically
when a tunnel fails with an auth error).

### MCP tunnel: Tailscale Funnel (default)

`ot up backend` publishes the backend MCP endpoint on your machine's public
`*.ts.net` domain. Funnel must be enabled for your tailnet; if it is not, ot
prints the enable URL. Per-worktree mounts use `--set-path=/_ot/<id>` so a
machine can serve many worktrees at once. Tenant apps from the backend's
catalog get their own URLs, exported as `<TENANT_SLUG>_MCP_PUBLIC_URL`.

Controls: `OT_BACKEND_MCP_TUNNEL=0` disables the tunnel for one run;
`OT_BACKEND_MCP_TUNNEL_PATH=/custom` overrides the mount path;
`TAILSCALE_BIN` points at a non-standard CLI location. MagicDNS is suspended
while a tunnel runs (so browsers resolve the funnel through public DNS) and
restored on shutdown.

## Commands

- `ot up [backend|backend-worker|auth|crm|frontend|wordpress|db]` — no
  argument starts the full dev stack
- `ot down [service]` / `ot logs
  [backend|backend-worker|crm|crm-web|crm-worker|frontend|wordpress|db]`
- `ot check [backend|auth|crm|frontend|ot|skills|wordpress|datadog|mcp]` — no
  argument runs every hook set
- `ot check mcp` — local MCP test harness against the running backend
- `ot datadog-hook` — run the Datadog static-analyzer hook directly
- `ot bootstrap` — full dependency install (tools, hooks, project deps)
- `ot upgrade [all|frontend|backend|ot]` — cooldown-gated dependency upgrades
  (24h release cooldown, GuardDog verification for Go modules, sfw-wrapped
  npm/uv calls)
- `ot selfcheck` / `ot version` / `ot reset`

Process management is overmind + tmux on generated procfiles under
`.ot/overmind/`.

## Repo conventions

The service commands encode the layout of the monorepo this was extracted
from. To use them as-is, your repo needs:

- `backend/` — Python service run with `uv` (uvicorn `server.main:app`,
  worker `server.worker.main`, migrations via `scripts.migrate`)
- `crm/` — optional second service (`uv run crm-web` / `crm-worker`)
- `frontend/` — npm workspaces (`marketing`, `auth`, `app`, `docs`) fronted
  by ot's built-in path router on port 3000
- `harness/` — optional MCP test harness (`uv run mcp-test-harness`)
- `ot/scripts/wordpress_dev.sh` — optional project-provided CMS fixture hook.
  Contract: `start [--reset] | stop | logs`; on start it writes
  `.ot/wordpress/backend-env.sh` with `export`s the backend should see. The
  original tenant-specific fixture script is not shipped. `ot up backend`
  runs this hook first when the script exists (and skips it with a notice
  when it doesn't); set `OT_BACKEND_SKIP_WORDPRESS=1` to skip it explicitly.

Adapting the command set to a different layout means editing `ot/backend`,
`ot/crm`, and the procfile templates in `ot/cli/cmd_up.go` — they are plain
Go and small.

## Development

```bash
go build ./...
go test ./...
golangci-lint run ./...
```

## License

MIT — see [LICENSE](LICENSE).
