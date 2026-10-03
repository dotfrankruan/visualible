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
};

// ---------- Editor state (working IR + history) ----------

export const editor = {
  project: null,    // ir.Project (source of truth; playbook = playbooks[0])
  selected: null,   // { kind: 'task'|'handler', id } | null
  dirty: false,
  past: [],         // undo stack of serialized projects
  future: [],       // redo stack
};

// ---------- UI state ----------

export const ui = {
  tab: 'visual',
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
  ui.yamlDirty = true;
  emit('editor', { label });
  emit('history');
}

export function undo() {
  if (editor.past.length === 0) return;
  editor.future.push(snapshot());
  editor.project = JSON.parse(editor.past.pop());
  editor.dirty = true;
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

export function currentPlaybook() {
  return editor.project?.playbooks?.[0] ?? null;
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
export function loadProject(project) {
  editor.project = project;
  editor.selected = null;
  editor.past = [];
  editor.future = [];
  editor.dirty = false;
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
