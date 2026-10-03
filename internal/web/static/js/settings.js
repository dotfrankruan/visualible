// Settings dialog. Settings contain only credential references for
// secrets; the AI editing workflow itself lives in ai.js.

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
}

async function openSettings() {
  $('#settings-modal').classList.remove('hidden');
  const [settings, creds] = await Promise.all([
    api.settings().catch(() => ({})),
    api.credentials().then((r) => r.credentials).catch(() => []),
  ]);
  // Make the active data location visible: an unexpectedly fresh database
  // is the common cause of "my credentials disappeared".
  try {
    const health = await api.health();
    $('#set-data-dir').value = health.dataDir ?? '(unknown)';
    $('#set-db-path').value = health.dbPath ?? '(unknown)';
  } catch {
    $('#set-data-dir').value = '(unavailable)';
    $('#set-db-path').value = '(unavailable)';
  }
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
    const saved = await api.saveSettings(next);
    store.server.settings = saved;
    closeSettings();
    // Ansible availability may have changed via path overrides.
    store.server.health = await api.health().catch(() => store.server.health);
    location.reload(); // module catalog and run-form state depend on it
  } catch (err) {
    alert(`Save settings failed: ${err.message}`);
  }
}
