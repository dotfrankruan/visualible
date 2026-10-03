// Targets stage: the machines automation runs on (Ansible inventory
// underneath, friendly language on top) plus the credential store.

import { api } from './api.js';
import * as store from './store.js';
import { el, kvEditor } from './form.js';
import { renderYaml } from './yaml.js';

const $ = (sel) => document.querySelector(sel);

export function wireTargets() {
  $('#inv-add-group').addEventListener('click', () => {
    store.commit('inv', (proj) => {
      ensureInventory(proj).groups.push({
        id: store.newId('grp'),
        name: `group${ensureInventory(proj).groups.length + 1}`,
        hosts: [],
        children: [],
        vars: {},
      });
    });
    renderGroups();
  });
  $('#inv-add-ungrouped').addEventListener('click', () => {
    store.commit('inv', (proj) => {
      ensureInventory(proj).hosts.push(newHost(`machine${ensureInventory(proj).hosts.length + 1}`));
    });
    renderGroups();
  });
  $('#cred-add-form').addEventListener('submit', addCredential);

  $('#targets-advanced-toggle').addEventListener('click', async (e) => {
    const btn = e.currentTarget;
    const pre = $('#targets-inventory-yaml');
    const open = pre.classList.toggle('hidden');
    btn.classList.toggle('open', !open);
    if (!open) await renderInventoryYaml();
  });

  store.on('editor', ({ label } = {}) => {
    if (['inv', 'load', 'yaml apply', 'ai apply', 'undo', 'redo'].includes(label)) renderGroups();
  });
}

function ensureInventory(proj) {
  if (!proj.inventories) proj.inventories = [];
  if (!proj.inventories.length) {
    proj.inventories.push({
      id: store.newId('inv'), name: 'inventory', groups: [], hosts: [],
    });
  }
  const inv = proj.inventories[0];
  inv.groups = inv.groups ?? [];
  inv.hosts = inv.hosts ?? [];
  return inv;
}

function newHost(name) {
  return { id: store.newId('host'), name, address: '', sshUser: '', sshPort: 0, credentialId: '', vars: {} };
}

export async function renderTargets() {
  renderGroups();
  await renderCredentials();
}

async function renderInventoryYaml() {
  const pre = $('#targets-inventory-yaml');
  pre.replaceChildren();
  const inv = store.editor.project?.inventories?.[0];
  if (!inv) {
    pre.append(el('div', { class: 'empty-state' }, 'No inventory yet.'));
    return;
  }
  try {
    const { yaml } = await api.renderInventory(inv);
    renderYaml(pre, yaml);
  } catch (e) {
    const msg = e.problems?.length ? e.problems.join('\n') : e.message;
    pre.append(el('div', { class: 'yaml-error' }, `Cannot render: ${msg}`));
  }
}

function renderGroups() {
  const proj = store.editor.project;
  const wrap = $('#inv-groups');
  wrap.replaceChildren();
  if (!proj) return;
  const inv = ensureInventory(proj);

  if (!inv.groups.length && !inv.hosts.length) {
    wrap.append(el('div', { class: 'empty-state' },
      'No machines yet. Add a group (e.g. "web servers") or a single machine.'));
    return;
  }

  for (const g of inv.groups) {
    wrap.append(groupCard(inv, g));
  }
  if (inv.hosts.length) {
    const section = el('div', { class: 'inv-group' },
      el('div', { class: 'inv-group-head' },
        el('span', { class: 'inv-group-name' }, 'Ungrouped machines')));
    for (const h of inv.hosts) {
      section.append(hostCard(inv, null, h));
    }
    wrap.append(section);
  }
}

function groupCard(inv, g) {
  const card = el('div', { class: 'inv-group' });

  const nameInput = el('input', { class: 'inline-edit mono', value: g.name, title: 'Group name' });
  nameInput.addEventListener('change', () => {
    store.commit('inv', () => { g.name = nameInput.value.trim() || g.name; });
    renderGroups();
  });

  card.append(el('div', { class: 'inv-group-head' },
    nameInput,
    el('span', { class: 'inv-count' }, `${(g.hosts ?? []).length} machine(s)`),
    el('button', {
      class: 'mini-btn',
      onclick: () => {
        store.commit('inv', () => { g.hosts = g.hosts ?? []; g.hosts.push(newHost(`machine${g.hosts.length + 1}`)); });
        renderGroups();
      },
    }, '+ machine'),
    el('button', {
      class: 'mini-btn danger',
      onclick: () => {
        if (!confirm(`Delete group “${g.name}” and its machines?`)) return;
        store.commit('inv', (proj) => {
          const gs = ensureInventory(proj).groups;
          const i = gs.findIndex((x) => x.id === g.id);
          if (i >= 0) gs.splice(i, 1);
        });
        renderGroups();
      },
    }, '✕'),
  ));

  const varsWrap = el('div', { class: 'inv-vars' });
  g.vars = g.vars ?? {};
  varsWrap.append(el('div', { class: 'fdesc' }, 'group variables (advanced)'),
    kvEditor(g.vars, (v) => {
      store.commit('inv', () => { g.vars = v ?? {}; });
    }));
  card.append(varsWrap);

  for (const h of g.hosts ?? []) {
    card.append(hostCard(inv, g, h));
  }
  return card;
}

function hostCard(inv, group, h) {
  const creds = store.server.credentials ?? [];
  const row = el('div', { class: 'inv-host' });

  const mk = (placeholder, value, apply, opts = {}) => {
    const input = el('input', { class: 'mono', type: opts.type ?? 'text', placeholder, value: value ?? '', title: opts.title ?? placeholder });
    input.addEventListener('change', () => {
      store.commit('inv', () => apply(input));
      renderGroups();
    });
    return input;
  };

  const credSelect = el('select', { title: 'SSH credential (stored encrypted, never exported)' });
  credSelect.append(el('option', { value: '' }, '(no credential)'));
  for (const c of creds) {
    const o = el('option', { value: c.id }, `${c.name} (${c.kind})`);
    if (h.credentialId === c.id) o.selected = true;
    credSelect.append(o);
  }
  credSelect.addEventListener('change', () => {
    store.commit('inv', () => { h.credentialId = credSelect.value || undefined; });
  });

  const testBtn = el('button', {
    class: 'mini-btn', title: 'Test SSH connection',
    onclick: () => testConnection(h, resultEl),
  }, 'Test');
  const resultEl = el('div', { class: 'host-test-result' });

  row.append(
    mk('name', h.name, (i) => { h.name = i.value.trim() || h.name; }, { title: 'Machine name' }),
    mk('address (IP or hostname)', h.address, (i) => { h.address = i.value.trim() || undefined; }, { title: 'Address (IP or hostname)' }),
    mk('SSH user', h.sshUser, (i) => { h.sshUser = i.value.trim() || undefined; }, { title: 'SSH user' }),
    mk('port', h.sshPort || '', (i) => { h.sshPort = parseInt(i.value, 10) || undefined; }, { type: 'number', title: 'SSH port' }),
    credSelect,
    testBtn,
    el('button', {
      class: 'mini-btn danger', title: 'Delete machine',
      onclick: () => {
        store.commit('inv', () => {
          const list = group ? group.hosts : inv.hosts;
          const i = list.findIndex((x) => x.id === h.id);
          if (i >= 0) list.splice(i, 1);
        });
        renderGroups();
      },
    }, '✕'),
  );
  row.append(resultEl);

  const varsWrap = el('div', { class: 'inv-host-vars' });
  h.vars = h.vars ?? {};
  varsWrap.append(kvEditor(h.vars, (v) => {
    store.commit('inv', () => { h.vars = v ?? {}; });
  }));
  row.append(varsWrap);
  return row;
}

async function testConnection(h, resultEl) {
  resultEl.className = 'host-test-result';
  resultEl.textContent = 'Testing connection…';
  try {
    const res = await api.testTarget(h);
    if (res.ok) {
      resultEl.classList.add('ok');
      resultEl.textContent = '✓ Connection works';
    } else {
      resultEl.classList.add('err');
      resultEl.textContent = `✗ ${res.message || 'Could not connect'}`;
    }
  } catch (e) {
    resultEl.classList.add('err');
    resultEl.textContent = `✗ ${e.message}`;
  }
}

// ---------- Credentials ----------

async function renderCredentials() {
  const wrap = $('#cred-list');
  wrap.replaceChildren(el('div', { class: 'empty-state' }, 'Loading…'));
  try {
    const { credentials } = await api.credentials();
    store.server.credentials = credentials;
    wrap.replaceChildren();
    if (!credentials.length) {
      wrap.append(el('div', { class: 'empty-state' },
        'No credentials stored. SSH keys and passwords are write-only and encrypted at rest.'));
    }
    for (const c of credentials) {
      wrap.append(el('div', { class: 'cred-row' },
        el('span', { class: 'cred-name' }, c.name),
        el('span', { class: 'badge type' }, c.kind),
        el('button', {
          class: 'mini-btn danger',
          onclick: async () => {
            if (!confirm(`Delete credential “${c.name}”?`)) return;
            await api.deleteCredential(c.id);
            renderCredentials();
          },
        }, '✕')));
    }
  } catch (e) {
    wrap.replaceChildren(el('div', { class: 'empty-state' }, `Could not load credentials: ${e.message}`));
  }
}

async function addCredential(e) {
  e.preventDefault();
  const name = $('#cred-name').value.trim();
  const kind = $('#cred-kind').value;
  const secret = $('#cred-secret').value;
  if (!name || !secret) return;
  try {
    await api.createCredential(name, kind, secret);
    $('#cred-name').value = '';
    $('#cred-secret').value = '';
    await renderCredentials();
    renderGroups();
  } catch (err) {
    alert(`Could not store credential: ${err.message}`);
  }
}
