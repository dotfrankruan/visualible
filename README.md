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

Run the test suite (no network or Ansible required):

```bash
go test ./...
```

## What you can do today

1. Start Visualible; it detects your local Ansible.
2. Browse/search all installed Ansible modules (live from `ansible-doc`,
   normalized and cached — including every installed collection).
3. Add modules as tasks; edit arguments in **forms generated dynamically from
   `ansible-doc` metadata** (types, choices, defaults, required fields,
   recursive suboptions).
4. Sequence tasks and handlers on the canvas (drag-and-drop or arrows),
   wire `notify` → handler relationships.
5. View canonical rendered YAML; edit YAML directly and apply it back into
   the editor with structured diagnostics (explicit sync, no fragile
   reparse-on-keystroke).
6. Build an inventory (groups, hosts, connection vars) and store SSH
   credentials in an encrypted credential store (referenced, never inlined).
7. Deploy via `ansible-playbook` (`--check`, `--diff`, tags, limit,
   verbosity) with a **structured live event stream** (play/task/host
   statuses, not just raw terminal output) and cancellation.
8. Optionally configure an OpenAI-compatible AI endpoint (e.g. Ollama) and
   generate playbook proposals from natural language — proposals become
   reviewable IR; nothing is applied or deployed without your approval.

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
| `internal/ai` | Provider abstraction + OpenAI-compatible adapter; intent → IR proposals. |
| `internal/server` | REST API + SSE, embedded static frontend. |

Frontend (`internal/web/static`): three explicit state slices (server /
editor / ui) with event subscriptions; all editor mutations flow through one
`commit()` function, giving snapshot-based undo/redo.

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
