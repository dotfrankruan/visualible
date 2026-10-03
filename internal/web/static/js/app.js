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
import { wireLibrary, renderLibrary, refreshProjects, relativeTime } from './library.js';
import { wireProjectView, renderProject } from './project.js';
import { wireDocumentDialogs, downloadPlaybook, duplicatePlaybook, saveAsPlaybook, openImportDialog } from './documents.js';

const $ = (sel) => document.querySelector(sel);

async function boot() {
  wireChrome();
  wireBuild();
  wireTargets();
  wireReview();
  wireDeploy();
  wireAI();
  wireSettingsAI();
  wireLibrary();
  wireProjectView();
  wireDocumentDialogs();

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

  await Promise.all([refreshProjects(), loadSettings()]);
  renderProjectBar();

  // Deep links: /library, /projects/{id}, /projects/{id}/playbooks/{pid}
  await routeFromLocation();
  window.addEventListener('popstate', () => { routeFromLocation(); });
  // Any module can request a view change; rendering and URL stay in sync
  // here so there is one place that decides what is on screen.
  document.addEventListener('visualible:navigate', () => { renderView(); pushRoute(); });
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

// goStage switches the workflow stage inside an open playbook.
export function goStage(name) {
  document.querySelectorAll('.stage-btn').forEach((b) => b.classList.toggle('active', b.dataset.stage === name));
  document.querySelectorAll('.stage').forEach((s) => s.classList.remove('active'));
  $(`#stage-${name}`).classList.add('active');
  store.ui.view = 'playbook';
  store.ui.stage = name;
  renderProjectBar();
  if (name === 'build') renderBuild();
  if (name === 'targets') renderTargets();
  if (name === 'review') renderReview();
  if (name === 'deploy') renderDeploy();
  pushRoute();
}

// showLibrary returns to the library home.
export function showLibrary() {
  if (store.editor.dirty && !confirm('You have unsaved changes. Leave this playbook?')) return;
  store.ui.view = 'library';
  store.ui.openProjectId = null;
  renderView();
  pushRoute();
}

// showProject returns to the playbook list of the open project.
export function showProject() {
  if (!store.editor.project) { showLibrary(); return; }
  if (store.editor.dirty && !confirm('You have unsaved changes. Leave this playbook?')) return;
  store.ui.view = 'project';
  renderView();
  pushRoute();
}

// renderView shows exactly one top-level view.
function renderView() {
  document.querySelectorAll('.stage').forEach((s) => s.classList.remove('active'));
  document.querySelectorAll('.stage-btn').forEach((b) => b.classList.remove('active'));

  const view = store.ui.view;
  if (view === 'library') {
    $('#view-library').classList.add('active');
    $('#stage-nav').classList.add('hidden');
    $('#editor-tools').classList.add('hidden');
    renderProjectBar();
    renderLibrary();
    return;
  }
  if (view === 'project') {
    $('#view-project').classList.add('active');
    $('#stage-nav').classList.add('hidden');
    $('#editor-tools').classList.add('hidden');
    renderProjectBar();
    renderProject();
    return;
  }
  // Playbook open: the editor stages with their own navigation.
  $('#stage-nav').classList.remove('hidden');
  $('#editor-tools').classList.remove('hidden');
  goStage(store.ui.stage || 'build');
}

// ---------- Routing (History API; no framework) ----------

function pushRoute() {
  const project = store.editor.project;
  let path = '/library';
  if (store.ui.view === 'project' && project) path = `/projects/${encodeURIComponent(project.id)}`;
  if (store.ui.view === 'playbook' && project) {
    const pb = store.currentPlaybook();
    path = `/projects/${encodeURIComponent(project.id)}/playbooks/${encodeURIComponent(pb?.id ?? '')}`;
  }
  if (location.pathname !== path) history.pushState({ path }, '', path);
}

// routeFromLocation restores the view described by the URL. Playbook deep
// links load the project from the server, so a refresh returns to the same
// document.
async function routeFromLocation() {
  const parts = location.pathname.split('/').filter(Boolean);
  if (parts[0] !== 'projects' || !parts[1]) {
    store.ui.view = 'library';
    renderView();
    return;
  }
  const projectId = decodeURIComponent(parts[1]);
  const playbookId = parts[2] === 'playbooks' && parts[3] ? decodeURIComponent(parts[3]) : null;
  try {
    if (store.editor.project?.id !== projectId) {
      const project = await api.project(projectId);
      store.loadProject(project, playbookId);
    } else if (playbookId) {
      store.selectPlaybook(playbookId);
    }
  } catch {
    // Unknown project (deleted elsewhere): fall back to the library.
    store.ui.view = 'library';
    renderView();
    return;
  }
  store.ui.view = playbookId ? 'playbook' : 'project';
  renderView();
}

// ---------- Header chrome ----------

function wireChrome() {
  document.querySelectorAll('.stage-btn').forEach((b) =>
    b.addEventListener('click', () => goStage(b.dataset.stage)));

  $('#btn-library').addEventListener('click', showLibrary);
  $('#crumb-library').addEventListener('click', showLibrary);
  $('#crumb-project').addEventListener('click', showProject);

  $('#btn-undo').addEventListener('click', store.undo);
  $('#btn-redo').addEventListener('click', store.redo);
  $('#btn-export').addEventListener('click', () => {
    const pb = store.currentPlaybook();
    if (pb) downloadPlaybook(pb);
  });
  $('#btn-duplicate').addEventListener('click', async () => {
    const pb = store.currentPlaybook();
    if (!pb) return;
    const copy = duplicatePlaybook(pb);
    if (copy) {
      store.selectPlaybook(copy.id);
      await saveProject().catch(() => {});
      renderProjectBar();
      goStage('build');
    }
  });
  $('#btn-saveas').addEventListener('click', () => saveAsPlaybook());

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

  // Import is available from the editor too: Cmd/Ctrl+Shift+I.
  document.addEventListener('keydown', (e) => {
    if ((e.metaKey || e.ctrlKey) && e.shiftKey && e.key.toLowerCase() === 'i') {
      e.preventDefault();
      openImportDialog({ target: 'project' });
    }
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

// renderProjectBar keeps the breadcrumb ("Library / Project / Playbook")
// and the editor toolbar in sync with what is actually open, so the user
// never has to wonder which document they are editing.
function renderProjectBar() {
  const p = store.editor.project;
  const pb = store.currentPlaybook();
  const inEditor = store.ui.view === 'playbook' && p && pb;

  const crumbProject = $('#crumb-project');
  const crumbPlaybook = $('#crumb-playbook');
  const showProjectCrumb = store.ui.view !== 'library' && !!p;
  crumbProject.classList.toggle('hidden', !showProjectCrumb);
  crumbProject.textContent = p ? p.name : '—';
  $('#crumb-project-sep').classList.toggle('hidden', !showProjectCrumb);

  crumbPlaybook.classList.toggle('hidden', !inEditor);
  $('#crumb-playbook-sep').classList.toggle('hidden', !inEditor);
  if (inEditor) {
    crumbPlaybook.textContent = pb.name + (store.editor.dirty ? ' •' : '');
    crumbPlaybook.title = store.editor.dirty ? 'Unsaved changes' : 'Saved';
  }
  $('#project-dirty').classList.toggle('hidden', !store.editor.dirty);
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

// freshProject creates an in-memory project for the "start from scratch"
// path before anything has been persisted.
export function freshProject() {
  return {
    id: store.newId('proj'),
    name: 'Untitled project',
    playbooks: [{
      id: store.newId('pb'),
      name: 'My automation',
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
      plays: [{ id: store.newId('play'), name: 'My automation', hosts: 'all', tasks: [], handlers: [] }],
    }],
    inventories: [],
  };
}

boot();
