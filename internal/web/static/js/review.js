// Review stage: human inspection of intent before deployment. YAML is a
// secondary, expandable view — never the only review mechanism.

import { api } from './api.js';
import * as store from './store.js';
import { el } from './form.js';
import { humanizeProblems } from './errors.js';
import { renderYaml } from './yaml.js';
import { goStage } from './app.js';

const $ = (sel) => document.querySelector(sel);

export function wireReview() {
  $('#review-yaml-toggle').addEventListener('click', (e) => {
    const btn = e.currentTarget;
    const wrap = $('#review-yaml-wrap');
    const open = wrap.classList.toggle('hidden');
    btn.classList.toggle('open', !open);
    if (!open) renderYamlView();
  });
  $('#review-deploy-btn').addEventListener('click', () => goStage('deploy'));

  $('#btn-render-yaml').addEventListener('click', renderYamlView);
  $('#btn-edit-yaml').addEventListener('click', enterYamlEdit);
  $('#btn-apply-yaml').addEventListener('click', applyYamlEdit);
  $('#btn-cancel-yaml').addEventListener('click', exitYamlEdit);
  $('#btn-download-yaml').addEventListener('click', downloadYaml);
}

export async function renderReview() {
  await refreshRecognitions();
  renderSummary();
}

// Review shows the same curated labels as the canvas: recognitions come
// from the backend so Simple and Advanced views never disagree.
async function refreshRecognitions() {
  const play = store.currentPlay();
  if (!play) return;
  const all = [...(play.tasks ?? []), ...(play.handlers ?? [])];
  if (!all.length) return;
  try {
    const { recognitions } = await api.actionsRecognize(all);
    const map = new Map();
    for (const r of recognitions ?? []) map.set(r.taskId, r);
    store.server.recognitions = map;
  } catch { /* keep the current map */ }
}

// ---------- Human-readable summary ----------

function renderSummary() {
  const main = $('#review-main');
  main.replaceChildren();

  const play = store.currentPlay();
  if (!play) {
    main.append(el('div', { class: 'empty-state' }, 'No project open.'));
    return;
  }

  const inv = store.editor.project?.inventories?.[0];
  const hostCount = inv ? countHosts(inv) : 0;
  const tasks = play.tasks ?? [];
  const handlers = play.handlers ?? [];

  // --- Targets block ---
  const targetsBlock = el('div', { class: 'review-block' },
    el('h3', {}, 'Targets'));
  if (hostCount > 0) {
    const groupBits = (inv.groups ?? []).map((g) => `${(g.hosts ?? []).length} in “${g.name}”`);
    const ungrouped = (inv.hosts ?? []).length;
    if (ungrouped) groupBits.push(`${ungrouped} ungrouped`);
    targetsBlock.append(el('div', {},
      el('span', { class: 'review-num' }, String(hostCount)),
      el('span', { class: 'review-unit' }, `machine(s) — ${groupBits.join(', ')}`)));

    // Concrete machine names, so the review is about real infrastructure.
    const hosts = collectHosts(inv);
    const shown = hosts.slice(0, 8).map((h) => h.address || h.name);
    const more = hosts.length > shown.length ? ` and ${hosts.length - shown.length} more` : '';
    targetsBlock.append(el('div', { class: 'hint-line' }, shown.join(', ') + more));

    if (play.hosts && play.hosts !== 'all') {
      targetsBlock.append(el('div', { class: 'hint-line' }, `Automation runs only on group “${play.hosts}”.`));
    }
  } else {
    targetsBlock.append(el('div', { class: 'hint-line' },
      'No machines configured yet — add them on the Targets page.'));
  }
  main.append(targetsBlock);

  // --- Automation steps block ---
  const stepsBlock = el('div', { class: 'review-block' },
    el('h3', {}, 'Automation'));
  if (!tasks.length && !handlers.length) {
    stepsBlock.append(el('div', { class: 'hint-line' },
      'Nothing to do yet — add automation on the Build page.'));
  } else {
    const recog = store.server.recognitions ?? new Map();
    tasks.forEach((t, i) => {
      const rec = recog.get(t.id);
      stepsBlock.append(el('div', { class: 'review-step' },
        el('span', { class: 'n' }, `${i + 1}.`),
        el('span', {},
          el('span', { class: 's-name' }, rec?.label ?? t.name ?? '(unnamed)'),
          rec?.subtitle ? el('span', { class: 's-sub' }, rec.subtitle) : null,
          (t.notify?.length) ? el('span', { class: 's-handler' }, ` ↳ when changed: ${t.notify.join(', ')}`) : null)));
    });
    for (const h of handlers) {
      const rec = recog.get(h.id);
      stepsBlock.append(el('div', { class: 'review-step' },
        el('span', { class: 'n' }, '↳'),
        el('span', {},
          el('span', { class: 's-name' }, rec?.label ?? h.name ?? '(unnamed)'),
          el('span', { class: 's-sub' }, 'runs when something changes'))));
    }
  }
  main.append(stepsBlock);

  // --- Scope block ---
  const scopeBlock = el('div', { class: 'review-block' },
    el('h3', {}, 'Expected scope'));
  scopeBlock.append(el('div', {},
    el('span', { class: 'review-num' }, String(hostCount)),
    el('span', { class: 'review-unit' }, 'machine(s)')));
  scopeBlock.append(el('div', { style: 'margin-top:4px' },
    el('span', { class: 'review-num' }, String(tasks.length + handlers.length)),
    el('span', { class: 'review-unit' }, 'automation step(s)')));
  main.append(scopeBlock);

  // --- Warnings block ---
  const warnings = computeWarnings(play, inv, hostCount);
  const warnBlock = el('div', { class: 'review-block' },
    el('h3', {}, 'Warnings'));
  if (!warnings.length) {
    warnBlock.append(el('div', { class: 'review-ok' }, '✓ Nothing to worry about.'));
  } else {
    for (const w of warnings) warnBlock.append(el('div', { class: 'review-warning' }, w));
  }
  main.append(warnBlock);

  // --- Advanced details (optional, for Ansible users) ---
  const advBtn = el('button', { class: 'mini-btn' }, '▸ View advanced details (modules and arguments)');
  const advWrap = el('div', { class: 'hidden' });
  advBtn.addEventListener('click', () => {
    const open = advWrap.classList.toggle('hidden');
    advBtn.textContent = (open ? '▸' : '▾') + ' View advanced details (modules and arguments)';
    if (!open) renderAdvancedDetails(advWrap, play);
  });
  main.append(el('div', { class: 'review-block' }, advBtn, advWrap));
}

function renderAdvancedDetails(wrap, play) {
  wrap.replaceChildren();
  const recog = store.server.recognitions ?? new Map();
  const rows = [...(play.tasks ?? []).map((t) => ({ t, kind: 'step' })),
    ...(play.handlers ?? []).map((t) => ({ t, kind: 'handler' }))];
  for (const { t, kind } of rows) {
    const rec = recog.get(t.id);
    const details = el('div', { class: 'ansible-details' });
    details.append(el('div', { class: 'kv' },
      el('span', { class: 'k' }, kind),
      el('span', { class: 'v' }, rec?.recognized ? `${rec.label} (${t.module})` : t.module)));
    for (const [k, v] of Object.entries(t.args ?? {})) {
      details.append(el('div', { class: 'kv' },
        el('span', { class: 'k' }, k),
        el('span', { class: 'v' }, typeof v === 'object' ? JSON.stringify(v) : String(v))));
    }
    for (const [k, v] of Object.entries({
      when: t.when, register: t.register, become: t.become,
      notify: t.notify?.join(', '), tags: t.tags?.join(', '),
    })) {
      if (v === undefined || v === null || v === false || v === '') continue;
      details.append(el('div', { class: 'kv' },
        el('span', { class: 'k' }, k), el('span', { class: 'v' }, String(v))));
    }
    wrap.append(details);
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

function computeWarnings(play, inv, hostCount) {
  const warnings = [];
  if (hostCount === 0) {
    warnings.push('No target machines — deployment would do nothing.');
  }
  // Steps that cannot be shown as a simple automation stay Advanced; say
  // so rather than letting the user wonder why a card looks technical.
  const recog = store.server.recognitions ?? new Map();
  const advanced = [...(play.tasks ?? []), ...(play.handlers ?? [])]
    .filter((t) => recog.size > 0 && !recog.get(t.id)?.recognized);
  if (advanced.length) {
    warnings.push(`${advanced.length} step(s) use advanced Ansible settings and are shown as technical tasks: ` +
      advanced.slice(0, 3).map((t) => `“${t.name}”`).join(', ') +
      (advanced.length > 3 ? ` and ${advanced.length - 3} more` : '') + '.');
  }
  // Actions whose collection is missing would fail at deploy time.
  const missing = (store.server.actions ?? []).filter((a) => a.available === false);
  if (missing.length) {
    const usedMissing = [...(play.tasks ?? [])].some((t) =>
      missing.some((a) => t.module.startsWith(a.requiresCollection + '.')));
    if (usedMissing) {
      warnings.push('Some steps need Ansible collections that are not installed on this machine.');
    }
  }
  const tasks = [...(play.tasks ?? []), ...(play.handlers ?? [])];
  for (const t of tasks) {
    if (t.module === 'ansible.builtin.shell' || t.module === 'ansible.builtin.command') {
      warnings.push(`“${t.name}” runs a raw command. That works, but is harder to make repeatable than a purpose-built automation step.`);
    }
  }
  if (!play.become && tasks.some((t) => t.become !== true) && needsPrivileges(tasks)) {
    warnings.push('Some steps usually need administrator privileges (installing packages, managing services). Enable “administrator privileges” on the Build page if deployment fails with permission errors.');
  }
  if (hostCount > 0 && inv) {
    const noCred = collectHosts(inv).filter((h) => !h.credentialId && !h.vars?.ansible_password);
    if (noCred.length > 0 && noCred.length === collectHosts(inv).length) {
      warnings.push('No machines have credentials assigned — connections will rely on your default SSH keys/agent.');
    }
  }
  return warnings;
}

function needsPrivileges(tasks) {
  const adminModules = ['ansible.builtin.package', 'ansible.builtin.apt', 'ansible.builtin.dnf',
    'ansible.builtin.yum', 'ansible.builtin.service', 'ansible.builtin.systemd_service',
    'ansible.builtin.user'];
  return tasks.some((t) => adminModules.includes(t.module));
}

function collectHosts(inv) {
  const out = [...(inv.hosts ?? [])];
  const walk = (g) => {
    out.push(...(g.hosts ?? []));
    (g.children ?? []).forEach(walk);
  };
  (inv.groups ?? []).forEach(walk);
  return out;
}

// ---------- YAML view (secondary, explicit sync) ----------

let lastRenderedYaml = '';

async function renderYamlView() {
  const pre = $('#yaml-view');
  const status = $('#yaml-status');
  pre.replaceChildren();
  $('#yaml-diagnostics').replaceChildren();
  status.textContent = 'rendering…';
  try {
    const { yaml } = await api.render(store.currentPlaybook());
    lastRenderedYaml = yaml;
    renderYaml(pre, yaml);
    status.textContent = 'Generated from your automation — always up to date.';
    store.ui.yamlDirty = false;
  } catch (e) {
    status.textContent = '';
    const msg = e.problems?.length ? e.problems.join('\n') : e.message;
    pre.append(el('div', { class: 'yaml-error' }, `Cannot render: ${msg}`));
  }
}

function enterYamlEdit() {
  if (store.ui.yamlDirty) {
    api.render(store.currentPlaybook())
      .then(({ yaml }) => { lastRenderedYaml = yaml; $('#yaml-editor').value = yaml; })
      .catch(() => { $('#yaml-editor').value = lastRenderedYaml; });
  }
  $('#yaml-editor').value = lastRenderedYaml;
  $('#yaml-editor').classList.remove('hidden');
  $('#yaml-view').classList.add('hidden');
  $('#btn-edit-yaml').classList.add('hidden');
  $('#btn-render-yaml').classList.add('hidden');
  $('#btn-download-yaml').classList.add('hidden');
  $('#btn-apply-yaml').classList.remove('hidden');
  $('#btn-cancel-yaml').classList.remove('hidden');
  $('#yaml-status').textContent = 'editing — Apply parses the YAML back into the editor';
}

function exitYamlEdit() {
  $('#yaml-editor').classList.add('hidden');
  $('#yaml-view').classList.remove('hidden');
  $('#btn-edit-yaml').classList.remove('hidden');
  $('#btn-render-yaml').classList.remove('hidden');
  $('#btn-download-yaml').classList.remove('hidden');
  $('#btn-apply-yaml').classList.add('hidden');
  $('#btn-cancel-yaml').classList.add('hidden');
  $('#yaml-status').textContent = '';
}

async function applyYamlEdit() {
  const yaml = $('#yaml-editor').value;
  const diagEl = $('#yaml-diagnostics');
  diagEl.replaceChildren();
  let res;
  try {
    res = await api.parseYaml(yaml, store.currentPlaybook()?.name ?? 'imported');
  } catch (e) {
    diagEl.append(el('div', { class: 'diag error' }, `Parse failed: ${e.message}`));
    return;
  }
  const diags = res.diagnostics ?? [];
  for (const d of diags) {
    diagEl.append(el('div', { class: `diag ${d.severity}` },
      el('span', { class: 'path' }, d.path), d.message));
  }
  if (diags.some((d) => d.severity === 'error')) {
    diagEl.prepend(el('div', { class: 'diag error' },
      'Errors must be resolved before this YAML can be applied.'));
    return;
  }
  const warns = diags.length ? ` with ${diags.length} warning(s) (listed above)` : '';
  if (!confirm(`Replace the current automation${warns}? You can undo with Ctrl/Cmd+Z.`)) return;
  store.commit('yaml apply', (proj) => {
    proj.playbooks[0] = res.playbook;
  });
  store.editor.selected = null;
  exitYamlEdit();
  store.ui.yamlDirty = true;
  renderSummary();
  renderYamlView();
}

async function downloadYaml() {
  try {
    const { yaml } = await api.render(store.currentPlaybook());
    const blob = new Blob([yaml], { type: 'application/yaml' });
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = `${store.currentPlaybook()?.name ?? 'playbook'}.yml`;
    a.click();
    URL.revokeObjectURL(a.href);
  } catch (e) {
    const msg = e.problems?.length ? humanizeProblems(e.problems) : e.message;
    alert(`Cannot export the YAML yet:\n\n${msg}`);
  }
}
