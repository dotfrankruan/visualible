// Deployments tab: launch form, deployment list, and a structured live
// view driven by normalized Server-Sent Events (never raw terminal only).

import { api } from './api.js';
import * as store from './store.js';
import { el } from './form.js';

const $ = (sel) => document.querySelector(sel);

let selectedDeployment = null;
let eventSource = null;
// taskViews tracks rendered task rows for live updates: key -> row el.
let taskViews = new Map();
let liveLogEl = null;

export function wireDeploymentsTab() {
  $('#deploy-run-form').addEventListener('submit', startDeployment);
  $('#deploy-refresh').addEventListener('click', loadDeployments);
}

export async function renderDeployments() {
  await Promise.all([loadBackendInfo(), loadDeployments()]);
  renderRunFormState();
}

async function loadBackendInfo() {
  try {
    store.server.deployBackend = await api.deployBackend();
  } catch {
    store.server.deployBackend = null;
  }
}

function renderRunFormState() {
  const btn = $('#deploy-run-btn');
  const note = $('#deploy-note');
  const ansibleUp = store.server.health?.ansible?.available;
  const inv = store.editor.project?.inventories?.[0];
  const hosts = inv ? countHosts(inv) : 0;
  if (!ansibleUp) {
    btn.disabled = true;
    note.textContent = 'Ansible is not detected; deployments are unavailable.';
  } else if (!hosts) {
    btn.disabled = true;
    note.textContent = 'Add at least one host in the Inventory tab first.';
  } else if (store.editor.dirty) {
    btn.disabled = true;
    note.textContent = 'Save the project before deploying (Ctrl/Cmd+S).';
  } else {
    btn.disabled = false;
    note.textContent = `${hosts} host(s) targeted.`;
  }
}

function countHosts(inv) {
  let n = (inv.hosts ?? []).length;
  const walk = (g) => {
    n += (g.hosts ?? []).length;
    (g.children ?? []).forEach(walk);
  };
  (inv.groups ?? []).forEach(walk);
  return n;
}

async function startDeployment(e) {
  e.preventDefault();
  const proj = store.editor.project;
  if (!proj) return;
  const plan = {
    projectId: proj.id,
    playbookId: proj.playbooks[0].id,
    inventoryId: proj.inventories[0].id,
    check: $('#deploy-check').checked,
    diff: $('#deploy-diff').checked,
    tags: $('#deploy-tags').value ? $('#deploy-tags').value.split(',').map((s) => s.trim()).filter(Boolean) : undefined,
    limit: $('#deploy-limit').value.trim() || undefined,
    verbosity: parseInt($('#deploy-verbosity').value, 10) || 0,
  };
  $('#deploy-run-btn').disabled = true;
  try {
    const d = await api.createDeployment(plan);
    await loadDeployments();
    selectDeployment(d.id);
  } catch (err) {
    const msg = err.problems?.length ? err.problems.join('\n') : err.message;
    alert(`Deployment rejected:\n${msg}`);
  } finally {
    renderRunFormState();
  }
}

async function loadDeployments() {
  const wrap = $('#deploy-list');
  try {
    const { deployments } = await api.deployments();
    wrap.replaceChildren();
    if (!deployments.length) {
      wrap.append(el('div', { class: 'empty-state' }, 'No deployments yet.'));
      return;
    }
    for (const d of deployments) {
      wrap.append(el('div', {
        class: 'deploy-row' + (selectedDeployment === d.id ? ' selected' : ''),
        onclick: () => selectDeployment(d.id),
      },
        el('span', { class: `deploy-status ${d.status}` }, statusIcon(d.status)),
        el('span', { class: 'deploy-id mono' }, d.id),
        el('span', { class: 'deploy-time' }, new Date(d.startedAt).toLocaleTimeString()),
      ));
    }
  } catch (err) {
    wrap.replaceChildren(el('div', { class: 'empty-state' }, `Could not load: ${err.message}`));
  }
}

function statusIcon(status) {
  return { pending: '…', running: '●', succeeded: '✓', failed: '✗', canceled: '◼' }[status] ?? '?';
}

async function selectDeployment(id) {
  selectedDeployment = id;
  if (eventSource) { eventSource.close(); eventSource = null; }
  taskViews = new Map();

  const detail = $('#deploy-detail');
  detail.replaceChildren();

  const header = el('div', { class: 'deploy-detail-head' },
    el('span', { class: 'mono' }, id),
    el('button', {
      class: 'mini-btn danger hidden', id: 'deploy-cancel-btn',
      onclick: () => api.cancelDeployment(id).catch(() => {}),
    }, 'Cancel'),
  );
  const statusLine = el('div', { class: 'deploy-detail-status', id: 'deploy-status-line' }, '');
  const structured = el('div', { id: 'deploy-structured', class: 'scroll' });
  const logLabel = el('div', { class: 'section-label' }, 'Log');
  const log = el('pre', { id: 'deploy-log', class: 'mono scroll' });
  liveLogEl = log;
  detail.append(header, statusLine, structured, logLabel, log);

  loadDeployments();

  // Replay + live via SSE.
  eventSource = new EventSource(`/api/deployments/${encodeURIComponent(id)}/events`);
  eventSource.onmessage = (msg) => {
    const ev = JSON.parse(msg.data);
    handleEvent(ev);
  };
  eventSource.onerror = () => {
    // Server closes the stream when finished; refresh final state.
    eventSource.close();
    eventSource = null;
    loadDeployments();
    renderRunFormState();
  };
}

function handleEvent(ev) {
  const structured = $('#deploy-structured');
  const statusLine = $('#deploy-status-line');
  if (!structured) return;

  switch (ev.type) {
    case 'deployment.started':
      statusLine.textContent = 'running…';
      $('#deploy-cancel-btn')?.classList.remove('hidden');
      break;
    case 'play.started':
      structured.append(el('div', { class: 'play-header' }, `PLAY [${ev.play}]`));
      break;
    case 'task.started':
    case 'handler.started': {
      const key = `${ev.play}|${ev.task}`;
      const row = el('div', { class: 'deploy-task running' },
        el('span', { class: 'task-icon' }, '●'),
        el('span', { class: 'task-name' }, ev.task),
        el('span', { class: 'task-state' }, ev.type === 'handler.started' ? 'handler' : 'running'));
      taskViews.set(key, row);
      structured.append(row);
      break;
    }
    case 'task.ok':
    case 'task.changed':
    case 'task.skipped':
    case 'task.failed':
    case 'host.unreachable': {
      const key = `${ev.play}|${ev.task}`;
      const row = taskViews.get(key);
      const state = { 'task.ok': 'ok', 'task.changed': 'changed', 'task.skipped': 'skipped', 'task.failed': 'failed', 'host.unreachable': 'unreachable' }[ev.type];
      const icon = { ok: '✓', changed: '✓', skipped: '–', failed: '✗', unreachable: '✗' }[state];
      if (row) {
        row.className = `deploy-task ${state}`;
        row.querySelector('.task-icon').textContent = icon;
        row.querySelector('.task-state').textContent = state + (ev.host ? ` · ${ev.host}` : '');
      } else {
        structured.append(el('div', { class: `deploy-task ${state}` },
          el('span', { class: 'task-icon' }, icon),
          el('span', { class: 'task-name' }, ev.task),
          el('span', { class: 'task-state' }, state + (ev.host ? ` · ${ev.host}` : ''))));
      }
      if (ev.message && (state === 'failed' || state === 'unreachable')) {
        structured.append(el('div', { class: 'deploy-task-msg' }, ev.message));
      }
      break;
    }
    case 'deployment.finished': {
      const final = ev.status ? `finished: ${ev.status}` : (ev.message || 'finished');
      statusLine.textContent = final;
      $('#deploy-cancel-btn')?.classList.add('hidden');
      loadDeployments();
      break;
    }
    case 'log.stdout':
    case 'log.stderr':
      if (liveLogEl) {
        liveLogEl.textContent += ev.message + '\n';
        liveLogEl.scrollTop = liveLogEl.scrollHeight;
      }
      break;
  }
}
