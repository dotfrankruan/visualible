// Visualible frontend entry point. Wires the module browser, playbook
// canvas, properties panel and YAML view to the explicit store. All
// editor mutations go through store.commit for undo/redo.

import { api } from './api.js';
import * as store from './store.js';
import { el, buildOptionsForm } from './form.js';
import { renderYaml } from './yaml.js';
import { wireInventoryTab, renderInventory } from './inventory.js';
import { wireDeploymentsTab, renderDeployments } from './deployments.js';
import { wireSettingsAI } from './settings.js';

const $ = (sel) => document.querySelector(sel);

// ---------- Boot ----------

async function boot() {
  wireChrome();
  wireCanvas();
  wireTabs();
  wireProjectModal();
  wireInventoryTab();
  wireDeploymentsTab();
  wireSettingsAI();

  store.on('editor', () => { renderCanvas(); renderProps(); renderProjectBar(); });
  store.on('history', renderHistoryButtons);
  store.on('project', renderProjectBar);

  try {
    const health = await api.health();
    store.server.health = health;
    renderAnsibleStatus();
  } catch (e) {
    renderAnsibleStatus(e);
  }

  await Promise.all([loadModules(false), openInitialProject()]);
  renderCanvas();
  renderProps();
  renderProjectBar();
}

async function openInitialProject() {
  try {
    const { projects } = await api.projects();
    store.server.projects = projects;
    if (projects.length) {
      const p = await api.project(projects[0].id);
      store.loadProject(p);
      return;
    }
  } catch { /* storage unavailable: fall through to a fresh local project */ }
  store.loadProject(freshProject());
}

function freshProject() {
  return {
    id: store.newId('proj'),
    name: 'Untitled project',
    playbooks: [{
      id: store.newId('pb'),
      name: 'playbook',
      plays: [{ id: store.newId('play'), name: 'New play', hosts: 'all', tasks: [], handlers: [] }],
    }],
    inventories: [],
  };
}

function renderAnsibleStatus(err) {
  const pill = $('#ansible-status');
  if (err) {
    pill.textContent = 'backend unreachable';
    pill.className = 'status-pill err';
    return;
  }
  const a = store.server.health?.ansible;
  if (a?.available) {
    pill.textContent = `ansible ${a.version || '?'} · ${store.server.health.modules || '…'} modules`;
    pill.className = 'status-pill ok';
  } else {
    pill.textContent = 'ansible not detected';
    pill.className = 'status-pill err';
  }
}

async function loadModules(refresh) {
  const listEl = $('#module-list');
  listEl.replaceChildren(el('div', { class: 'empty-state' }, 'Loading modules…'));
  try {
    const data = await api.modules(refresh);
    store.server.modules = data.modules ?? [];
    store.server.ansibleError = null;
  } catch (e) {
    store.server.modules = [];
    store.server.ansibleError = e;
  }
  renderModuleBrowser();
  renderAnsibleStatus();
}

// ---------- Project bar / persistence ----------

function renderProjectBar() {
  const p = store.editor.project;
  $('#project-name').textContent = p ? p.name : '—';
  $('#project-dirty').style.display = store.editor.dirty ? '' : 'none';
  $('#btn-save').disabled = !store.editor.dirty;
}

async function saveProject() {
  const p = store.editor.project;
  if (!p) return;
  $('#btn-save').disabled = true;
  try {
    const saved = await api.saveProject(p);
    store.markSaved(saved);
  } catch (e) {
    const msg = e.problems?.length ? e.problems.join('\n') : e.message;
    alert(`Save failed:\n${msg}`);
    $('#btn-save').disabled = false;
  }
}

function wireProjectModal() {
  $('#btn-save').addEventListener('click', saveProject);
  $('#btn-projects').addEventListener('click', openProjectModal);
  $('#project-modal-close').addEventListener('click', closeProjectModal);
  $('#project-modal').addEventListener('click', (e) => {
    if (e.target.id === 'project-modal') closeProjectModal();
  });
  $('#project-new-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const name = $('#project-new-name').value.trim();
    if (!name) return;
    try {
      const p = await api.createProject(name, '');
      store.loadProject(p);
      closeProjectModal();
    } catch (err) {
      alert(`Create failed: ${err.message}`);
    }
  });
}

async function openProjectModal() {
  $('#project-modal').classList.remove('hidden');
  $('#project-new-name').value = '';
  await refreshProjectList();
}

function closeProjectModal() {
  $('#project-modal').classList.add('hidden');
}

async function refreshProjectList() {
  const listEl = $('#project-list');
  listEl.replaceChildren(el('div', { class: 'empty-state' }, 'Loading…'));
  try {
    const { projects } = await api.projects();
    store.server.projects = projects;
    listEl.replaceChildren();
    if (!projects.length) {
      listEl.append(el('div', { class: 'empty-state' }, 'No projects yet. Create one below.'));
      return;
    }
    for (const m of projects) {
      const isCurrent = store.editor.project?.id === m.id;
      listEl.append(el('div', { class: 'project-row' },
        el('div', { class: 'project-info' },
          el('div', { class: 'project-title' }, m.name + (isCurrent ? ' (open)' : '')),
          el('div', { class: 'project-meta' }, `updated ${new Date(m.updatedAt).toLocaleString()}`)),
        el('button', {
          class: 'mini-btn', disabled: isCurrent,
          onclick: () => openProject(m.id),
        }, 'Open'),
        el('button', {
          class: 'mini-btn danger',
          onclick: () => deleteProject(m.id, m.name),
        }, 'Delete'),
      ));
    }
  } catch (e) {
    listEl.replaceChildren(el('div', { class: 'empty-state' }, `Could not load projects: ${e.message}`));
  }
}

async function openProject(id) {
  if (store.editor.dirty && !confirm('Discard unsaved changes?')) return;
  try {
    const p = await api.project(id);
    store.loadProject(p);
    renderCanvas();
    renderProps();
    closeProjectModal();
  } catch (e) {
    alert(`Open failed: ${e.message}`);
  }
}

async function deleteProject(id, name) {
  if (!confirm(`Delete project “${name}”? This cannot be undone.`)) return;
  try {
    await api.deleteProject(id);
    if (store.editor.project?.id === id) {
      store.loadProject(freshProject());
      renderCanvas();
      renderProps();
    }
    await refreshProjectList();
  } catch (e) {
    alert(`Delete failed: ${e.message}`);
  }
}

// ---------- Module browser ----------

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
    renderModuleActions();
    return;
  }

  // Collection filter options.
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

  const LIMIT = 400; // avoid pathological DOM size; search narrows results
  for (const m of filtered.slice(0, LIMIT)) {
    const item = el('div', {
      class: 'module-item' + (store.ui.selectedModule === m.fqcn ? ' selected' : ''),
      onclick: () => { store.ui.selectedModule = m.fqcn; renderModuleBrowser(); },
    },
      el('div', { class: 'fqcn' }, m.fqcn),
      m.shortDescription ? el('div', { class: 'desc', title: m.shortDescription }, m.shortDescription) : null,
    );
    listEl.append(item);
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
  let bar = $('#module-actions-bar');
  if (!bar) {
    bar = el('div', { id: 'module-actions-bar', class: 'module-actions' });
    $('#module-panel').append(bar);
  }
  bar.replaceChildren(
    el('button', {
      class: 'primary',
      disabled: !store.ui.selectedModule,
      onclick: () => addTaskFromModule('task'),
    }, 'Add task'),
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
  // Pre-fill required options with documented defaults where available.
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

// ---------- Canvas ----------

function renderCanvas() {
  const play = store.currentPlay();
  if (!play) return;

  $('#play-name').value = play.name ?? '';
  $('#play-hosts').value = play.hosts ?? '';
  $('#play-become').checked = !!play.become;

  const taskList = $('#task-list');
  const handlerList = $('#handler-list');
  taskList.replaceChildren();
  handlerList.replaceChildren();

  (play.tasks ?? []).forEach((t, i, arr) => {
    taskList.append(taskCard(t, 'task', i, arr.length));
    if (i < arr.length - 1) taskList.append(el('div', { class: 'flow-arrow' }, '↓'));
  });
  (play.handlers ?? []).forEach((h, i, arr) => {
    handlerList.append(taskCard(h, 'handler', i, arr.length));
  });

  $('#canvas-hint').style.display =
    (play.tasks?.length || play.handlers?.length) ? 'none' : '';
}

function taskCard(t, kind, index, count) {
  const sel = store.editor.selected;
  const isSel = sel && sel.id === t.id;
  const notify = (t.notify?.length) ? ` ⚡ ${t.notify.join(', ')}` : '';
  return el('div', {
    class: `task-card ${kind}` + (isSel ? ' selected' : ''),
    onclick: () => {
      store.editor.selected = { kind, id: t.id };
      renderCanvas();
      renderProps();
    },
  },
    el('div', { class: 'grip', title: 'Reorder with the arrows' }, '⋮⋮'),
    el('div', {},
      el('div', { class: 'tname' }, t.name || '(unnamed)'),
      el('div', { class: 'tmodule' }, t.module),
      notify ? el('div', { class: 'tnotify' }, notify.trim()) : null,
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

  const sel = store.editor.selected;
  const found = sel ? store.findTask(sel.id) : null;
  if (!found) {
    props.append(el('div', { class: 'empty-state' }, 'No task selected.'));
    return;
  }
  const { task, kind } = found;

  // --- Task metadata section ---
  const meta = el('div', { class: 'props-section' },
    el('div', { class: 'props-heading' }, kind === 'handler' ? 'Handler' : 'Task'));
  props.append(meta);

  meta.append(textField('name', task.name ?? '', (v) => {
    store.commit('rename', () => { task.name = v; });
  }));
  meta.append(el('div', { class: 'field' },
    el('label', {}, el('span', { class: 'fname' }, 'module'), el('span', { class: 'badge type' }, 'FQCN')),
    el('div', { class: 'tmodule', style: 'font-family:var(--mono);padding:4px 0' }, task.module)));

  // --- Common fields section ---
  const common = el('div', { class: 'props-section' },
    el('div', { class: 'props-heading' }, 'Common'));
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

  // notify: multi-select of available handlers.
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
      el('label', {}, el('span', { class: 'fname' }, 'notify')), select));
  }

  // --- Module arguments section (dynamic form) ---
  const argsSection = el('div', { class: 'props-section' },
    el('div', { class: 'props-heading' }, 'Module arguments'));
  props.append(argsSection);

  const status = el('div', { class: 'empty-state' }, 'Loading module schema…');
  argsSection.append(status);

  try {
    const schema = await ensureSchema(task.module);
    // Re-entry guard: selection may have changed while loading.
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
  } catch (e) {
    status.textContent = store.server.ansibleError?.code === 'ansible_unavailable'
      ? 'Schema unavailable: Ansible is not detected.'
      : `Could not load schema for ${task.module}: ${e.message}`;
  }
}

function textField(label, value, onCommit) {
  const input = el('input', { type: 'text', value });
  input.addEventListener('change', () => onCommit(input.value));
  return el('div', { class: 'field' },
    el('label', {}, el('span', { class: 'fname' }, label)), input);
}

// ---------- YAML tab ----------

let lastRenderedYaml = '';

async function renderYamlView() {
  const pre = $('#yaml-view');
  const status = $('#yaml-status');
  pre.replaceChildren();
  $('#yaml-diagnostics').replaceChildren();
  status.textContent = 'rendering…';
  status.className = 'hint-line';
  try {
    const { yaml } = await api.render(store.currentPlaybook());
    lastRenderedYaml = yaml;
    renderYaml(pre, yaml);
    status.textContent = 'rendered from current editor state';
    store.ui.yamlDirty = false;
  } catch (e) {
    status.textContent = '';
    const msg = e.problems?.length ? e.problems.join('\n') : e.message;
    pre.append(el('div', { class: 'yaml-error' }, `Cannot render: ${msg}`));
  }
}

function enterYamlEdit() {
  if (store.ui.yamlDirty) {
    // Ensure the editor starts from the current state, not stale text.
    api.render(store.currentPlaybook())
      .then(({ yaml }) => { lastRenderedYaml = yaml; $('#yaml-editor').value = yaml; })
      .catch(() => { $('#yaml-editor').value = lastRenderedYaml; });
  }
  $('#yaml-editor').value = lastRenderedYaml;
  $('#yaml-editor').classList.remove('hidden');
  $('#yaml-view').classList.add('hidden');
  $('#btn-edit-yaml').classList.add('hidden');
  $('#btn-render-yaml').classList.add('hidden');
  $('#btn-apply-yaml').classList.remove('hidden');
  $('#btn-cancel-yaml').classList.remove('hidden');
  $('#yaml-status').textContent = 'editing — Apply parses the YAML back into the editor';
}

function exitYamlEdit() {
  $('#yaml-editor').classList.add('hidden');
  $('#yaml-view').classList.remove('hidden');
  $('#btn-edit-yaml').classList.remove('hidden');
  $('#btn-render-yaml').classList.remove('hidden');
  $('#btn-apply-yaml').classList.add('hidden');
  $('#btn-cancel-yaml').classList.add('hidden');
  $('#yaml-status').textContent = '';
}

async function applyYamlEdit() {
  const yaml = $('#yaml-editor').value;
  const diagEl = $('#yaml-diagnostics');
  diagEl.replaceChildren();
  let res;
  try {
    res = await api.parseYaml(yaml, store.currentPlaybook()?.name ?? 'imported');
  } catch (e) {
    diagEl.append(el('div', { class: 'diag error' }, `Parse failed: ${e.message}`));
    return;
  }
  const diags = res.diagnostics ?? [];
  for (const d of diags) {
    diagEl.append(el('div', { class: `diag ${d.severity}` },
      el('span', { class: 'path' }, d.path), d.message));
  }
  if (diags.some((d) => d.severity === 'error')) {
    diagEl.prepend(el('div', { class: 'diag error' },
      'Errors must be resolved before this YAML can be applied.'));
    return;
  }
  const warns = diags.length ? ` with ${diags.length} warning(s) (listed above)` : '';
  if (!confirm(`Replace the current playbook${warns}? You can undo with Ctrl/Cmd+Z.`)) return;
  store.commit('yaml apply', (proj) => {
    // Keep the project's playbook slot; the imported playbook gets new IDs.
    proj.playbooks[0] = res.playbook;
  });
  store.editor.selected = null;
  exitYamlEdit();
  store.ui.yamlDirty = true;
  renderCanvas();
  renderProps();
  renderYamlView();
}

// ---------- Chrome (header/tabs/canvas wiring) ----------

function wireChrome() {
  $('#btn-undo').addEventListener('click', store.undo);
  $('#btn-redo').addEventListener('click', store.redo);
  $('#btn-deploy').addEventListener('click', () => {
    document.querySelector('#tabs .tab[data-tab="deployments"]').click();
  });
  document.addEventListener('keydown', (e) => {
    if (!(e.metaKey || e.ctrlKey)) return;
    if (e.target.matches('input, textarea, select')) return;
    if (e.key === 'z' && !e.shiftKey) { e.preventDefault(); store.undo(); }
    if (e.key === 'z' && e.shiftKey) { e.preventDefault(); store.redo(); }
    if (e.key === 's') { e.preventDefault(); saveProject(); }
  });

  $('#module-search').addEventListener('input', (e) => {
    store.ui.moduleSearch = e.target.value;
    renderModuleBrowser();
  });
  $('#module-collection').addEventListener('change', (e) => {
    store.ui.moduleCollection = e.target.value;
    renderModuleBrowser();
  });
}

function renderHistoryButtons() {
  $('#btn-undo').disabled = store.editor.past.length === 0;
  $('#btn-redo').disabled = store.editor.future.length === 0;
}

function wireCanvas() {
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

function wireTabs() {
  for (const btn of document.querySelectorAll('#tabs .tab')) {
    btn.addEventListener('click', () => {
      document.querySelectorAll('#tabs .tab').forEach((b) => b.classList.remove('active'));
      document.querySelectorAll('.tab-panel').forEach((p) => p.classList.remove('active'));
      btn.classList.add('active');
      const tab = btn.dataset.tab;
      $(`#tab-${tab}`).classList.add('active');
      store.ui.tab = tab;
      if (tab === 'yaml' && store.ui.yamlDirty) renderYamlView();
      if (tab === 'inventory') renderInventory();
      if (tab === 'deployments') renderDeployments();
    });
  }
  $('#btn-render-yaml').addEventListener('click', renderYamlView);
  $('#btn-edit-yaml').addEventListener('click', enterYamlEdit);
  $('#btn-apply-yaml').addEventListener('click', applyYamlEdit);
  $('#btn-cancel-yaml').addEventListener('click', exitYamlEdit);
}

boot();
