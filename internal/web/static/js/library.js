// Library: the front door of the product. Projects are workspaces; each
// holds one or more playbooks (independently editable automation
// documents). Nothing here uses Ansible vocabulary.

import { api } from './api.js';
import * as store from './store.js';
import { el } from './form.js';
import { humanizeProblems } from './errors.js';
import { openImportDialog, openNewProjectDialog } from './documents.js';

const $ = (sel) => document.querySelector(sel);

export function wireLibrary() {
  $('#library-new-project').addEventListener('click', () => openNewProjectDialog());
  $('#library-import').addEventListener('click', () => openImportDialog({ target: 'library' }));
  $('#library-ai')?.addEventListener('click', startWithAI);
  $('#library-search').addEventListener('input', (e) => {
    store.ui.librarySearch = e.target.value;
    renderLibrary();
  });
}

export async function renderLibrary() {
  await refreshProjects();
  const wrap = $('#library-main');
  wrap.replaceChildren();

  const projects = store.server.projects ?? [];

  // First run: a calm, actionable empty state. AI is offered only when it
  // is actually configured — never as a nag.
  if (!projects.length) {
    const hero = el('div', { class: 'library-hero' },
      el('h2', {}, 'Your automation library'),
      el('p', { class: 'stage-sub' },
        'Create reusable automation and deploy it whenever you need it.'),
      el('div', { class: 'library-hero-actions' },
        el('button', { class: 'primary', onclick: () => openNewProjectDialog() }, '+ New project'),
        el('button', { class: 'mini-btn', onclick: () => openImportDialog({ target: 'library' }) },
          'Import Ansible YAML')));
    if (aiConfigured()) {
      hero.append(el('div', { class: 'library-hero-ai' },
        el('button', { class: 'mini-btn', onclick: startWithAI },
          '✨ Describe your first automation')));
    }
    wrap.append(hero);
    return;
  }

  const query = (store.ui.librarySearch ?? '').trim().toLowerCase();
  const matches = (p) => {
    if (!query) return true;
    return p.name.toLowerCase().includes(query) ||
      (p.description ?? '').toLowerCase().includes(query) ||
      (p.playbooks ?? []).some((pb) => pb.name.toLowerCase().includes(query) ||
        (pb.description ?? '').toLowerCase().includes(query));
  };
  const visible = projects.filter(matches);

  // Recent playbooks across projects: fastest path back into work.
  const recent = [];
  for (const p of projects) {
    for (const pb of p.playbooks ?? []) {
      recent.push({ project: p, playbook: pb });
    }
  }
  recent.sort((a, b) => new Date(b.playbook.updatedAt ?? 0) - new Date(a.playbook.updatedAt ?? 0));

  if (!query && recent.length) {
    const block = el('section', { class: 'library-block' }, el('h3', {}, 'Recent'));
    for (const r of recent.slice(0, 5)) {
      block.append(el('button', {
        class: 'recent-row',
        onclick: () => openPlaybook(r.project.id, r.playbook.id),
      },
        el('span', { class: 'recent-name' }, r.playbook.name),
        el('span', { class: 'recent-project' }, r.project.name),
        el('span', { class: 'recent-time' }, relativeTime(r.playbook.updatedAt))));
    }
    wrap.append(block);
  }

  const allBlock = el('section', { class: 'library-block' },
    el('h3', {}, query ? `Results for “${query}”` : 'All projects'));
  if (!visible.length) {
    allBlock.append(el('div', { class: 'empty-state' }, 'No projects match that search.'));
  }
  for (const p of visible) {
    allBlock.append(projectCard(p));
  }
  wrap.append(allBlock);
}

function projectCard(p) {
  const count = (p.playbooks ?? []).length;
  return el('button', {
    class: 'project-card',
    onclick: () => openProject(p.id),
  },
    el('div', { class: 'pc-name' }, p.name),
    el('div', { class: 'pc-meta' },
      `${count} playbook${count === 1 ? '' : 's'} · modified ${relativeTime(p.updatedAt)}`),
    p.description ? el('div', { class: 'pc-desc' }, p.description) : null,
    el('div', { class: 'pc-playbooks' },
      (p.playbooks ?? []).slice(0, 3).map((pb) =>
        el('span', { class: 'pc-chip' }, pb.name)).concat(
        count > 3 ? [el('span', { class: 'pc-chip muted' }, `+${count - 3} more`)] : [])));
}

// aiConfigured mirrors app.aiConfigured without importing app.js, which
// would create a module cycle.
function aiConfigured() {
  const ai = store.server.settings?.ai;
  return !!(ai?.endpoint && ai?.model);
}

// startWithAI creates a first document (if needed) and opens the AI
// request dialog, so the fastest path from nothing to automation is one
// click.
async function startWithAI() {
  try {
    if (!store.editor.project) {
      const project = await api.createProject('My automation', '');
      store.loadProject(project);
      await refreshProjects();
    }
    if (!store.currentPlaybook()) {
      alert('Create a playbook first — the AI edits the playbook you are working on.');
      return;
    }
    store.ui.view = 'playbook';
    store.ui.stage = 'build';
    document.dispatchEvent(new CustomEvent('visualible:navigate'));
    $('#btn-ai').click();
  } catch (e) {
    alert(`Could not start: ${e.message}`);
  }
}

export async function refreshProjects() {
  try {
    const { projects } = await api.projects();
    store.server.projects = projects ?? [];
  } catch (e) {
    store.server.projects = [];
    $('#library-main')?.replaceChildren(
      el('div', { class: 'empty-state' }, `Could not load your library: ${e.message}`));
  }
}

export function openProject(id) {
  if (store.editor.dirty && !confirm('You have unsaved changes in the open project. Discard them?')) return;
  goToProject(id);
}

// goToProject loads a project from the server and shows its playbook list.
export async function goToProject(id) {
  try {
    const project = store.editor.project?.id === id ? store.editor.project : await api.project(id);
    if (store.editor.project?.id !== id) store.loadProject(project);
    store.ui.openProjectId = id;
    store.ui.view = 'project';
    document.dispatchEvent(new CustomEvent('visualible:navigate'));
  } catch (e) {
    alert(`Could not open that project: ${e.message}`);
  }
}

export function openPlaybook(projectId, playbookId) {
  const navigate = async () => {
    try {
      let project = store.editor.project;
      if (!project || project.id !== projectId) {
        project = await api.project(projectId);
      }
      // The server load drops unsaved edits, so only reload when switching.
      if (store.editor.project?.id !== projectId) {
        store.loadProject(project, playbookId);
      } else {
        store.selectPlaybook(playbookId);
      }
      store.ui.view = 'playbook';
      store.ui.stage = 'build';
      document.dispatchEvent(new CustomEvent('visualible:navigate'));
    } catch (e) {
      alert(`Could not open that playbook: ${e.message}`);
    }
  };
  if (store.editor.dirty && store.editor.project?.id !== projectId) {
    if (!confirm('You have unsaved changes. Discard them and open another project?')) return;
  }
  navigate();
}

// relativeTime renders deterministic, human timestamps from persistence.
export function relativeTime(iso) {
  if (!iso) return 'never';
  const then = new Date(iso);
  if (Number.isNaN(then.getTime())) return 'unknown';
  const seconds = Math.max(0, Math.round((Date.now() - then.getTime()) / 1000));
  if (seconds < 60) return 'just now';
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes} minute${minutes === 1 ? '' : 's'} ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours} hour${hours === 1 ? '' : 's'} ago`;
  const days = Math.round(hours / 24);
  if (days === 1) return 'yesterday';
  if (days < 30) return `${days} days ago`;
  return then.toLocaleDateString();
}

export { humanizeProblems };
