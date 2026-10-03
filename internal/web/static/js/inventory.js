// Inventory tab: groups/hosts editor plus the credential store UI.
// Inventory edits go through store.commit like all editor mutations;
// credentials are server-side state (write-only secrets), fetched via API.

import { api } from './api.js';
import * as store from './store.js';
import { el, kvEditor } from './form.js';

const $ = (sel) => document.querySelector(sel);

export function wireInventoryTab() {
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
    renderInventory();
  });
  $('#inv-add-ungrouped').addEventListener('click', () => {
    store.commit('inv', (proj) => {
      ensureInventory(proj).hosts.push(newHost(`host${ensureInventory(proj).hosts.length + 1}`));
    });
    renderInventory();
  });
  $('#cred-add-form').addEventListener('submit', addCredential);
  store.on('editor', ({ label } = {}) => {
    // Only re-render for inventory-affecting changes to avoid clobbering
    // open text inputs on unrelated edits.
    if (['inv', 'load', 'yaml apply', 'undo', 'redo'].includes(label)) renderInventory();
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

export async function renderInventory() {
  renderGroups();
  await renderCredentials();
}

function renderGroups() {
  const proj = store.editor.project;
  const wrap = $('#inv-groups');
  wrap.replaceChildren();
  if (!proj) return;
  const inv = ensureInventory(proj);

  if (!inv.groups.length && !inv.hosts.length) {
    wrap.append(el('div', { class: 'empty-state' },
      'No inventory yet. Add a group or an ungrouped host.'));
    return;
  }

  for (const g of inv.groups) {
    wrap.append(groupCard(inv, g));
  }
  if (inv.hosts.length) {
    const section = el('div', { class: 'inv-group' },
      el('div', { class: 'inv-group-head' },
        el('span', { class: 'inv-group-name' }, 'ungrouped')));
    for (const h of inv.hosts) {
      section.append(hostCard(inv, null, h));
    }
    wrap.append(section);
  }
}

function groupCard(inv, g) {
  const card = el('div', { class: 'inv-group' });

  const nameInput = el('input', { class: 'inline-edit mono', value: g.name });
  nameInput.addEventListener('change', () => {
    store.commit('inv', () => { g.name = nameInput.value.trim() || g.name; });
    renderGroups();
  });

  card.append(el('div', { class: 'inv-group-head' },
    nameInput,
    el('span', { class: 'inv-count' }, `${(g.hosts ?? []).length} host(s)`),
    el('button', {
      class: 'mini-btn',
      onclick: () => {
        store.commit('inv', () => { g.hosts = g.hosts ?? []; g.hosts.push(newHost(`host${g.hosts.length + 1}`)); });
        renderInventory();
      },
    }, '+ host'),
    el('button', {
      class: 'mini-btn danger',
      onclick: () => {
        if (!confirm(`Delete group “${g.name}” and its hosts?`)) return;
        store.commit('inv', (proj) => {
          const gs = ensureInventory(proj).groups;
          const i = gs.findIndex((x) => x.id === g.id);
          if (i >= 0) gs.splice(i, 1);
        });
        renderInventory();
      },
    }, '✕'),
  ));

  // Group variables.
  const varsWrap = el('div', { class: 'inv-vars' });
  g.vars = g.vars ?? {};
  varsWrap.append(el('div', { class: 'fdesc' }, 'group vars'),
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
    const input = el('input', { class: 'mono', type: opts.type ?? 'text', placeholder, value: value ?? '' });
    input.addEventListener('change', () => {
      store.commit('inv', () => apply(input));
      renderGroups();
    });
    return input;
  };

  const credSelect = el('select', { title: 'SSH credential (stored in the credential store, never exported)' });
  credSelect.append(el('option', { value: '' }, '(no credential)'));
  for (const c of creds) {
    const o = el('option', { value: c.id }, `${c.name} (${c.kind})`);
    if (h.credentialId === c.id) o.selected = true;
    credSelect.append(o);
  }
  credSelect.addEventListener('change', () => {
    store.commit('inv', () => { h.credentialId = credSelect.value || undefined; });
  });

  row.append(
    mk('name', h.name, (i) => { h.name = i.value.trim() || h.name; }),
    mk('address (ansible_host)', h.address, (i) => { h.address = i.value.trim() || undefined; }),
    mk('ssh user', h.sshUser, (i) => { h.sshUser = i.value.trim() || undefined; }),
    mk('port', h.sshPort || '', (i) => { h.sshPort = parseInt(i.value, 10) || undefined; }, { type: 'number' }),
    credSelect,
    el('button', {
      class: 'mini-btn danger', title: 'Delete host',
      onclick: () => {
        store.commit('inv', () => {
          const list = group ? group.hosts : inv.hosts;
          const i = list.findIndex((x) => x.id === h.id);
          if (i >= 0) list.splice(i, 1);
        });
        renderInventory();
      },
    }, '✕'),
  );

  // Host variables.
  const varsWrap = el('div', { class: 'inv-host-vars' });
  h.vars = h.vars ?? {};
  varsWrap.append(kvEditor(h.vars, (v) => {
    store.commit('inv', () => { h.vars = v ?? {}; });
  }));
  row.append(varsWrap);
  return row;
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
        'No credentials stored. Secrets are write-only and encrypted at rest.'));
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
    renderGroups(); // new credential becomes selectable on hosts
  } catch (err) {
    alert(`Could not store credential: ${err.message}`);
  }
}
