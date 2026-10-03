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
  $('#inv-add-ungrouped').addEventListener('click', () => openMachineModal(null));
  $('#machine-close').addEventListener('click', closeMachineModal);
  $('#machine-add').addEventListener('click', addMachineFromModal);
  $('#machine-test').addEventListener('click', testMachineFromModal);
  $('#machine-modal').addEventListener('click', (e) => {
    if (e.target.id === 'machine-modal') closeMachineModal();
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
  // Order matters: machine rows resolve their credential reference against
  // the loaded credential list. Rendering first would show a saved
  // reference as "(no credential)" — and let an unrelated edit wipe it.
  await renderCredentials();
  renderGroups();
}

// credentialsLoaded reports whether the credential list has been fetched.
function credentialsLoaded() {
  return Array.isArray(store.server.credentials);
}

// credentialOptions builds a dropdown that always contains the currently
// referenced credential, even if the list has not loaded yet or the
// credential was removed elsewhere. A stored reference can therefore never
// silently render as "no credential".
function credentialOptions(selectedID) {
  const creds = store.server.credentials ?? [];
  const options = [{ id: '', label: '(no credential)' }];
  for (const c of creds) options.push({ id: c.id, label: `${c.name} (${c.kind})` });
  if (selectedID && !options.some((o) => o.id === selectedID)) {
    options.push({ id: selectedID, label: `(unknown credential ${selectedID})` });
  }
  return options;
}

function buildCredentialSelect(selectedID, onChange) {
  const sel = el('select', { title: 'SSH credential (stored encrypted, never exported)' });
  for (const o of credentialOptions(selectedID)) {
    const opt = el('option', { value: o.id }, o.label);
    if (o.id === (selectedID ?? '')) opt.selected = true;
    sel.append(opt);
  }
  sel.addEventListener('change', () => onChange(sel.value));
  return sel;
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
      onclick: () => openMachineModal(g),
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

  const groupVarsToggle = el('button', { class: 'mini-btn', type: 'button' }, '▸ Advanced: group variables');
  const varsWrap = el('div', { class: 'inv-vars hidden' });
  g.vars = g.vars ?? {};
  varsWrap.append(kvEditor(g.vars, (v) => {
    store.commit('inv', () => { g.vars = v ?? {}; });
  }));
  groupVarsToggle.addEventListener('click', () => {
    const open = varsWrap.classList.toggle('hidden');
    groupVarsToggle.textContent = (open ? '▸' : '▾') + ' Advanced: group variables';
  });
  card.append(groupVarsToggle, varsWrap);

  for (const h of g.hosts ?? []) {
    card.append(hostCard(inv, g, h));
  }
  return card;
}

function hostCard(inv, group, h) {
  const row = el('div', { class: 'inv-host' });

  const mk = (placeholder, value, apply, opts = {}) => {
    const input = el('input', { class: 'mono', type: opts.type ?? 'text', placeholder, value: value ?? '', title: opts.title ?? placeholder });
    input.addEventListener('change', () => {
      store.commit('inv', () => apply(input));
      renderGroups();
    });
    return input;
  };

  const credSelect = buildCredentialSelect(h.credentialId, (value) => {
    store.commit('inv', () => { h.credentialId = value || undefined; });
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

  // Ansible-specific variables stay behind an advanced expander.
  const varsToggle = el('button', { class: 'mini-btn', type: 'button' }, '▸ Advanced: Ansible variables');
  const varsWrap = el('div', { class: 'inv-host-vars hidden' });
  h.vars = h.vars ?? {};
  varsWrap.append(kvEditor(h.vars, (v) => {
    store.commit('inv', () => { h.vars = v ?? {}; });
  }));
  varsToggle.addEventListener('click', () => {
    const open = varsWrap.classList.toggle('hidden');
    varsToggle.textContent = (open ? '▸' : '▾') + ' Advanced: Ansible variables';
  });
  row.append(el('div', { class: 'inv-host-vars' }, varsToggle), varsWrap);
  return row;
}

async function testConnection(h, resultEl) {
  resultEl.className = 'host-test-result';
  resultEl.textContent = 'Testing connection…';
  try {
    const res = await api.testTarget(h);
    resultEl.className = 'host-test-result ' + (res.ok ? 'ok' : 'err');
    if (res.ok) {
      resultEl.textContent = '✓ Connection works';
      return;
    }
    // Explain the failure in plain language and keep the raw details one
    // hover away (advanced users can also read the deployment log).
    const addr = h.address || h.name;
    const target = h.sshPort ? `${addr}:${h.sshPort}` : addr;
    resultEl.textContent = `✗ ${res.message} (${target})`;
    resultEl.title = res.details || '';
  } catch (e) {
    resultEl.className = 'host-test-result err';
    resultEl.textContent = `✗ ${e.message}`;
  }
}

// ---------- Add machine (friendly form) ----------

let machineTargetGroup = null;

function openMachineModal(group) {
  machineTargetGroup = group;
  $('#machine-modal').classList.remove('hidden');
  $('#machine-result').textContent = '';
  $('#machine-result').className = 'host-test-result';
  $('#machine-name').value = '';
  $('#machine-address').value = '';
  $('#machine-user').value = 'root';
  $('#machine-port').value = '22';

  // Ensure the credential list is present before offering the choice.
  if (!credentialsLoaded()) {
    api.credentials().then(({ credentials }) => {
      store.server.credentials = credentials;
      const sel = $('#machine-cred');
      sel.replaceChildren(el('option', { value: '' }, 'Default SSH keys / agent'));
      for (const c of credentials) sel.append(el('option', { value: c.id }, `${c.name} (${c.kind})`));
    }).catch(() => {});
  }
  const credSel = $('#machine-cred');
  credSel.replaceChildren(el('option', { value: '' }, 'Default SSH keys / agent'));
  for (const c of store.server.credentials ?? []) {
    credSel.append(el('option', { value: c.id }, `${c.name} (${c.kind})`));
  }

  const groupSel = $('#machine-group');
  const inv = ensureInventory(store.editor.project);
  groupSel.replaceChildren(el('option', { value: '' }, 'Ungrouped'));
  for (const g of inv.groups) {
    const o = el('option', { value: g.id }, g.name);
    if (group && g.id === group.id) o.selected = true;
    groupSel.append(o);
  }
  groupSel.disabled = !!group; // invoked from a group header
}

function closeMachineModal() {
  $('#machine-modal').classList.add('hidden');
}

function machineFromModal() {
  const name = $('#machine-name').value.trim() || $('#machine-address').value.trim();
  const host = newHost(name || 'machine');
  host.name = name || 'machine';
  host.address = $('#machine-address').value.trim() || undefined;
  host.sshUser = $('#machine-user').value.trim() || undefined;
  host.sshPort = parseInt($('#machine-port').value, 10) || undefined;
  host.credentialId = $('#machine-cred').value || undefined;
  return host;
}

async function testMachineFromModal() {
  const host = machineFromModal();
  const result = $('#machine-result');
  result.className = 'host-test-result';
  result.textContent = 'Testing connection…';
  try {
    const res = await api.testTarget(host);
    result.className = 'host-test-result ' + (res.ok ? 'ok' : 'err');
    result.textContent = (res.ok ? '✓ ' : '✗ ') + res.message;
    if (!res.ok && res.details) {
      result.title = res.details;
    }
  } catch (e) {
    result.className = 'host-test-result err';
    result.textContent = `✗ ${e.message}`;
  }
}

function addMachineFromModal() {
  const host = machineFromModal();
  if (!host.name) return;
  store.commit('inv', (proj) => {
    const inv = ensureInventory(proj);
    const groupID = $('#machine-group').value;
    if (groupID) {
      const g = inv.groups.find((x) => x.id === groupID);
      if (g) {
        g.hosts = g.hosts ?? [];
        g.hosts.push(host);
        return;
      }
    }
    inv.hosts.push(host);
  });
  closeMachineModal();
  renderGroups();
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
