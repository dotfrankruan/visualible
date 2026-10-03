// Build stage: the automation workspace. Beginners see curated Actions
// and intent-first language; the full Ansible module browser is one
// expander away. Everything edits the same IR through store.commit.

import { api } from './api.js';
import * as store from './store.js';
import { el, buildOptionsForm } from './form.js';
import { goStage, aiConfigured } from './app.js';
import { openAIWithIntent } from './ai.js';

const $ = (sel) => document.querySelector(sel);

export function wireBuild() {
  wirePlayFields();
  wirePaletteToggle();
  wireEmptyState();
}

export async function renderBuild() {
  renderPlayFields();
  renderCanvas();
  renderProps();
  await renderPalette();
}

// ---------- Play fields (friendly language) ----------

function wirePlayFields() {
  $('#play-name').addEventListener('change', (e) => {
    store.commit('play', (proj) => { proj.playbooks[0].plays[0].name = e.target.value; });
  });
  $('#play-hosts').addEventListener('change', (e) => {
    store.commit('play', (proj) => { proj.playbooks[0].plays[0].hosts = e.target.value; });
  });
  $('#play-become').addEventListener('change', (e) => {
    store.commit('play', (proj) => { proj.playbooks[0].plays[0].become = e.target.checked || undefined; });
  });
}

function renderPlayFields() {
  const play = store.currentPlay();
  if (!play) return;
  $('#play-name').value = play.name ?? '';
  $('#play-become').checked = !!play.become;

  // Target select: all machines + each group, from the current inventory.
  const sel = $('#play-hosts');
  const inv = store.editor.project?.inventories?.[0];
  const groups = (inv?.groups ?? []).map((g) => g.name);
  const current = play.hosts || 'all';
  sel.replaceChildren();
  const mkOpt = (value, label) => {
    const o = el('option', { value }, label);
    if (value === current) o.selected = true;
    return o;
  };
  sel.append(mkOpt('all', 'all machines'));
  for (const g of groups) sel.append(mkOpt(g, `group: ${g}`));
  if (!['all', ...groups].includes(current)) sel.append(mkOpt(current, current));
}

// ---------- Empty state ----------

function wireEmptyState() {
  $('#empty-ai-generate').addEventListener('click', () => {
    openAIWithIntent($('#empty-ai-intent').value.trim());
  });
  $('#empty-import-yaml').addEventListener('click', () => {
    goStage('review');
    document.querySelector('#review-yaml-toggle')?.click();
    setTimeout(() => document.querySelector('#btn-edit-yaml')?.click(), 50);
  });
}

function renderEmptyState() {
  const play = store.currentPlay();
  const empty = !play || ((play.tasks ?? []).length === 0 && (play.handlers ?? []).length === 0);
  $('#canvas-empty').classList.toggle('hidden', !empty);
  $('#canvas-wrap').classList.toggle('hidden', empty);
  $('#empty-ai').classList.toggle('hidden', !aiConfigured());
}

// ---------- Palette: common Actions + advanced module browser ----------

let actionsLoaded = false;

async function renderPalette() {
  renderEmptyState();
  if (!actionsLoaded) {
    actionsLoaded = true;
    await loadActions();
  }
  await loadModules(false);
}

async function loadActions() {
  try {
    const { actions } = await api.actions();
    store.server.actions = actions.filter((a) => a.available !== false);
  } catch {
    store.server.actions = []; // older backend: module browser only
  }
  renderActionButtons();
}

function renderActionButtons() {
  const palette = $('#palette-common');
  const empty = $('#empty-actions');
  palette.replaceChildren();
  empty.replaceChildren();
  for (const a of store.server.actions ?? []) {
    const btn = (cls) => el('button', {
      class: cls, onclick: () => addAction(a),
    },
      el('span', { class: 'a-icon' }, a.icon),
      el('span', { class: 'a-label' }, a.name,
        el('span', { class: 'a-sub' }, a.summary ?? '')));
    palette.append(btn('action-btn'));
    empty.append(btn('action-btn'));
  }
}

// ---------- Advanced module browser ----------

function wirePaletteToggle() {
  $('#palette-advanced-toggle').addEventListener('click', () => {
    const t = $('#palette-advanced-toggle');
    const panel = $('#palette-advanced');
    const open = panel.classList.toggle('hidden');
    t.classList.toggle('open', !open);
    if (!open && !(store.server.modules?.length)) loadModules(false);
  });
  $('#module-search').addEventListener('input', (e) => {
    store.ui.moduleSearch = e.target.value;
    renderModuleBrowser();
  });
  $('#module-collection').addEventListener('change', (e) => {
    store.ui.moduleCollection = e.target.value;
    renderModuleBrowser();
  });
  renderModuleActions();
}

async function loadModules(refresh) {
  if (store.server.modules?.length && !refresh) {
    renderModuleBrowser();
    return;
  }
  try {
    const data = await api.modules(refresh);
    store.server.modules = data.modules ?? [];
    store.server.ansibleError = null;
  } catch (e) {
    store.server.modules = [];
    store.server.ansibleError = e;
  }
  renderModuleBrowser();
}

function renderModuleBrowser() {
  const listEl = $('#module-list');
  listEl.replaceChildren();

  const err = store.server.ansibleError;
  if (err) {
    listEl.append(
      el('div', { class: 'empty-state' },
        err.code === 'ansible_unavailable'
          ? 'Ansible was not detected. Install Ansible and restart Visualible.'
          : `Could not load modules: ${err.message}`),
      el('div', { style: 'padding:0 10px' },
        el('button', { class: 'mini-btn', onclick: () => loadModules(true) }, 'retry discovery')),
    );
    return;
  }

  const collSelect = $('#module-collection');
  const collections = [...new Set(store.server.modules.map((m) => m.collection).filter(Boolean))].sort();
  const currentColl = store.ui.moduleCollection;
  collSelect.replaceChildren(el('option', { value: '' }, 'All collections'));
  for (const c of collections) {
    const o = el('option', { value: c }, c);
    if (c === currentColl) o.selected = true;
    collSelect.append(o);
  }

  const q = store.ui.moduleSearch.toLowerCase();
  const filtered = store.server.modules.filter((m) => {
    if (store.ui.moduleCollection && m.collection !== store.ui.moduleCollection) return false;
    if (!q) return true;
    return m.fqcn.includes(q) || (m.shortDescription ?? '').toLowerCase().includes(q);
  });

  const LIMIT = 400;
  for (const m of filtered.slice(0, LIMIT)) {
    listEl.append(el('div', {
      class: 'module-item' + (store.ui.selectedModule === m.fqcn ? ' selected' : ''),
      onclick: () => { store.ui.selectedModule = m.fqcn; renderModuleBrowser(); },
    },
      el('div', { class: 'fqcn' }, m.fqcn),
      m.shortDescription ? el('div', { class: 'desc', title: m.shortDescription }, m.shortDescription) : null,
    ));
  }
  if (filtered.length > LIMIT) {
    listEl.append(el('div', { class: 'empty-state' }, `${filtered.length - LIMIT} more — refine your search.`));
  }
  if (filtered.length === 0) {
    listEl.append(el('div', { class: 'empty-state' }, 'No modules match.'));
  }
  renderModuleActions();
}

function renderModuleActions() {
  const bar = $('#module-actions-bar');
  bar.replaceChildren(
    el('button', {
      class: 'primary',
      disabled: !store.ui.selectedModule,
      onclick: () => addTaskFromModule('task'),
    }, 'Add step'),
    el('button', {
      class: 'mini-btn',
      disabled: !store.ui.selectedModule,
      onclick: () => addTaskFromModule('handler'),
    }, 'Add handler'),
    el('button', { class: 'mini-btn', title: 'Refresh module cache', onclick: () => loadModules(true) }, '⟳'),
  );
}

async function ensureSchema(fqcn) {
  if (store.server.moduleSchemas.has(fqcn)) return store.server.moduleSchemas.get(fqcn);
  const schema = await api.moduleDoc(fqcn);
  store.server.moduleSchemas.set(fqcn, schema);
  return schema;
}

async function addTaskFromModule(kind) {
  const fqcn = store.ui.selectedModule;
  if (!fqcn) return;
  const task = {
    id: store.newId(kind === 'handler' ? 'h' : 't'),
    name: fqcn.split('.').pop(),
    module: fqcn,
    args: {},
  };
  try {
    const schema = await ensureSchema(fqcn);
    for (const opt of Object.values(schema.options ?? {})) {
      if (opt.required && opt.default !== undefined) task.args[opt.name] = opt.default;
    }
  } catch { /* schema is optional at add time */ }

  store.commit(`add ${kind}`, (proj) => {
    const play = proj.playbooks[0].plays[0];
    if (kind === 'handler') {
      play.handlers = play.handlers ?? [];
      play.handlers.push(task);
    } else {
      play.tasks.push(task);
    }
  });
  store.editor.selected = { kind, id: task.id };
  renderProps();
}

// ---------- Action creation (curated path) ----------

async function addAction(def) {
  // Open the details panel in "new action" mode; the form is generated
  // from the action definition's curated fields.
  store.ui.draftAction = { def, params: defaultParams(def) };
  renderProps();
}

function defaultParams(def) {
  const params = {};
  for (const f of def.fields ?? []) {
    if (f.default !== undefined) params[f.id] = f.default;
  }
  return params;
}

// ---------- Canvas ----------

function renderCanvas() {
  renderEmptyState();
  const play = store.currentPlay();
  if (!play) return;

  const taskList = $('#task-list');
  const handlerList = $('#handler-list');
  taskList.replaceChildren();
  handlerList.replaceChildren();

  const recog = store.server.recognitions ?? new Map();

  (play.tasks ?? []).forEach((t, i, arr) => {
    taskList.append(taskCard(t, 'task', i, arr.length, recog.get(t.id)));
    if (i < arr.length - 1) taskList.append(el('div', { class: 'flow-arrow' }, '↓'));
  });

  const handlers = play.handlers ?? [];
  $('#handlers-section').classList.toggle('hidden', handlers.length === 0);
  handlers.forEach((h, i, arr) => {
    handlerList.append(taskCard(h, 'handler', i, arr.length, recog.get(h.id)));
  });

  $('#canvas-hint').classList.toggle('hidden',
    (play.tasks?.length || play.handlers?.length) ? true : false);
}

function taskCard(t, kind, index, count, rec) {
  const sel = store.editor.selected;
  const isSel = sel && sel.id === t.id;
  const card = el('div', {
    class: `task-card ${kind}` + (isSel ? ' selected' : ''),
    draggable: true,
    onclick: () => {
      store.ui.draftAction = null;
      store.editor.selected = { kind, id: t.id };
      renderCanvas();
      renderProps();
    },
    ondragstart: (e) => {
      e.dataTransfer.setData('text/plain', JSON.stringify({ id: t.id, kind }));
      e.dataTransfer.effectAllowed = 'move';
    },
    ondragover: (e) => {
      e.preventDefault();
      e.dataTransfer.dropEffect = 'move';
      card.classList.add('drop-target');
    },
    ondragleave: () => card.classList.remove('drop-target'),
    ondrop: (e) => {
      e.preventDefault();
      card.classList.remove('drop-target');
      try {
        const src = JSON.parse(e.dataTransfer.getData('text/plain'));
        if (src.kind === kind && src.id !== t.id) moveTaskToPosition(src.id, kind, index);
      } catch { /* ignore foreign drops */ }
    },
  },
    el('div', { class: 'a-icon' }, rec?.icon ?? (kind === 'handler' ? '↳' : '▸')),
    el('div', {},
      el('div', { class: 'tname' }, rec?.label ?? t.name ?? '(unnamed)'),
      rec?.subtitle
        ? el('div', { class: 'tsub' }, rec.subtitle)
        : (t.name ? el('div', { class: 'tmodule' }, t.module) : null),
      notifyText(t, kind),
    ),
    el('div', { class: 'tactions' },
      el('button', {
        title: 'Move up', disabled: index === 0,
        onclick: (e) => { e.stopPropagation(); moveTask(t.id, kind, -1); },
      }, '↑'),
      el('button', {
        title: 'Move down', disabled: index === count - 1,
        onclick: (e) => { e.stopPropagation(); moveTask(t.id, kind, 1); },
      }, '↓'),
      el('button', {
        title: 'Delete',
        onclick: (e) => { e.stopPropagation(); deleteTask(t.id, kind); },
      }, '✕'),
    ),
  );
  return card;
}

function notifyText(t, kind) {
  if (kind === 'handler') return null;
  if (!t.notify?.length) return null;
  return el('div', { class: 'tnotify' }, `when this changes: ${t.notify.join(', ')}`);
}

function taskListOf(play, kind) {
  return kind === 'handler' ? (play.handlers = play.handlers ?? []) : play.tasks;
}

function moveTask(id, kind, delta) {
  store.commit('reorder', (proj) => {
    const list = taskListOf(proj.playbooks[0].plays[0], kind);
    const i = list.findIndex((t) => t.id === id);
    const j = i + delta;
    if (i < 0 || j < 0 || j >= list.length) return;
    [list[i], list[j]] = [list[j], list[i]];
  });
}

function moveTaskToPosition(srcId, kind, targetIndex) {
  store.commit('reorder', (proj) => {
    const list = taskListOf(proj.playbooks[0].plays[0], kind);
    const from = list.findIndex((t) => t.id === srcId);
    if (from < 0) return;
    const [moved] = list.splice(from, 1);
    const to = from < targetIndex ? targetIndex - 1 : targetIndex;
    list.splice(to, 0, moved);
  });
}

function deleteTask(id, kind) {
  store.commit('delete', (proj) => {
    const list = taskListOf(proj.playbooks[0].plays[0], kind);
    const i = list.findIndex((t) => t.id === id);
    if (i >= 0) list.splice(i, 1);
  });
  if (store.editor.selected?.id === id) store.editor.selected = null;
  renderCanvas();
  renderProps();
}

// ---------- Properties panel ----------

async function renderProps() {
  const props = $('#props');
  props.replaceChildren();

  // Draft action (being configured, not yet added).
  if (store.ui.draftAction) {
    const { renderActionForm } = await import('./actions.js');
    renderActionForm(props, store.ui.draftAction, () => {
      store.ui.draftAction = null;
      renderProps();
    });
    return;
  }

  const sel = store.editor.selected;
  const found = sel ? store.findTask(sel.id) : null;
  if (!found) {
    props.append(el('div', { class: 'empty-state' }, 'Select a step to see its details.'));
    return;
  }
  const { task, kind } = found;

  const rec = store.server.recognitions?.get(task.id);
  if (rec?.recognized) {
    const { renderRecognizedForm } = await import('./actions.js');
    renderRecognizedForm(props, task, kind, rec);
    return;
  }
  renderAdvancedTaskForm(props, task, kind);
}

// Advanced editor for any task (module arguments + task options).
// Used directly for unrecognized tasks and reachable from curated forms.
export function renderAdvancedTaskForm(props, task, kind) {
  const meta = el('div', { class: 'props-section' },
    el('div', { class: 'props-heading' }, kind === 'handler' ? 'Handler (runs when notified)' : 'Ansible step'));
  props.append(meta);

  meta.append(textField('name', task.name ?? '', (v) => {
    store.commit('rename', () => { task.name = v; });
  }));
  meta.append(el('div', { class: 'field' },
    el('label', {}, el('span', { class: 'fname mono' }, 'module'), el('span', { class: 'badge type' }, 'FQCN')),
    el('div', { class: 'tmodule', style: 'font-family:var(--mono);padding:4px 0' }, task.module)));

  const common = el('div', { class: 'props-section' },
    el('div', { class: 'props-heading' }, 'Task options'));
  props.append(common);

  common.append(textField('when', task.when ?? '', (v) => {
    store.commit('edit', () => { task.when = v || undefined; });
  }));
  common.append(textField('register', task.register ?? '', (v) => {
    store.commit('edit', () => { task.register = v || undefined; });
  }));
  common.append(textField('tags (comma-separated)', (task.tags ?? []).join(', '), (v) => {
    store.commit('edit', () => {
      task.tags = v ? v.split(',').map((s) => s.trim()).filter(Boolean) : undefined;
    });
  }));
  common.append(textField('delegate_to', task.delegateTo ?? '', (v) => {
    store.commit('edit', () => { task.delegateTo = v || undefined; });
  }));
  common.append(textField('changed_when', task.changedWhen ?? '', (v) => {
    store.commit('edit', () => { task.changedWhen = v || undefined; });
  }));
  common.append(textField('failed_when', task.failedWhen ?? '', (v) => {
    store.commit('edit', () => { task.failedWhen = v || undefined; });
  }));
  // Administrator privileges: beginner wording for become.
  const becomeBox = el('input', { type: 'checkbox' });
  becomeBox.checked = task.become === true;
  becomeBox.addEventListener('change', () => {
    store.commit('edit', () => { task.become = becomeBox.checked ? true : undefined; });
  });
  common.append(el('div', { class: 'field' },
    el('label', { title: 'become' }, becomeBox, ' administrator privileges (sudo)')));

  const play = store.currentPlay();
  const handlerNames = (play.handlers ?? []).map((h) => h.name);
  if (kind !== 'handler' && handlerNames.length) {
    const select = el('select', { multiple: true, size: String(Math.min(handlerNames.length, 4)) });
    for (const n of handlerNames) {
      const o = el('option', { value: n }, n);
      if ((task.notify ?? []).includes(n)) o.selected = true;
      select.append(o);
    }
    select.addEventListener('change', () => {
      const chosen = [...select.selectedOptions].map((o) => o.value);
      store.commit('edit', () => { task.notify = chosen.length ? chosen : undefined; });
    });
    common.append(el('div', { class: 'field' },
      el('label', {}, el('span', { class: 'fname' }, 'notify (run handler when changed)')), select));
  }

  const argsSection = el('div', { class: 'props-section' },
    el('div', { class: 'props-heading' }, 'Module arguments'));
  props.append(argsSection);
  const status = el('div', { class: 'empty-state' }, 'Loading module schema…');
  argsSection.append(status);

  ensureSchema(task.module).then((schema) => {
    if (store.editor.selected?.id !== task.id) return;
    status.remove();
    if (schema.shortDescription) {
      argsSection.append(el('div', { class: 'fdesc', style: 'margin-bottom:8px' }, schema.shortDescription));
    }
    task.args = task.args ?? {};
    const formWrap = el('div', {});
    buildOptionsForm(formWrap, schema.options, task.args, (key, v) => {
      store.commit('arg', () => {
        if (v === undefined) delete task.args[key];
        else task.args[key] = v;
      });
    });
    argsSection.append(formWrap);
  }).catch((e) => {
    status.textContent = store.server.ansibleError?.code === 'ansible_unavailable'
      ? 'Schema unavailable: Ansible is not detected.'
      : `Could not load schema for ${task.module}: ${e.message}`;
  });
}

function textField(label, value, onCommit) {
  const input = el('input', { type: 'text', value });
  input.addEventListener('change', () => onCommit(input.value));
  return el('div', { class: 'field' },
    el('label', {}, el('span', { class: 'fname' }, label)), input);
}
