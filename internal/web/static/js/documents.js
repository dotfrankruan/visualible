// Document operations shared by the Library, the project view and the
// editor toolbar: create, duplicate, export, import and Save As.
//
// Visualible follows one predictable export rule: exports always render
// the *current working state* of the document, so what you download is
// what you see. Unsaved changes are exported too, and the UI says so.

import { api } from './api.js';
import * as store from './store.js';
import { el } from './form.js';
import { humanizeProblems } from './errors.js';

const $ = (sel) => document.querySelector(sel);

function newId(prefix) {
  return store.newId(prefix);
}

// ---------- New project ----------

export function openNewProjectDialog() {
  openDialog({
    title: 'New project',
    fields: [
      { id: 'name', label: 'Name', placeholder: 'Homelab', required: true },
      { id: 'description', label: 'Description (optional)', placeholder: 'My home lab automation' },
    ],
    submitLabel: 'Create project',
    onSubmit: async (values) => {
      const project = await api.createProject(values.name, values.description ?? '');
      store.loadProject(project);
      store.ui.view = 'project';
      document.dispatchEvent(new CustomEvent('visualible:navigate'));
      return true;
    },
  });
}

// ---------- New playbook ----------

export function openNewPlaybookDialog() {
  openDialog({
    title: 'New playbook',
    fields: [
      { id: 'name', label: 'Name', placeholder: 'Configure nginx', required: true },
      { id: 'description', label: 'Description (optional)', placeholder: 'Reverse proxy for the web servers' },
      { id: 'hosts', label: 'Run on', placeholder: 'all', help: 'A machine group name, or "all".' },
    ],
    submitLabel: 'Create playbook',
    onSubmit: async (values) => {
      const project = store.editor.project;
      if (!project) throw new Error('Open a project first.');
      const playbook = emptyPlaybook(values.name, values.description, values.hosts);
      store.commit('add playbook', (proj) => {
        proj.playbooks = proj.playbooks ?? [];
        proj.playbooks.push(playbook);
      });
      store.selectPlaybook(playbook.id);
      store.ui.view = 'playbook';
      store.ui.stage = 'build';
      document.dispatchEvent(new CustomEvent('visualible:navigate'));
      return true;
    },
  });
}

export function emptyPlaybook(name, description, hosts) {
  const now = new Date().toISOString();
  return {
    id: newId('pb'),
    name: name || 'Untitled playbook',
    description: description || undefined,
    createdAt: now,
    updatedAt: now,
    plays: [{
      id: newId('play'),
      name: name || 'New play',
      hosts: hosts || 'all',
      tasks: [],
      handlers: [],
    }],
  };
}

// ---------- Duplicate ----------

export function duplicatePlaybook(pb) {
  const project = store.editor.project;
  if (!project) return null;
  // Copy the IR and assign fresh stable IDs, so the copy is independent.
  const copy = JSON.parse(JSON.stringify(pb));
  copy.id = newId('pb');
  copy.name = uniqueName(pb.name, (project.playbooks ?? []).map((x) => x.name));
  delete copy.description;
  copy.createdAt = new Date().toISOString();
  copy.updatedAt = copy.createdAt;
  for (const play of copy.plays ?? []) {
    play.id = newId('play');
    for (const list of [play.tasks, play.handlers, play.preTasks, play.postTasks]) {
      for (const task of list ?? []) task.id = newId('t');
    }
  }
  // Tasks that notify handlers keep working: handler names are copied too.
  store.commit('duplicate playbook', (proj) => {
    proj.playbooks = proj.playbooks ?? [];
    proj.playbooks.push(copy);
  });
  return copy;
}

function uniqueName(base, taken) {
  const copyName = `${base} copy`;
  if (!taken.includes(copyName)) return copyName;
  for (let n = 2; ; n++) {
    const candidate = `${base} copy ${n}`;
    if (!taken.includes(candidate)) return candidate;
  }
}

// ---------- Export ----------

// downloadPlaybook fetches the canonical YAML from the server and offers
// it as a file. The server renders the stored document; when the editor
// has unsaved changes we render the working state instead so the download
// always matches what the user sees.
export async function downloadPlaybook(pb) {
  const project = store.editor.project;
  const isOpen = project?.id && store.playbookById?.(pb.id) && store.editor.playbookId === pb.id;
  try {
    if (isOpen && store.editor.dirty) {
      const { yaml } = await api.render(pb);
      saveBlob(yaml, filenameFor(pb.name, 'yml'), 'application/yaml');
      return;
    }
    if (project?.id) {
      const resp = await fetch(
        `/api/projects/${encodeURIComponent(project.id)}/playbooks/${encodeURIComponent(pb.id)}/export/yaml`);
      if (resp.ok) {
        const text = await resp.text();
        saveBlob(text, filenameFor(pb.name, 'yml'), 'application/yaml');
        return;
      }
    }
    // Unsaved project: export the working state directly.
    const { yaml } = await api.render(pb);
    saveBlob(yaml, filenameFor(pb.name, 'yml'), 'application/yaml');
  } catch (e) {
    const msg = e.problems?.length ? humanizeProblems(e.problems) : e.message;
    alert(`Could not export this playbook:\n\n${msg}`);
  }
}

function saveBlob(text, filename, contentType) {
  const blob = new Blob([text], { type: contentType });
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = filename;
  a.click();
  URL.revokeObjectURL(a.href);
}

// filenameFor mirrors internal/artifact slugging (unit-tested there).
export function filenameFor(name, ext) {
  const slug = (name ?? '')
    .normalize('NFC')
    .toLowerCase()
    .replace(/[^\p{L}\p{N}]+/gu, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 80);
  return `${slug || 'playbook'}.${ext}`;
}

// ---------- Import ----------

export function openImportDialog({ target }) {
  const project = store.editor.project;
  const destinations = [];
  if (target === 'project' && project) {
    destinations.push({ value: 'current', label: `Add to “${project.name}” as a new playbook` });
    destinations.push({ value: 'new-project', label: 'Create a new project' });
  } else {
    destinations.push({ value: 'new-project', label: 'Create a new project' });
    for (const p of store.server.projects ?? []) {
      destinations.push({ value: p.id, label: `Add to “${p.name}”` });
    }
  }

  openDialog({
    title: 'Import Ansible YAML',
    fields: [
      { id: 'yaml', label: 'Playbook YAML', type: 'textarea', required: true,
        placeholder: '- name: Configure webserver\n  hosts: web\n  tasks:\n    - name: Install nginx\n      ansible.builtin.package:\n        name: nginx\n        state: present' },
      { id: 'name', label: 'Playbook name', placeholder: 'Imported playbook' },
      { id: 'destination', label: 'Import into', type: 'select', choices: destinations, required: true },
    ],
    submitLabel: 'Import',
    onSubmit: async (values) => {
      const res = await api.parseYaml(values.yaml, values.name || 'Imported playbook');
      const diagnostics = res.diagnostics ?? [];
      const errors = diagnostics.filter((d) => d.severity === 'error');
      if (errors.length) {
        showDiagnostics(errors);
        throw new Error('The YAML contains constructs Visualible cannot represent safely.');
      }
      const imported = res.playbook;
      imported.id = newId('pb');
      imported.name = values.name || imported.name || 'Imported playbook';
      imported.createdAt = new Date().toISOString();
      imported.updatedAt = imported.createdAt;

      if (values.destination === 'new-project') {
        const project = await api.createProject(imported.name, '');
        project.playbooks = [imported];
        project.playbooks[0].id = project.playbooks[0].id || newId('pb');
        await api.saveProject(project);
        await refreshLibrary();
        store.loadProject(project);
        store.ui.view = 'project';
      } else if (values.destination === 'current') {
        store.commit('import playbook', (proj) => {
          proj.playbooks = proj.playbooks ?? [];
          proj.playbooks.push(imported);
        });
        store.selectPlaybook(imported.id);
        store.ui.view = 'playbook';
        store.ui.stage = 'build';
      } else {
        const project = await api.project(values.destination);
        project.playbooks = project.playbooks ?? [];
        project.playbooks.push(imported);
        const saved = await api.saveProject(project);
        await refreshLibrary();
        store.loadProject(saved, imported.id);
        store.ui.view = 'playbook';
        store.ui.stage = 'build';
      }

      if (diagnostics.length) {
        // Warnings are preserved and reported, never silently dropped.
        setTimeout(() => showDiagnostics(diagnostics), 50);
      }
      document.dispatchEvent(new CustomEvent('visualible:navigate'));
      return true;
    },
  });
}

function showDiagnostics(diagnostics) {
  const wrap = $('#doc-diagnostics');
  if (!wrap) return;
  wrap.replaceChildren();
  for (const d of diagnostics) {
    wrap.append(el('div', { class: `diag ${d.severity}` },
      el('span', { class: 'path' }, d.path), d.message));
  }
  if (diagnostics.length) {
    wrap.append(el('div', { class: 'hint-line' },
      diagnostics.some((d) => d.severity === 'error')
        ? 'Errors must be resolved before importing.'
        : 'Imported with warnings — the constructs above are preserved as advanced steps.'));
  }
}

async function refreshLibrary() {
  const mod = await import('./library.js');
  await mod.refreshProjects();
}

// ---------- Save As ----------

export async function saveAsPlaybook() {
  const pb = store.currentPlaybook();
  if (!pb) return;
  openDialog({
    title: 'Save as',
    fields: [
      { id: 'name', label: 'New playbook name', required: true, placeholder: `${pb.name} variant` },
    ],
    submitLabel: 'Save as new playbook',
    onSubmit: async (values) => {
      const copy = JSON.parse(JSON.stringify(pb));
      copy.id = newId('pb');
      copy.name = values.name;
      copy.createdAt = new Date().toISOString();
      copy.updatedAt = copy.createdAt;
      for (const play of copy.plays ?? []) {
        play.id = newId('play');
        for (const list of [play.tasks, play.handlers, play.preTasks, play.postTasks]) {
          for (const task of list ?? []) task.id = newId('t');
        }
      }
      store.commit('save as', (proj) => {
        proj.playbooks = proj.playbooks ?? [];
        proj.playbooks.push(copy);
      });
      store.selectPlaybook(copy.id);
      document.dispatchEvent(new CustomEvent('visualible:navigate'));
      return true;
    },
  });
}

// ---------- Generic dialog ----------

let dialogSubmit = null;

export function openDialog({ title, fields, submitLabel, onSubmit }) {
  const modal = $('#doc-modal');
  $('#doc-modal-title').textContent = title;
  const body = $('#doc-modal-body');
  body.replaceChildren();
  $('#doc-diagnostics').replaceChildren();

  const values = {};
  for (const f of fields) {
    const label = el('label', { class: 'set-label' }, f.label);
    let input;
    if (f.type === 'textarea') {
      input = el('textarea', { placeholder: f.placeholder ?? '' });
    } else if (f.type === 'select') {
      input = el('select', {});
      for (const c of f.choices ?? []) input.append(el('option', { value: c.value }, c.label));
    } else {
      input = el('input', { type: 'text', placeholder: f.placeholder ?? '' });
    }
    input.dataset.fieldId = f.id;
    label.append(input);
    if (f.help) label.append(el('div', { class: 'fdesc' }, f.help));
    body.append(label);
    values[f.id] = f.type === 'textarea' ? '' : (input.value ?? '');
    input.addEventListener('input', () => { values[f.id] = input.value; });
    input.addEventListener('change', () => { values[f.id] = input.value; });
  }

  dialogSubmit = async () => {
    const missing = fields.filter((f) => f.required && !String(values[f.id] ?? '').trim());
    if (missing.length) {
      showDiagnostics([{ severity: 'error', path: missing[0].id, message: `${missing[0].label} is required.` }]);
      return;
    }
    const btn = $('#doc-modal-submit');
    btn.disabled = true;
    try {
      const done = await onSubmit(values);
      if (done) closeDialog();
    } catch (e) {
      const msg = e.problems?.length ? humanizeProblems(e.problems) : e.message;
      showDiagnostics([{ severity: 'error', path: '', message: msg }]);
    } finally {
      btn.disabled = false;
    }
  };

  $('#doc-modal-submit').textContent = submitLabel;
  modal.classList.remove('hidden');
  body.querySelector('input, textarea, select')?.focus();
}

export function closeDialog() {
  $('#doc-modal').classList.add('hidden');
  dialogSubmit = null;
}

export function submitDialog() {
  dialogSubmit?.();
}

export function wireDocumentDialogs() {
  $('#doc-modal-close').addEventListener('click', closeDialog);
  $('#doc-modal-submit').addEventListener('click', () => submitDialog());
  $('#doc-modal').addEventListener('click', (e) => {
    if (e.target.id === 'doc-modal') closeDialog();
  });
}
