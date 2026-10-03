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
};

// ---------- Editor state (working IR + history) ----------

export const editor = {
  playbook: null,   // ir.Playbook (source of truth for the canvas/YAML)
  selected: null,   // { kind: 'task'|'handler', id } | null
  dirty: false,
  past: [],         // undo stack of serialized playbooks
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
  return JSON.stringify(editor.playbook);
}

// commit records the pre-mutation snapshot for undo, applies the mutation,
// and emits change events. All editor changes go through here.
export function commit(label, mutate) {
  editor.past.push(snapshot());
  if (editor.past.length > HISTORY_LIMIT) editor.past.shift();
  editor.future = [];
  mutate(editor.playbook);
  editor.dirty = true;
  ui.yamlDirty = true;
  emit('editor', { label });
  emit('history');
}

export function undo() {
  if (editor.past.length === 0) return;
  editor.future.push(snapshot());
  editor.playbook = JSON.parse(editor.past.pop());
  editor.dirty = true;
  ui.yamlDirty = true;
  emit('editor', { label: 'undo' });
  emit('history');
}

export function redo() {
  if (editor.future.length === 0) return;
  editor.past.push(snapshot());
  editor.playbook = JSON.parse(editor.future.pop());
  editor.dirty = true;
  ui.yamlDirty = true;
  emit('editor', { label: 'redo' });
  emit('history');
}

// ---------- IR helpers ----------

let idCounter = 0;
export function newId(prefix) {
  return `${prefix}-${Date.now().toString(36)}-${(idCounter++).toString(36)}`;
}

export function currentPlay() {
  return editor.playbook?.plays?.[0] ?? null;
}

export function findTask(id) {
  const play = currentPlay();
  if (!play) return null;
  for (const t of play.tasks ?? []) if (t.id === id) return { task: t, kind: 'task' };
  for (const t of play.handlers ?? []) if (t.id === id) return { task: t, kind: 'handler' };
  return null;
}

export function initPlaybook() {
  editor.playbook = {
    id: newId('pb'),
    name: 'Untitled playbook',
    plays: [{
      id: newId('play'),
      name: 'New play',
      hosts: 'all',
      tasks: [],
      handlers: [],
    }],
  };
  editor.selected = null;
  editor.past = [];
  editor.future = [];
  editor.dirty = false;
  ui.yamlDirty = true;
  emit('editor', { label: 'init' });
  emit('history');
}
