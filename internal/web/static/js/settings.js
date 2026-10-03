// Settings + AI dialogs. Settings contain only credential references for
// secrets; AI proposals always land in the editor as reviewable IR.

import { api } from './api.js';
import * as store from './store.js';
import { el } from './form.js';

const $ = (sel) => document.querySelector(sel);

export function wireSettingsAI() {
  $('#btn-settings').addEventListener('click', openSettings);
  $('#settings-close').addEventListener('click', closeSettings);
  $('#settings-form').addEventListener('submit', saveSettings);
  $('#settings-modal').addEventListener('click', (e) => {
    if (e.target.id === 'settings-modal') closeSettings();
  });

  $('#btn-ai').addEventListener('click', openAI);
  $('#ai-close').addEventListener('click', closeAI);
  $('#ai-modal').addEventListener('click', (e) => {
    if (e.target.id === 'ai-modal') closeAI();
  });
  $('#ai-generate').addEventListener('click', generate);
  $('#ai-apply').addEventListener('click', applyProposal);
  $('#ai-discard').addEventListener('click', discardProposal);
}

// ---------- Settings ----------

async function openSettings() {
  $('#settings-modal').classList.remove('hidden');
  const [settings, creds] = await Promise.all([
    api.settings().catch(() => ({})),
    api.credentials().then((r) => r.credentials).catch(() => []),
  ]);
  $('#set-ansible-path').value = settings.ansiblePath ?? '';
  $('#set-ansible-doc-path').value = settings.ansibleDocPath ?? '';
  $('#set-ansible-playbook-path').value = settings.ansiblePlaybookPath ?? '';
  $('#set-ai-endpoint').value = settings.ai?.endpoint ?? '';
  $('#set-ai-model').value = settings.ai?.model ?? '';
  $('#set-s3-endpoint').value = settings.s3?.endpoint ?? '';
  $('#set-s3-bucket').value = settings.s3?.bucket ?? '';
  $('#set-s3-prefix').value = settings.s3?.prefix ?? '';

  const keySelect = $('#set-ai-key');
  keySelect.replaceChildren(el('option', { value: '' }, '(none — local servers often need none)'));
  for (const c of creds.filter((c) => c.kind === 'api_key' || c.kind === 'ssh_password')) {
    const o = el('option', { value: c.id }, c.name);
    if (settings.ai?.apiKeyCredId === c.id) o.selected = true;
    keySelect.append(o);
  }
}

function closeSettings() {
  $('#settings-modal').classList.add('hidden');
}

async function saveSettings(e) {
  e.preventDefault();
  const current = await api.settings().catch(() => ({}));
  const next = {
    ...current,
    ansiblePath: $('#set-ansible-path').value.trim() || undefined,
    ansibleDocPath: $('#set-ansible-doc-path').value.trim() || undefined,
    ansiblePlaybookPath: $('#set-ansible-playbook-path').value.trim() || undefined,
    ai: {
      ...(current.ai ?? {}),
      endpoint: $('#set-ai-endpoint').value.trim() || undefined,
      model: $('#set-ai-model').value.trim() || undefined,
      apiKeyCredId: $('#set-ai-key').value || undefined,
    },
    s3: {
      ...(current.s3 ?? {}),
      endpoint: $('#set-s3-endpoint').value.trim() || undefined,
      bucket: $('#set-s3-bucket').value.trim() || undefined,
      prefix: $('#set-s3-prefix').value.trim() || undefined,
    },
  };
  try {
    await api.saveSettings(next);
    closeSettings();
    // Ansible availability may have changed via path overrides.
    const health = await api.health();
    store.server.health = health;
    location.reload(); // module catalog and run-form state depend on it
  } catch (err) {
    alert(`Save settings failed: ${err.message}`);
  }
}

// ---------- AI assistant ----------

let proposal = null;

function openAI() {
  $('#ai-modal').classList.remove('hidden');
  $('#ai-intent').focus();
}

function closeAI() {
  $('#ai-modal').classList.add('hidden');
  discardProposal();
}

async function generate() {
  const intent = $('#ai-intent').value.trim();
  if (!intent) return;
  const status = $('#ai-status');
  const resultEl = $('#ai-result');
  status.textContent = 'Generating…';
  $('#ai-generate').disabled = true;
  resultEl.replaceChildren();
  $('#ai-actions').classList.add('hidden');

  let currentYaml = '';
  const play = store.currentPlay();
  if (play && (play.tasks?.length || play.handlers?.length)) {
    try {
      currentYaml = (await api.render(store.currentPlaybook())).yaml;
    } catch { /* proceed without grounding */ }
  }

  try {
    const res = await api.aiGenerate(intent, currentYaml);
    proposal = res.playbook;
    status.textContent = 'Proposal ready — review before applying.';
    const diags = res.diagnostics ?? [];
    if (diags.length) {
      const dEl = el('div', {});
      for (const d of diags) {
        dEl.append(el('div', { class: `diag ${d.severity}` },
          el('span', { class: 'path' }, d.path), d.message));
      }
      resultEl.append(dEl);
    }
    const playCount = res.playbook?.plays?.length ?? 0;
    const taskCount = (res.playbook?.plays ?? []).reduce((n, p) => n + (p.tasks?.length ?? 0), 0);
    resultEl.append(el('div', { class: 'hint-line' },
      `Proposed playbook: ${playCount} play(s), ${taskCount} task(s). Applying replaces the current playbook (undoable).`));
    $('#ai-actions').classList.remove('hidden');
  } catch (err) {
    proposal = null;
    status.textContent = err.code === 'ai_not_configured'
      ? 'AI is not configured. Open Settings to set an endpoint and model.'
      : `Generation failed: ${err.message}`;
  } finally {
    $('#ai-generate').disabled = false;
  }
}

function applyProposal() {
  if (!proposal) return;
  store.commit('ai apply', (proj) => {
    proj.playbooks[0] = proposal;
  });
  store.editor.selected = null;
  store.ui.yamlDirty = true;
  closeAI();
  document.querySelector('#tabs .tab[data-tab="yaml"]').click();
}

function discardProposal() {
  proposal = null;
  $('#ai-result')?.replaceChildren();
  $('#ai-actions')?.classList.add('hidden');
  const s = $('#ai-status');
  if (s) s.textContent = '';
}
