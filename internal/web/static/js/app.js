// Visualible frontend entry point: boot, stage navigation, and header
// chrome. The workflow is organized around four stages — Build, Targets,
// Review, Deploy — while the IR underneath stays the single source of
// truth for every view.

import { api } from './api.js';
import * as store from './store.js';
import { el } from './form.js';
import { humanizeProblems } from './errors.js';
import { wireBuild, renderBuild } from './build.js';
import { wireTargets, renderTargets } from './targets.js';
import { wireReview, renderReview } from './review.js';
import { wireDeploy, renderDeploy } from './deploy.js';
import { wireAI } from './ai.js';
import { wireSettingsAI } from './settings.js';

const $ = (sel) => document.querySelector(sel);

async function boot() {
  wireChrome();
  wireProjectModal();
  wireBuild();
  wireTargets();
  wireReview();
  wireDeploy();
  wireAI();
  wireSettingsAI();

  store.on('editor', () => { renderBuild(); renderProjectBar(); });
  store.on('history', renderHistoryButtons);
  store.on('project', renderProjectBar);

  try {
    const health = await api.health();
    store.server.health = health;
    renderAnsibleStatus();
  } catch (e) {
    renderAnsibleStatus(e);
  }

  await Promise.all([openInitialProject(), loadSettings()]);
  renderBuild();
  renderProjectBar();
}

async function loadSettings() {
  try {
    store.server.settings = await api.settings();
  } catch { /* settings optional */ }
}

export function aiConfigured() {
  const s = store.server.settings;
  return !!(s?.ai?.endpoint && s?.ai?.model);
}

// ---------- Stage navigation ----------

export function goStage(name) {
  document.querySelectorAll('.stage-btn').forEach((b) => b.classList.toggle('active', b.dataset.stage === name));
  document.querySelectorAll('.stage').forEach((s) => s.classList.remove('active'));
  $(`#stage-${name}`).classList.add('active');
  store.ui.stage = name;
  if (name === 'build') renderBuild();
  if (name === 'targets') renderTargets();
  if (name === 'review') renderReview();
  if (name === 'deploy') renderDeploy();
}

// ---------- Header chrome ----------

function wireChrome() {
  document.querySelectorAll('.stage-btn').forEach((b) =>
    b.addEventListener('click', () => goStage(b.dataset.stage)));

  $('#btn-undo').addEventListener('click', store.undo);
  $('#btn-redo').addEventListener('click', store.redo);

  // Escape closes whatever is open; Ctrl/Cmd+Enter submits AI intents.
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      const open = [...document.querySelectorAll('.modal:not(.hidden)')].pop();
      if (open) { open.classList.add('hidden'); e.preventDefault(); }
      $('#ansible-popover').classList.add('hidden');
      return;
    }
    if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
      if (e.target.id === 'ai-intent' || e.target.id === 'ai-bar-input' || e.target.id === 'empty-ai-intent') {
        e.preventDefault();
        if (e.target.id === 'ai-bar-input') $('#ai-bar-generate').click();
        else if (e.target.id === 'empty-ai-intent') $('#empty-ai-generate').click();
        else $('#ai-generate').click();
      }
      return;
    }
    if (!(e.metaKey || e.ctrlKey)) return;
    if (e.target.matches('input, textarea, select')) return;
    if (e.key === 'z' && !e.shiftKey) { e.preventDefault(); store.undo(); }
    if (e.key === 'z' && e.shiftKey) { e.preventDefault(); store.redo(); }
    if (e.key === 's') { e.preventDefault(); saveProject(); }
  });

  const pill = $('#ansible-status');
  pill.addEventListener('click', (e) => {
    e.stopPropagation();
    const pop = $('#ansible-popover');
    if (pop.classList.contains('hidden')) renderAnsiblePopover();
    pop.classList.toggle('hidden');
  });
  document.addEventListener('click', (e) => {
    if (!e.target.closest('#ansible-popover')) $('#ansible-popover').classList.add('hidden');
  });
}

function renderHistoryButtons() {
  $('#btn-undo').disabled = store.editor.past.length === 0;
  $('#btn-redo').disabled = store.editor.future.length === 0;
}

// ---------- Ansible status (simple by default, details on demand) ----------

function renderAnsibleStatus(err) {
  const pill = $('#ansible-status');
  if (err) {
    pill.textContent = '○ Backend unreachable';
    pill.className = 'status-pill err';
    return;
  }
  const a = store.server.health?.ansible;
  if (a?.available) {
    pill.textContent = '● Ansible ready';
    pill.className = 'status-pill ok';
  } else {
    pill.textContent = '○ Ansible not found';
    pill.className = 'status-pill err';
  }
}

function renderAnsiblePopover() {
  const pop = $('#ansible-popover');
  const h = store.server.health;
  const a = h?.ansible;
  pop.replaceChildren();
  if (!a?.available) {
    pop.append(
      el('div', { style: 'margin-bottom:6px;font-weight:600' }, 'Ansible was not detected'),
      el('div', { class: 'hint-line' },
        'Install Ansible (e.g. pipx install ansible) and restart Visualible, or set explicit paths in Settings.'),
    );
    return;
  }
  const rows = [
    ['Status', 'ready'],
    ['Version', a.version || '?'],
    ['Path', a.path || '?'],
    ['Modules', h.modules ? h.modules.toLocaleString() : 'not yet discovered'],
    ['Database', h.dbPath || '?'],
    ['Data dir', h.dataDir || '?'],
  ];
  for (const [k, v] of rows) {
    pop.append(el('div', { class: 'pop-row' },
      el('span', { class: 'k' }, k), el('span', { class: 'v' }, v)));
  }
}

// ---------- Project bar / persistence ----------

function renderProjectBar() {
  const p = store.editor.project;
  $('#project-name').textContent = p ? p.name : '—';
  $('#project-dirty').style.display = store.editor.dirty ? '' : 'none';
  $('#btn-save').disabled = !store.editor.dirty;
}

export async function saveProject() {
  const p = store.editor.project;
  if (!p) return;
  $('#btn-save').disabled = true;
  try {
    const saved = await api.saveProject(p);
    store.markSaved(saved);
  } catch (e) {
    // Explain validation problems in plain language; the technical text
    // stays available in the log/details.
    const msg = e.problems?.length ? humanizeProblems(e.problems) : e.message;
    alert(`Could not save:\n\n${msg}`);
    $('#btn-save').disabled = false;
  }
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

export function freshProject() {
  return {
    id: store.newId('proj'),
    name: 'Untitled project',
    playbooks: [{
      id: store.newId('pb'),
      name: 'playbook',
      plays: [{ id: store.newId('play'), name: 'My automation', hosts: 'all', tasks: [], handlers: [] }],
    }],
    inventories: [],
  };
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
      renderBuild();
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
    renderBuild();
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
      renderBuild();
    }
    await refreshProjectList();
  } catch (e) {
    alert(`Delete failed: ${e.message}`);
  }
}

boot();
