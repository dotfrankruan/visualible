// AI assistant: natural-language change requests that become reviewable
// IR change proposals. The model proposes desired state; Visualible
// validates, diffs, merges and applies — the model is never authoritative
// about what changed, and AI never deploys.

import { api } from './api.js';
import * as store from './store.js';
import { el } from './form.js';
import { renderYaml } from './yaml.js';
import { goStage, aiConfigured } from './app.js';

const $ = (sel) => document.querySelector(sel);

let proposal = null;      // server proposal document
let baseRevision = 0;     // editor revision when the request was made
let basePlaybook = null;  // base IR snapshot the diff is computed against
let accepted = new Map(); // changeId -> bool

export function wireAI() {
  $('#btn-ai').addEventListener('click', () => openAIWithIntent(''));
  $('#ai-close').addEventListener('click', closeAI);
  $('#ai-modal').addEventListener('click', (e) => {
    if (e.target.id === 'ai-modal') closeAI();
  });
  $('#ai-generate').addEventListener('click', generate);
  $('#ai-apply').addEventListener('click', applySelected);
  $('#ai-discard').addEventListener('click', discardProposal);
}

export function openAIWithIntent(intent) {
  if (!aiConfigured()) {
    if (!confirm('AI is not configured yet. Open Settings to set an endpoint and model?')) return;
    document.querySelector('#btn-settings').click();
    return;
  }
  $('#ai-modal').classList.remove('hidden');
  $('#ai-intent').value = intent;
  resetProposalView();
  $('#ai-intent').focus();
}

function closeAI() {
  $('#ai-modal').classList.add('hidden');
  discardProposal();
}

function resetProposalView() {
  $('#ai-status').textContent = '';
  $('#ai-result').replaceChildren();
  $('#ai-actions').classList.add('hidden');
}

function discardProposal() {
  proposal = null;
  accepted = new Map();
  resetProposalView();
}

// ---------- Generation ----------

async function generate() {
  const intent = $('#ai-intent').value.trim();
  if (!intent) return;
  const status = $('#ai-status');
  status.textContent = 'Generating proposal…';
  $('#ai-generate').disabled = true;
  $('#ai-result').replaceChildren();
  $('#ai-actions').classList.add('hidden');

  // Snapshot the base the diff will be computed against.
  baseRevision = store.editor.revision;
  basePlaybook = JSON.parse(JSON.stringify(store.currentPlaybook()));
  accepted = new Map();

  try {
    proposal = await api.aiPropose(intent, basePlaybook, baseRevision);
  } catch (e) {
    status.textContent = e.code === 'ai_not_configured'
      ? 'AI is not configured. Open Settings to set an endpoint and model.'
      : `Generation failed: ${e.message}`;
    $('#ai-generate').disabled = false;
    return;
  }
  $('#ai-generate').disabled = false;

  if (proposal.status === 'failed') {
    status.textContent = 'The AI produced output Visualible could not accept. Nothing was changed.';
    const res = $('#ai-result');
    for (const d of proposal.diagnostics ?? []) {
      res.append(el('div', { class: 'diag error' }, d));
    }
    return;
  }

  status.textContent = 'Review the proposal below. Nothing is applied until you say so.';
  renderProposal();
}

// ---------- Proposal review ----------

function renderProposal() {
  const res = $('#ai-result');
  res.replaceChildren();
  if (!proposal?.diff) return;

  const d = proposal.diff;

  // Stale protection: the project changed while the model was working.
  if (store.editor.revision !== baseRevision) {
    const banner = el('div', { class: 'stale-banner' },
      `This project changed while the AI was generating (proposal is based on revision ${baseRevision}, current is ${store.editor.revision}). `,
      el('button', { class: 'mini-btn', onclick: generate }, 'Generate again'),
      ' ',
      el('button', { class: 'mini-btn', onclick: discardProposal }, 'Discard'));
    res.append(banner);
    return; // do not offer acceptance of a stale proposal
  }

  res.append(el('div', { class: 'hint-line', style: 'margin-bottom:4px' },
    `Request: “${proposal.request}”`));

  // Summary counts.
  const s = d.summary;
  res.append(el('div', { class: 'proposal-summary' },
    s.added ? el('span', { class: 'sum added' }, `+ ${s.added} addition(s)`) : null,
    s.modified ? el('span', { class: 'sum modified' }, `~ ${s.modified} modification(s)`) : null,
    s.removed ? el('span', { class: 'sum removed' }, `− ${s.removed} removal(s)`) : null,
    s.moved ? el('span', { class: 'sum moved' }, `⇄ ${s.moved} moved`) : null,
    (!s.added && !s.modified && !s.removed && !s.moved)
      ? el('span', { class: 'sum' }, 'No changes — the AI thinks it is already done.')
      : null));

  // Play-level change card.
  if (d.playChanges?.length) {
    res.append(changeCard({
      id: 'play:modified', kind: 'modified', name: 'Automation settings',
      fields: d.playChanges,
    }));
  }

  for (const c of d.changes ?? []) {
    if (c.kind === 'unchanged') continue;
    res.append(changeCard(c));
  }

  // Advanced inspection.
  const adv = el('div', { class: 'advanced-block' });
  const advBtn = el('button', { class: 'mini-btn' }, '▸ Inspect proposed IR / YAML');
  const advWrap = el('div', { class: 'hidden' });
  advBtn.addEventListener('click', async () => {
    const open = advWrap.classList.toggle('hidden');
    advBtn.textContent = (open ? '▸' : '▾') + ' Inspect proposed IR / YAML';
    if (!open) {
      advWrap.replaceChildren(el('div', { class: 'empty-state' }, 'Rendering…'));
      try {
        const { yaml } = await api.render(proposal.proposedIr);
        const pre = el('pre', { class: 'mono yaml' });
        renderYaml(pre, yaml);
        advWrap.replaceChildren(pre);
      } catch (e) {
        advWrap.replaceChildren(el('div', { class: 'yaml-error' }, e.message));
      }
    }
  });
  adv.append(advBtn, advWrap);
  res.append(adv);

  updateApplyButton();
  $('#ai-actions').classList.remove('hidden');
}

const kindMeta = {
  added: { icon: '+', cls: 'added', word: 'Add', rejectWord: 'Reject' },
  modified: { icon: '~', cls: 'modified', word: 'Accept', rejectWord: 'Keep current' },
  moved: { icon: '⇄', cls: 'moved', word: 'Accept move', rejectWord: 'Keep position' },
  removed: { icon: '−', cls: 'removed', word: 'Accept removal', rejectWord: 'Keep it' },
};

function changeCard(c) {
  const meta = kindMeta[c.kind] ?? kindMeta.modified;
  const isRemoval = c.kind === 'removed';
  const state = accepted.get(c.id);

  const head = el('div', { class: 'change-head' },
    el('span', { class: 'c-icon' }, meta.icon),
    el('span', { class: 'c-name' }, c.name ?? '(unnamed)'),
    el('span', { class: 'c-kind' }, c.kind === 'moved' ? `moved ${c.fromPos + 1} → ${c.toPos + 1}` : c.kind),
    el('div', { class: 'c-actions' },
      el('button', {
        class: 'mini-btn' + (state === true ? ' primary-on' : ''),
        onclick: (e) => { setAccepted(c.id, true); refreshCard(e, c.id); },
      }, meta.word),
      el('button', {
        class: 'mini-btn',
        onclick: (e) => { setAccepted(c.id, false); refreshCard(e, c.id); },
      }, meta.rejectWord)));

  const card = el('div', {
    class: `change-card ${meta.cls}` +
      (state === true ? ' accepted' : '') + (state === false ? ' rejected' : ''),
    'data-change-id': c.id,
  }, head);

  // Field-level side-by-side comparison.
  if (c.fields?.length) {
    const body = el('div', { class: 'change-body' });
    for (const f of c.fields) {
      body.append(
        el('div', { class: 'field-diff' },
          el('span', { class: 'fd-path' }, friendlyPath(f.path)),
          el('span', { class: 'fd-from' }, displayValue(f.from) || '(empty)'),
          el('span', { class: 'fd-arrow' }, '→'),
          el('span', { class: 'fd-to' }, displayValue(f.to) || '(empty)')));
    }
    card.append(body);
  } else if (c.kind === 'added' && c.proposed) {
    card.append(el('div', { class: 'change-body' },
      el('div', { class: 'fdesc' }, describeTask(c.proposed))));
  } else if (isRemoval) {
    card.append(el('div', { class: 'change-body' },
      el('div', { class: 'fdesc' },
        'The AI proposes deleting this existing automation. It stays unless you accept the removal.')));
  }
  return card;
}

function setAccepted(id, value) {
  accepted.set(id, value);
  updateApplyButton();
}

function refreshCard(e, id) {
  const card = document.querySelector(`[data-change-id="${CSS.escape(id)}"]`);
  if (card) {
    const state = accepted.get(id);
    card.classList.toggle('accepted', state === true);
    card.classList.toggle('rejected', state === false);
  }
}

function updateApplyButton() {
  const btn = $('#ai-apply');
  const n = [...accepted.values()].filter(Boolean).length;
  const removals = [...accepted.entries()].filter(([id, v]) => v && id.includes(':removed:')).length;
  btn.textContent = n
    ? `Apply ${n} change(s) to project${removals ? ` — includes ${removals} removal(s)!` : ''}`
    : 'Apply to project';
  btn.disabled = n === 0;
}

// ---------- Apply (one undoable operation) ----------

async function applySelected() {
  if (!proposal) return;
  const ids = [...accepted.entries()].filter(([, v]) => v).map(([k]) => k);
  if (!ids.length) return;
  const btn = $('#ai-apply');
  btn.disabled = true;
  try {
    const { playbook } = await api.aiMerge(basePlaybook, proposal.proposedIr, ids);
    store.commit('ai apply', (proj) => {
      proj.playbooks[0] = playbook;
    });
    store.editor.selected = null;
    closeAI();
    goStage('build');
  } catch (e) {
    alert(`Could not apply the selected changes:\n${e.message}`);
    btn.disabled = false;
  }
}

// ---------- Rendering helpers ----------

function friendlyPath(path) {
  return path
    .replace(/^args\./, '')
    .replace(/^play\./, '')
    .replace(/^become$/, 'administrator privileges');
}

function displayValue(v) {
  if (v === undefined || v === null) return '';
  if (typeof v === 'object') return JSON.stringify(v);
  return String(v);
}

function describeTask(t) {
  const parts = [];
  if (t.module) parts.push(`uses ${t.module}`);
  const argBits = Object.entries(t.args ?? {}).slice(0, 4)
    .map(([k, v]) => `${k}: ${displayValue(v)}`);
  if (argBits.length) parts.push(argBits.join(', '));
  if (t.notify?.length) parts.push(`when changed → ${t.notify.join(', ')}`);
  return parts.join(' · ') || 'automation step';
}
