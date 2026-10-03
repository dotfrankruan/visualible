// Explicit browser state. Three separate slices, updated only through
// functions that notify subscribers. No implicit global mutation:
// components read state and subscribe to named change events.

const listeners = new Map(); // event -> Set<fn>

export function on(event, fn) {
  if (!listeners.has(event)) listeners.set(event, new Set());
  listeners.get(event).add(fn);
  return () => listeners.get(event).delete(fn);
}

export function emit(event, payload) {
  for (const fn of listeners.get(event) ?? []) fn(payload);
}

// ---------- Server state (mirrors of API data) ----------

export const server = {
  health: null,              // GET /api/health
  modules: [],               // module catalog summaries
  moduleSchemas: new Map(),  // fqcn -> normalized schema
  ansibleError: null,        // structured API error when discovery unavailable
  projects: [],              // project metadata list
  credentials: [],           // credential metadata (never secrets)
  deployBackend: null,       // backend metadata + capabilities
  settings: null,            // app settings (no secrets)
  actions: [],               // curated action definitions
  recognitions: new Map(),   // taskId -> recognition result
};

// ---------- Editor state (working IR + history) ----------

export const editor = {
  project: null,     // ir.Project (the workspace document, saved as a whole)
  playbookId: null,  // currently open playbook inside the project
  selected: null,    // { kind: 'task'|'handler', id } | null
  dirty: false,
  revision: 0,       // bumped on every mutation; AI proposals capture it
  past: [],          // undo stack of serialized projects
  future: [],        // redo stack
};

// ---------- UI state ----------

export const ui = {
  // library | project | build | targets | review | deploy
  view: 'library',
  stage: 'build',
  draftAction: null,     // action being configured (not yet added)
  moduleSearch: '',
  moduleCollection: '',
  selectedModule: null, // fqcn selected in the module browser
  yamlDirty: true,      // YAML view needs re-render
};

// ---------- Editor mutations (history-aware) ----------

const HISTORY_LIMIT = 100;

function snapshot() {
  return JSON.stringify(editor.project);
}

// commit records the pre-mutation snapshot for undo, applies the mutation,
// and emits change events. All editor changes go through here.
export function commit(label, mutate) {
  editor.past.push(snapshot());
  if (editor.past.length > HISTORY_LIMIT) editor.past.shift();
  editor.future = [];
  mutate(editor.project);
  editor.dirty = true;
  editor.revision++;
  ui.yamlDirty = true;
  emit('editor', { label });
  emit('history');
}

export function undo() {
  if (editor.past.length === 0) return;
  editor.future.push(snapshot());
  editor.project = JSON.parse(editor.past.pop());
  editor.dirty = true;
  editor.revision++;
  ui.yamlDirty = true;
  if (editor.selected && !findTask(editor.selected.id)) editor.selected = null;
  emit('editor', { label: 'undo' });
  emit('history');
}

export function redo() {
  if (editor.future.length === 0) return;
  editor.past.push(snapshot());
  editor.project = JSON.parse(editor.future.pop());
  editor.dirty = true;
  editor.revision++;
  ui.yamlDirty = true;
  if (editor.selected && !findTask(editor.selected.id)) editor.selected = null;
  emit('editor', { label: 'redo' });
  emit('history');
}

// ---------- IR helpers ----------

let idCounter = 0;
export function newId(prefix) {
  return `${prefix}-${Date.now().toString(36)}-${(idCounter++).toString(36)}`;
}

// currentPlaybook returns the playbook the editor is working on. Every
// view (canvas, review, AI, deploy) is scoped to this document, so a
// project can hold many playbooks without any of them leaking into
// another.
export function currentPlaybook() {
  const playbooks = editor.project?.playbooks ?? [];
  if (!playbooks.length) return null;
  return playbooks.find((pb) => pb.id === editor.playbookId) ?? playbooks[0];
}

// selectPlaybook switches the open document. Unsaved changes are safe:
// the whole project (all playbooks) is one document with one save.
export function selectPlaybook(id) {
  const playbooks = editor.project?.playbooks ?? [];
  if (!playbooks.some((pb) => pb.id === id)) return false;
  editor.playbookId = id;
  editor.selected = null;
  ui.yamlDirty = true;
  emit('editor', { label: 'playbook' });
  emit('playbook');
  return true;
}

export function playbookById(id) {
  return (editor.project?.playbooks ?? []).find((pb) => pb.id === id) ?? null;
}

// mutatePlaybook returns the currently open playbook from the project
// passed to commit(). Every editor mutation that edits document content
// must go through this so multi-playbook projects never edit the wrong
// document. Returns null when nothing is open.
export function mutatePlaybook(proj) {
  const playbooks = proj?.playbooks ?? [];
  if (!playbooks.length) return null;
  return playbooks.find((pb) => pb.id === editor.playbookId) ?? playbooks[0];
}

// mutatePlay returns the first play of the currently open playbook, which
// is the document scope of the visual editor.
export function mutatePlay(proj) {
  const pb = mutatePlaybook(proj);
  if (!pb) return null;
  pb.plays = pb.plays ?? [];
  if (!pb.plays.length) {
    pb.plays.push({ id: newId('play'), name: pb.name || 'New play', hosts: 'all', tasks: [], handlers: [] });
  }
  return pb.plays[0];
}

export function currentPlay() {
  return currentPlaybook()?.plays?.[0] ?? null;
}

export function findTask(id) {
  const play = currentPlay();
  if (!play) return null;
  for (const t of play.tasks ?? []) if (t.id === id) return { task: t, kind: 'task' };
  for (const t of play.handlers ?? []) if (t.id === id) return { task: t, kind: 'handler' };
  return null;
}

// loadProject replaces the editor content (open from server / new).
// History is reset: undo must never cross a project boundary.
export function loadProject(project, playbookId) {
  editor.project = project;
  const playbooks = project?.playbooks ?? [];
  editor.playbookId = playbookId && playbooks.some((pb) => pb.id === playbookId)
    ? playbookId
    : (playbooks[0]?.id ?? null);
  editor.selected = null;
  editor.past = [];
  editor.future = [];
  editor.dirty = false;
  editor.revision = 0;
  ui.yamlDirty = true;
  emit('editor', { label: 'load' });
  emit('history');
  emit('project');
}

// markSaved clears the dirty flag after a successful save.
export function markSaved(project) {
  if (project) editor.project = project;
  editor.dirty = false;
  emit('project');
}
