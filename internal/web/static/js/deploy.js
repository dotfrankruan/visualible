// Deploy stage: deliberate preflight, friendly progress language, and
// raw Ansible output behind an expander. Driven by structured SSE events
// — never by parsing terminal text in the browser.

import { api } from './api.js';
import * as store from './store.js';
import { el } from './form.js';

const $ = (sel) => document.querySelector(sel);

let selectedDeployment = null;
let eventSource = null;
let hostBlocks = new Map(); // host -> { list: el, tasks: Map }
let liveLogEl = null;

export function wireDeploy() {
  $('#deploy-run-form').addEventListener('submit', startDeployment);
  $('#deploy-refresh').addEventListener('click', loadDeployments);
  $('#deploy-advanced-toggle').addEventListener('click', (e) => {
    const open = $('#deploy-advanced').classList.toggle('hidden');
    e.currentTarget.classList.toggle('open', !open);
  });
}

export async function renderDeploy() {
  await loadDeployments();
  renderRunFormState();
  renderPreflight();
}

// ---------- Preflight (honest, no invented numbers) ----------

async function renderPreflight() {
  const wrap = $('#deploy-preflight');
  wrap.replaceChildren();
  const proj = store.editor.project;
  const inv = proj?.inventories?.[0];
  const hosts = inv ? countHosts(inv) : 0;
  const tasks = store.currentPlay()?.tasks ?? [];

  wrap.append(
    preflightRow(hosts > 0, 'Targets', hosts ? `${hosts} machine(s)` : 'none yet — add machines on the Targets page'),
    preflightRow(tasks.length > 0, 'Automation', tasks.length ? `${tasks.length} step(s)` : 'nothing to do yet — add steps on the Build page'),
    preflightRow(store.server.health?.ansible?.available, 'Ansible',
      store.server.health?.ansible?.available ? 'ready' : 'not detected'),
  );
}

function preflightRow(ok, label, detail) {
  return el('div', { class: `preflight-row ${ok ? 'ok' : 'err'}` },
    el('span', { class: 'icon' }, ok ? '✓' : '✗'),
    el('span', {}, label),
    el('span', { class: 'detail' }, detail));
}

function renderRunFormState() {
  const btn = $('#deploy-run-btn');
  const note = $('#deploy-note');
  const ansibleUp = store.server.health?.ansible?.available;
  const inv = store.editor.project?.inventories?.[0];
  const hosts = inv ? countHosts(inv) : 0;
  const tasks = store.currentPlay()?.tasks ?? [];
  if (!ansibleUp) {
    btn.disabled = true;
    note.textContent = 'Ansible is not detected; deployments are unavailable.';
  } else if (!hosts) {
    btn.disabled = true;
    note.textContent = 'Add at least one machine on the Targets page first.';
  } else if (!tasks.length) {
    btn.disabled = true;
    note.textContent = 'Add automation steps on the Build page first.';
  } else if (store.editor.dirty) {
    btn.disabled = true;
    note.textContent = 'Save the project before deploying (Ctrl/Cmd+S).';
  } else {
    btn.disabled = false;
    note.textContent = '';
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
    alert(`Deployment could not start:\n${msg}`);
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

// ---------- Live structured view ----------

async function selectDeployment(id) {
  selectedDeployment = id;
  if (eventSource) { eventSource.close(); eventSource = null; }
  hostBlocks = new Map();

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
  const structured = el('div', { id: 'deploy-structured' });
  const logsToggle = el('button', { class: 'mini-btn', id: 'deploy-log-toggle' }, '▸ Raw Ansible output');
  const log = el('pre', { id: 'deploy-log', class: 'mono hidden' });
  logsToggle.addEventListener('click', () => {
    const open = log.classList.toggle('hidden');
    logsToggle.textContent = (open ? '▸' : '▾') + ' Raw Ansible output';
  });
  liveLogEl = log;
  detail.append(header, statusLine, structured, logsToggle, log);

  loadDeployments();

  eventSource = new EventSource(`/api/deployments/${encodeURIComponent(id)}/events`);
  eventSource.onmessage = (msg) => handleEvent(JSON.parse(msg.data));
  eventSource.onerror = () => {
    eventSource.close();
    eventSource = null;
    loadDeployments();
    renderRunFormState();
  };
}

// friendlyStatus translates Ansible jargon into outcome language.
// Advanced users still see the raw status in parentheses via title attr.
function friendlyStatus(type, changed) {
  switch (type) {
    case 'task.ok': return 'Already correct';
    case 'task.changed': return 'Updated';
    case 'task.skipped': return 'Skipped';
    case 'task.failed': return 'Failed';
    case 'host.unreachable': return 'Unreachable';
    default: return changed ? 'Updated' : '';
  }
}

function hostBlock(host) {
  if (hostBlocks.has(host)) return hostBlocks.get(host);
  const structured = $('#deploy-structured');
  const tasks = new Map();
  const list = el('div', {});
  const block = el('div', { class: 'deploy-host-block' },
    el('div', { class: 'deploy-host-name' }, host || '(local)'),
    list);
  structured.append(block);
  const entry = { list, tasks };
  hostBlocks.set(host, entry);
  return entry;
}

function handleEvent(ev) {
  const structured = $('#deploy-structured');
  const statusLine = $('#deploy-status-line');
  if (!structured) return;

  switch (ev.type) {
    case 'deployment.started':
      statusLine.textContent = 'Deploying…';
      $('#deploy-cancel-btn')?.classList.remove('hidden');
      break;
    case 'play.started':
      structured.append(el('div', { class: 'play-header' }, `Starting: ${ev.play}`));
      break;
    case 'task.started':
    case 'handler.started': {
      const hb = hostBlock(ev.host || '…');
      const key = `${ev.play}|${ev.task}`;
      const row = el('div', { class: 'deploy-task running' },
        el('span', { class: 'task-icon' }, '●'),
        el('span', { class: 'task-name' }, ev.task),
        el('span', { class: 'task-state' }, 'Running'));
      hb.tasks.set(key, row);
      hb.list.append(row);
      break;
    }
    case 'task.ok':
    case 'task.changed':
    case 'task.skipped':
    case 'task.failed':
    case 'host.unreachable': {
      const state = { 'task.ok': 'ok', 'task.changed': 'changed', 'task.skipped': 'skipped', 'task.failed': 'failed', 'host.unreachable': 'unreachable' }[ev.type];
      const icon = { ok: '✓', changed: '✓', skipped: '–', failed: '✗', unreachable: '✗' }[state];
      const hb = hostBlock(ev.host || '…');
      const key = `${ev.play}|${ev.task}`;
      const friendly = friendlyStatus(ev.type, ev.changed);
      const row = hb.tasks.get(key);
      if (row) {
        row.className = `deploy-task ${state}`;
        row.querySelector('.task-icon').textContent = icon;
        row.querySelector('.task-state').textContent = friendly;
        row.querySelector('.task-state').title = ev.type;
      } else {
        hb.tasks.set(key, el('div', { class: `deploy-task ${state}` },
          el('span', { class: 'task-icon' }, icon),
          el('span', { class: 'task-name' }, ev.task),
          el('span', { class: 'task-state', title: ev.type }, friendly)));
        hb.list.append(hb.tasks.get(key));
      }
      if (ev.message && (state === 'failed' || state === 'unreachable')) {
        hb.list.append(el('div', { class: 'deploy-task-msg' },
          humanizeFailure(ev) + '\n\n' + ev.message));
      }
      break;
    }
    case 'deployment.finished': {
      const map = { succeeded: '✓ Finished — everything is done', failed: '✗ Deployment had failures', canceled: '◼ Canceled' };
      statusLine.textContent = map[ev.status] ?? (ev.message || 'Finished');
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

function humanizeFailure(ev) {
  if (ev.type === 'host.unreachable') {
    return `Visualible could not connect to ${ev.host} over SSH. Check the address, SSH user and credential on the Targets page.`;
  }
  return `“${ev.task}” failed on ${ev.host}.`;
}
