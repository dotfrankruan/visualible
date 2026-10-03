// Project view: the documents inside one workspace. Creating, opening,
// renaming, duplicating, deleting, importing and exporting playbooks all
// live here in plain sight — no hidden menus.

import { api } from './api.js';
import * as store from './store.js';
import { el } from './form.js';
import { saveProject } from './app.js';
import { openImportDialog, openNewPlaybookDialog, duplicatePlaybook, downloadPlaybook } from './documents.js';
import { relativeTime } from './library.js';

const $ = (sel) => document.querySelector(sel);

export function wireProjectView() {
  $('#project-new-playbook').addEventListener('click', () => openNewPlaybookDialog());
  $('#project-import').addEventListener('click', () => openImportDialog({ target: 'project' }));
  $('#project-rename').addEventListener('click', renameProject);
  $('#project-export-all')?.addEventListener('click', exportAllPlaybooks);
}

export function renderProject() {
  const project = store.editor.project;
  const wrap = $('#project-main');
  if (!wrap) return;
  wrap.replaceChildren();
  if (!project) {
    wrap.append(el('div', { class: 'empty-state' }, 'No project open.'));
    return;
  }

  $('#project-title').textContent = project.name;
  $('#project-subtitle').textContent = project.description ||
    `${(project.playbooks ?? []).length} playbook(s)`;

  const list = el('div', { class: 'playbook-list' });
  const playbooks = project.playbooks ?? [];
  if (!playbooks.length) {
    list.append(el('div', { class: 'empty-state' },
      'No playbooks yet. Create one or import existing Ansible YAML.'));
  }
  for (const pb of playbooks) {
    list.append(playbookCard(pb));
  }
  wrap.append(el('section', { class: 'library-block' },
    el('h3', {}, 'Playbooks'), list));
}

function playbookCard(pb) {
  const steps = countSteps(pb);
  const isOpen = store.editor.playbookId === pb.id;

  const actions = el('div', { class: 'pb-actions' });
  const action = (label, fn, extra = {}) => {
    const b = el('button', { class: 'mini-btn ' + (extra.class ?? ''), ...extra }, label);
    b.addEventListener('click', (e) => { e.stopPropagation(); fn(); });
    return b;
  };

  actions.append(
    action('Open', () => openPlaybook(pb)),
    action('Rename', () => renamePlaybook(pb)),
    action('Duplicate', () => {
      duplicatePlaybook(pb);
      renderProject();
    }),
    action('Export YAML', () => downloadPlaybook(pb)),
    action('Delete', () => deletePlaybook(pb), { class: 'danger' }),
  );

  const card = el('div', { class: 'playbook-card' + (isOpen ? ' open' : '') },
    el('div', { class: 'pb-main' },
      el('div', { class: 'pb-name' }, pb.name),
      el('div', { class: 'pb-meta' },
        `${steps} step${steps === 1 ? '' : 's'} · modified ${relativeTime(pb.updatedAt)}`),
      pb.description ? el('div', { class: 'pb-desc' }, pb.description) : null),
    actions);

  card.addEventListener('click', () => openPlaybook(pb));
  return card;
}

function countSteps(pb) {
  let n = 0;
  for (const play of pb.plays ?? []) {
    n += (play.tasks ?? []).length + (play.handlers ?? []).length;
  }
  return n;
}

function openPlaybook(pb) {
  store.selectPlaybook(pb.id);
  store.ui.view = 'playbook';
  store.ui.stage = 'build';
  document.dispatchEvent(new CustomEvent('visualible:navigate'));
}

async function renameProject() {
  const project = store.editor.project;
  if (!project) return;
  const name = prompt('Project name', project.name);
  if (name === null) return;
  const trimmed = name.trim();
  if (!trimmed || trimmed === project.name) return;
  store.commit('rename project', () => { project.name = trimmed; });
  renderProject();
  await saveIfPossible();
}

async function renamePlaybook(pb) {
  const name = prompt('Playbook name', pb.name);
  if (name === null) return;
  const trimmed = name.trim();
  if (!trimmed || trimmed === pb.name) return;
  store.commit('rename playbook', () => { pb.name = trimmed; });
  renderProject();
  await saveIfPossible();
}

async function deletePlaybook(pb) {
  const project = store.editor.project;
  const steps = countSteps(pb);
  const ok = confirm(
    `Delete “${pb.name}”?\n\n` +
    `This removes ${steps} automation step${steps === 1 ? '' : 's'} from Visualible.\n` +
    `It does not change machines that were already configured with it.`);
  if (!ok) return;
  store.commit('delete playbook', (proj) => {
    const i = proj.playbooks.findIndex((x) => x.id === pb.id);
    if (i >= 0) proj.playbooks.splice(i, 1);
  });
  const remaining = project.playbooks ?? [];
  if (store.editor.playbookId === pb.id) {
    store.editor.playbookId = remaining[0]?.id ?? null;
  }
  renderProject();
  await saveIfPossible();
}

// exportAllPlaybooks downloads every playbook in the project, one file
// each. Filenames stay distinct even when two documents share a name.
async function exportAllPlaybooks() {
  const project = store.editor.project;
  if (!project) return;
  for (const pb of project.playbooks ?? []) {
    await downloadPlaybook(pb);
  }
}

// saveIfPossible persists the project when it is a stored document. A
// brand-new unsaved project is left for the user to save explicitly.
async function saveIfPossible() {
  const project = store.editor.project;
  if (!project) return;
  const stored = (store.server.projects ?? []).some((p) => p.id === project.id);
  if (!stored) return;
  try {
    await saveProject();
  } catch (e) {
    alert(`Could not save: ${e.message}`);
  }
}
