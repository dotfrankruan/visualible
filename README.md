# Visualible

**Visual + Ansible.** A visual IDE and deployment control plane for Ansible,
distributed as a single binary.

Visualible lets you visually construct Ansible playbooks, inspect and edit the
generated YAML, configure inventories and credentials, and execute deployments
against remote hosts through Ansible — while Ansible remains the actual
execution engine.

It is a lightweight native infrastructure tool, not a SaaS dashboard:

- **Go backend**, standard `net/http`, SQLite for state, `go:embed` for the UI.
- **No JavaScript framework, no Node.js toolchain** — the frontend is plain
  HTML/CSS/ES modules.
- **One executable** is the whole distribution.

## Build & run

Requires Go 1.22+. Ansible is detected at runtime from `PATH` (optional but
needed for module discovery and deployment).

```bash
go build ./cmd/visualible
./visualible
# Visualible v0.1.0
# Ansible: 2.x detected (/usr/local/bin/ansible)
# Database: ~/.config/visualible/visualible.db
# Listening: http://127.0.0.1:8080
```

Configure with flags or environment variables:

```bash
./visualible -addr 127.0.0.1 -port 8080 -data-dir ~/.config/visualible
# or VISUALIBLE_ADDR / VISUALIBLE_PORT / VISUALIBLE_DATA_DIR
```

### Logging

The console reports every level with standard severity ranks, so output can
be filtered and parsed by ordinary tooling:

```text
2026-10-03T19:42:11.123+02:00 INFO  ansible detected         version=2.21.4 modules=8862
2026-10-03T19:42:11.201+02:00 DEBUG http request            method=GET path=/api/actions status=200 duration=1.2ms
2026-10-03T19:42:12.004+02:00 WARN  connection test failed  host=web01 message="The SSH login was refused…"
2026-10-03T19:42:12.010+02:00 ERROR deployment failed       deployment=dep-1 exitCode=2
```

| Flag | Purpose |
| --- | --- |
| `-verbose` | shorthand for `-log-level=debug`: full detail (requests, `ansible-doc` invocations, rendered commands, Ansible output, deployment events) |
| `-log-level` | `debug`, `info` (default), `warn`, `error`, `fatal` |
| `-log-format` | `text` (default) or `json` for log pipelines |

Environment equivalents: `VISUALIBLE_VERBOSE=1`,
`VISUALIBLE_LOG_LEVEL=debug`, `VISUALIBLE_LOG_FORMAT=json`.

Default output stays calm: successful requests and internal steps are
DEBUG; failed connections, client errors and rejections are WARN; server
errors and failed deployments are ERROR; unrecoverable startup problems are
FATAL (logged, then exit 1). Logs go to stderr, the startup banner to
stdout, so `./visualible > banner.txt` still gives a clean summary.

**Secrets never reach the console.** The logger redacts any value logged
under a sensitive key (`secret`, `password`, `token`, `api_key`, …) in both
text and JSON formats, and credential logging emits metadata only — which
is assert-tested.

Run the test suite (no network or Ansible required):

```bash
go test ./...
```

## The workflow

Visualible is organized around intent, not Ansible vocabulary:

```text
Build  →  Targets  →  Review  →  Deploy
what      which       human      run it
happens   machines    inspection
```

**Build.** Start from a curated Action ("Install software", "Manage a
service", "Create a user", "Deploy a file", …) or describe the change in
plain language to the AI. One intent may become several Ansible tasks —
you never have to know that. Or open *Browse all 8,862 Ansible modules* and
build at the module level.

**Targets.** Machines with friendly fields (name, address, SSH user,
credential, port), groups, and a **Test connection** button that explains
failures in plain language ("The SSH login was refused…", "The machine's
SSH host key is not trusted yet…") with the raw Ansible output one hover
away. The exact Ansible inventory is available under *View Ansible
inventory*.

**Review.** A human summary: which machines, which steps in outcome
language, expected scope, warnings (raw commands, unreachable machines,
steps that stay technical), and *View advanced details* for module names
and arguments. YAML lives here, behind *View Ansible YAML* — an escape
hatch and learning tool, never a requirement.

**Deploy.** Preflight (targets, automation, Ansible readiness, per-machine
connection check), honest work estimates (steps × reachable machines), then
a run that reports per machine in outcome language — *Already correct*,
*Updated*, *Failed* — with raw Ansible output in a collapsible log.

### Simple and Advanced, one IR

There is no separate simplified model and no separate engine: curated
Actions generate ordinary IR tasks, and recognition maps tasks back to
Actions **only when the mapping is faithful**. A task with `when`, `loop`,
`register`, `tags`, unusual module arguments or a second `notify` target is
shown as an *Advanced Ansible task* instead, so nothing is ever hidden.
"Show Ansible details" on any Action reveals the module, its arguments and
the task options — the gradual learning path to Ansible itself.

### AI as an editing workflow

Describe a change; Visualible proposes one.

```text
intent → AI → complete proposed IR → validation → Visualible semantic diff
      → review (accept/reject per change) → merge → validate → apply (one undo)
```

- The model returns **structured IR**, never deployable YAML; Visualible
  owns the prompt, validates everything, and computes the diff itself
  (Added / Removed / Modified / Moved / Unchanged, with field-level
  comparison and moves recognized rather than reported as delete + add).
- Removals require explicit acceptance; "Accept all" says so when removals
  are included.
- Proposals are tied to the project revision they were generated from: if
  you edit while the model works, the proposal is marked stale instead of
  being applied to changed state.
- Applying is a single undoable operation; deployment always remains a
  separate, explicit action.
- AI is optional — everything above works without a provider configured.

## What you can do today

1. Start Visualible; it detects your local Ansible ("Ansible ready"; version
   and module count on click).
2. Build automation with curated Actions, or browse/search all installed
   Ansible modules (live from `ansible-doc`, normalized and cached) and edit
   arguments in **forms generated dynamically from `ansible-doc` metadata**
   (types, choices, defaults, required fields, recursive suboptions).
3. Sequence steps on the canvas (drag-and-drop or arrows) with
   "when this changes → restart…" relationships shown in plain language.
4. Ask the AI for a change, review the proposed diff change by change, and
   apply what you want.
5. Review a human summary and, when you want it, the generated YAML — which
   you can also edit and apply back with structured diagnostics.
6. Manage target machines and groups, test connections, and store SSH
   credentials in an encrypted credential store (referenced, never inlined).
7. Deploy via `ansible-playbook` (`--check`, `--diff`, tags, limit,
   verbosity) with a **structured live event stream** (per-machine progress,
   not just raw terminal output) and cancellation.

## Architecture

```text
Human Intent
    ↓
Visual Editor / AI
    ↓
Visualible Internal IR          ← source of truth
    ↓
Renderer
    ↓
Ansible Playbook / inventory
    ↓
Deployment Plan
    ↓
Deployment Backend / Executor   ← interface, not "SSH"
    ↓
Target Infrastructure
```

Go packages:

| Package | Responsibility |
| --- | --- |
| `internal/ir` | Typed domain model (Project/Playbook/Play/Task/Inventory/Deployment…), validation. Canonical state. |
| `internal/render` | IR → canonical Ansible YAML (playbook + inventory). Refuses unsupported constructs explicitly. |
| `internal/parse` | Ansible YAML → IR with structured diagnostics; unmodeled keys preserved via `Task.Extras`. |
| `internal/ansible` | Local installation detection, `ansible-doc` client (structured exec, no shell), schema normalization, cache. |
| `internal/deploy` | `Backend` interface + capabilities, event normalization, deployment manager. |
| `internal/deploy` (Ansible SSH) | The one real v0.1 backend: workspace render, credential materialization, preflight, process-group exec, event tailing. |
| `internal/store` | SQLite (pure Go): projects as IR documents, encrypted credentials, deployments, settings. |
| `internal/logging` | Leveled console logger (DEBUG/INFO/WARN/ERROR/FATAL), text or JSON, with secret redaction. |
| `internal/action` | Curated beginner Actions: Action → IR generation (incl. compound) and strict IR → Action recognition. Presentation layer only. |
| `internal/ai` | Provider abstraction + OpenAI-compatible adapter; intent → validated IR proposals with rationale. |
| `internal/aidiff` | IR-aware semantic diff (Added/Removed/Modified/Moved, field-level) and selective merge with validation. |
| `internal/server` | REST API + SSE, embedded static frontend. |

Frontend (`internal/web/static`): plain ES modules, no build step. Four
stage modules (build/targets/review/deploy) plus AI, Actions and settings,
over three explicit state slices (server / editor / ui); every editor
mutation flows through one `commit()` function, giving snapshot-based
undo/redo — including whole AI proposals as single operations.

### Key invariants

- **The IR is the source of truth.** Visual state and YAML are derived.
- **Module metadata comes from your Ansible installation** via `ansible-doc`
  (`-l -j`, `-j <fqcn>`), never from a hardcoded database. Any installed
  collection works without Visualible knowing it exists.
- **Structured processes only.** Ansible is invoked with argument arrays
  through `exec.CommandContext`; no shell strings exist in the codebase.
- **Secrets are write-only.** Credentials are AES-GCM encrypted at rest
  (stdlib only, key in a `0600` key file), never returned by the API, never
  logged, and materialized only into the ephemeral `0700` deployment
  workspace, which is deleted after the run.
- **Imported YAML is untrusted data.** It is parsed into data structures
  with diagnostics; it is never executed automatically.
- **Deployment is abstracted.** Backends implement
  `Validate/Prepare/Execute/Cancel/Cleanup` plus a capability set the UI can
  adapt to. Pull/bootstrap or agent-based backends can be added without
  touching the application core.

### API surface (selection)

```text
GET    /api/health                      Ansible availability
GET    /api/actions                     curated Actions (+ availability)
POST   /api/actions/generate            Action → validated IR tasks
POST   /api/actions/recognize           IR tasks → curated labels or Advanced
GET    /api/modules[?refresh=1]         full module catalog from ansible-doc
GET    /api/modules/{fqcn}              normalized schema → dynamic form
POST   /api/render | /api/parse         IR → YAML | YAML → IR + diagnostics
POST   /api/render/inventory            IR inventory → Ansible inventory YAML
GET/POST/PUT/DELETE /api/projects/...   project persistence (IR documents)
GET/POST/DELETE /api/credentials        write-only, encrypted credentials
POST   /api/targets/test                connection test for one machine
POST   /api/deployments/preflight       per-machine readiness check
POST   /api/ai/proposals                intent → validated IR + semantic diff
POST   /api/ai/merge                    selective acceptance → validated IR
POST   /api/deployments[/{id}/...]      deploy, inspect, SSE events, cancel
GET/PUT /api/settings                   configuration (credential references)
```

### Execution events

Raw terminal output is not the execution API. An embedded Ansible callback
plugin (`aggregate` type, stable `v2_*` API) emits JSON-lines events, which
the backend normalizes into typed deployment events
(`play.started`, `task.changed`, `task.failed`, `host.unreachable`, …) and
streams to clients over SSE with persisted replay.

## Security notes

- Credentials at rest are protected by AES-GCM with a random key stored in a
  `0600` key file next to the database. **Threat model:** this protects
  database files/backups at rest. An attacker who can read *both* the
  database and the key file (i.e. has the app's filesystem permissions) can
  decrypt — that is the same trust boundary the app operates under.
- Workspace files (rendered inventory with connection secrets, key files)
  are `0600` inside a `0700` temp directory removed on cleanup.
- FQCNs are validated against a strict regex before reaching `ansible-doc`
  (no option injection). File names derived from host names are sanitized.
  Cleanup refuses paths it does not own.
- API settings store only credential *references*; secret values have no
  read endpoint.

## Deferred by design (explicit non-goals for v0.1)

Kubernetes deployment, Terraform, cloud provisioning, Ansible pull mode,
multi-user collaboration, RBAC/OAuth, plugin marketplace, HA, distributed
workers, perfect YAML formatting preservation, every obscure Ansible syntax
edge case, a custom Ansible replacement engine, mobile UI, external
databases, microservices. The S3 artifact store has a configuration model
and settings UI; the upload backend is deferred. `block/rescue/always` is
parsed and preserved (with diagnostics) but not yet editable in the UI, and
the renderer refuses it rather than silently reinterpreting it.

## License

MIT — see [LICENSE](LICENSE).
