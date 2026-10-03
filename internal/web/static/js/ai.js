// AI assistant: natural-language change requests that become reviewable
// IR change proposals. The model proposes desired state; Visualible
// validates, diffs, merges and applies — the model is never authoritative
// about what changed, and AI never deploys.

import { api } from './api.js';
import * as store from './store.js';
import { el } from './form.js';
import { humanizeProblems } from './errors.js';
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
  $('#ai-status').replaceChildren();
  $('#ai-result').replaceChildren();
  $('#ai-review-head').replaceChildren();
  $('#ai-foot-note').replaceChildren();
  $('#ai-compose').classList.remove('hidden');
  $('#ai-review').classList.add('hidden');
  $('#ai-apply').disabled = true;
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
  status.replaceChildren('Generating proposal…');
  $('#ai-generate').disabled = true;
  resetProposalView();
  status.replaceChildren('Generating proposal…');

  // Snapshot the base the diff will be computed against.
  baseRevision = store.editor.revision;
  basePlaybook = JSON.parse(JSON.stringify(store.currentPlaybook()));
  accepted = new Map();

  try {
    proposal = await api.aiPropose(intent, basePlaybook, baseRevision);
  } catch (e) {
    status.replaceChildren(e.code === 'ai_not_configured'
      ? 'AI is not configured. Open Settings to set an endpoint and model.'
      : `Generation failed: ${e.message}`);
    $('#ai-generate').disabled = false;
    return;
  }
  $('#ai-generate').disabled = false;

  if (proposal.status === 'failed') {
    status.replaceChildren('The AI produced output Visualible could not accept. Nothing was changed.');
    for (const d of proposal.diagnostics ?? []) {
      status.append(el('div', { class: 'diag error' }, d));
    }
    return;
  }

  status.replaceChildren();
  renderProposal();
}

// ---------- Proposal review ----------
//
// All wording, counts, detail extraction and risk hints come from the
// backend view model (internal/present), which is unit-tested. This file
// renders that model; it does not invent descriptions of its own.

function renderProposal() {
  $('#ai-compose').classList.add('hidden');
  $('#ai-review').classList.remove('hidden');

  const head = $('#ai-review-head');
  const list = $('#ai-result');
  const footNote = $('#ai-foot-note');
  head.replaceChildren();
  list.replaceChildren();
  footNote.replaceChildren();

  // Stale protection: the project changed while the model was working.
  if (store.editor.revision !== baseRevision) {
    head.append(el('div', { class: 'stale-banner' },
      `This playbook changed while the AI was generating (the proposal is based on revision ${baseRevision}, the current revision is ${store.editor.revision}). `,
      el('button', { class: 'mini-btn', onclick: generate }, 'Generate again'),
      ' ',
      el('button', { class: 'mini-btn', onclick: discardProposal }, 'Discard')));
    $('#ai-apply').disabled = true;
    return;
  }

  const view = proposal.view;
  if (!view) {
    head.append(el('div', { class: 'diag error' }, 'The proposal arrived without a review model. Nothing was changed.'));
    $('#ai-apply').disabled = true;
    return;
  }

  // --- Request + summary ---
  head.append(el('div', { class: 'proposal-request' },
    el('span', { class: 'pr-label' }, 'Request'),
    el('span', { class: 'pr-text' }, `“${proposal.request}”`)));

  const summary = el('div', { class: 'proposal-headline' },
    el('span', { class: 'ph-count' }, view.headline));
  if (view.summaryParts?.length) {
    const parts = el('div', { class: 'proposal-summary' });
    for (const p of view.summaryParts) {
      parts.append(el('span', { class: `sum ${p.kind}` }, p.label));
    }
    summary.append(parts);
  }
  head.append(summary);

  if (view.hasRemovals) {
    head.append(el('div', { class: 'removal-notice' },
      `⚠ This proposal would remove ${view.removals === 1 ? 'one existing automation' : view.removals + ' existing automations'}. Removals are never applied unless you accept them.`));
  }

  // --- Bulk actions ---
  const actionable = (view.changes ?? []).map((c) => c.id);
  head.append(el('div', { class: 'proposal-bulk' },
    el('button', {
      class: 'mini-btn',
      onclick: () => acceptAll(view),
    }, view.bulkAcceptLabel ?? 'Accept all'),
    view.bulkAcceptNote ? el('span', { class: 'hint-line' }, view.bulkAcceptNote) : null,
    el('button', {
      class: 'mini-btn',
      onclick: () => { accepted = new Map(); renderProposal(); },
    }, view.bulkRejectLabel ?? 'Reject all')));

  // --- Rationale (optional, condensed) ---
  if (proposal.rationale?.length) {
    head.append(rationaleBlock(proposal.rationale));
  }

  // --- Changes in execution order ---
  if (!view.changes?.length) {
    list.append(el('div', { class: 'empty-state' },
      'The AI did not propose any changes — the automation already matches your request.'));
  }
  for (const c of view.changes ?? []) {
    list.append(changeCard(c));
  }

  // --- Advanced inspection of the raw result ---
  list.append(rawProposalBlock());

  updateApplyButton();
}

function acceptAll(view) {
  const removals = view.removals ?? 0;
  if (removals > 0) {
    const ok = confirm(
      `Accept all ${view.additions + view.modifications + view.deletions + view.moves} changes?\n\n` +
      `This includes ${removals} removal${removals === 1 ? '' : 's'} of existing automation.`);
    if (!ok) return;
  }
  for (const c of view.changes ?? []) accepted.set(c.id, true);
  renderProposal();
}

function rationaleBlock(lines) {
  const first = lines.slice(0, 2);
  const rest = lines.slice(2);
  const box = el('div', { class: 'rationale-box' },
    el('div', { class: 'rationale-title' }, 'Why these changes?'));
  for (const line of first) box.append(el('div', { class: 'rationale-line' }, '• ' + line));
  if (rest.length) {
    const more = el('div', { class: 'hidden' });
    for (const line of rest) more.append(el('div', { class: 'rationale-line' }, '• ' + line));
    const toggle = el('button', { class: 'mini-btn' }, `Show full rationale (${lines.length} points)`);
    toggle.addEventListener('click', () => {
      const open = more.classList.toggle('hidden');
      toggle.textContent = (open ? 'Show full rationale' : 'Hide rationale') + ` (${lines.length} points)`;
    });
    box.append(more, toggle);
  }
  box.append(el('div', { class: 'rationale-note' },
    'The proposal below is what will be applied — the explanation is informational only.'));
  return box;
}

function rawProposalBlock() {
  const wrap = el('div', { class: 'advanced-block' });
  const btn = el('button', { class: 'mini-btn' }, '▸ Inspect proposed IR / YAML');
  const body = el('div', { class: 'hidden' });
  btn.addEventListener('click', async () => {
    const open = body.classList.toggle('hidden');
    btn.textContent = (open ? '▸' : '▾') + ' Inspect proposed IR / YAML';
    if (!open) {
      body.replaceChildren(el('div', { class: 'empty-state' }, 'Rendering…'));
      try {
        const { yaml } = await api.render(proposal.proposedIr);
        const pre = el('pre', { class: 'mono yaml' });
        renderYaml(pre, yaml);
        body.replaceChildren(pre);
      } catch (e) {
        body.replaceChildren(el('div', { class: 'yaml-error' }, e.message));
      }
    }
  });
  wrap.append(btn, body);
  return wrap;
}

// ---------- Change cards ----------

function changeCard(v) {
  const state = accepted.get(v.id); // true accepted · false rejected · undefined pending
  const card = el('div', {
    class: `change-card ${v.kind}` +
      (state === true ? ' accepted' : '') + (state === false ? ' rejected' : ''),
    'data-change-id': v.id,
  });

  // Header: icon + textual rank (never colour alone) + title + state badge.
  card.append(el('div', { class: 'change-head' },
    el('span', { class: 'c-icon' }, v.icon),
    el('span', { class: 'c-rank' }, v.kindRank),
    el('div', { class: 'c-title-block' },
      el('div', { class: 'c-name' }, v.title),
      v.subtitle ? el('div', { class: 'c-sub' }, v.subtitle) : null),
    el('span', { class: 'c-state ' + stateClass(state) }, stateText(v, state))));

  const body = el('div', { class: 'change-body' });

  if (v.destructive) {
    body.append(el('div', { class: 'destructive-note' },
      'This existing automation would be removed.'));
  }

  // Risk hints (deterministic, from the backend).
  if (v.risks?.length) {
    const riskRow = el('div', { class: 'risk-row' });
    for (const r of v.risks) riskRow.append(el('span', { class: 'risk' }, '⚠ ' + r));
    body.append(riskRow);
  }

  // What this change does, in machine-level language.
  if (v.kind === 'removed' && v.currentDetails?.length) {
    body.append(el('div', { class: 'detail-group-label' }, 'Current behaviour'));
    body.append(detailRows(v.currentDetails));
  } else if (v.details?.length) {
    body.append(detailRows(v.details));
  }

  // Changed fields only (modified items).
  if (v.fieldChanges?.length) {
    body.append(fieldChanges(v.fieldChanges));
  }

  // Dependencies between proposed changes.
  if (v.dependsOn?.length) {
    const dep = el('div', { class: 'depends' });
    for (const d of v.dependsOn) {
      dep.append(el('div', {}, '↳ Depends on: ' + d));
    }
    body.append(dep);
  }

  // Unchanged fields stay available without competing with the change.
  if (v.resultDetails?.length) {
    const rest = el('div', { class: 'hidden' });
    rest.append(detailRows(v.resultDetails));
    const toggle = el('button', { class: 'mini-btn' }, '▸ Show unchanged fields');
    toggle.addEventListener('click', () => {
      const open = rest.classList.toggle('hidden');
      toggle.textContent = (open ? '▸' : '▾') + ' Show unchanged fields';
    });
    body.append(toggle, rest);
  }

  // Technical details: module + real arguments, structured.
  body.append(techDetails(v));

  // Decision buttons.
  body.append(el('div', { class: 'change-actions' },
    el('button', {
      class: 'mini-btn' + (state === true ? ' chosen' : ''),
      onclick: () => { accepted.set(v.id, true); renderProposal(); },
    }, v.acceptLabel),
    el('button', {
      class: 'mini-btn' + (state === false ? ' chosen' : ''),
      onclick: () => { accepted.set(v.id, false); renderProposal(); },
    }, v.rejectLabel),
    state !== undefined
      ? el('button', {
          class: 'mini-btn linkish',
          onclick: () => { accepted.delete(v.id); renderProposal(); },
        }, 'Undo decision')
      : null));

  card.append(body);
  return card;
}

function stateClass(state) {
  if (state === true) return 'accepted';
  if (state === false) return 'rejected';
  return 'pending';
}

// stateText mirrors internal/present.StateLabel (unit-tested there).
function stateText(v, state) {
  if (state === undefined) return '○ Pending decision';
  if (state === false) {
    if (v.kind === 'added') return '× Not added';
    if (v.kind === 'removed') return '× Kept as is';
    if (v.kind === 'moved') return '× Kept in place';
    return '× Kept as is';
  }
  if (v.kind === 'added') return '✓ Will be added';
  if (v.kind === 'removed') return '✓ Will be removed';
  if (v.kind === 'moved') return '✓ Will be moved';
  return '✓ Will be changed';
}

// detailRows renders labelled facts, lists and summarized large values.
function detailRows(details) {
  const wrap = el('div', { class: 'detail-rows' });
  for (const d of details ?? []) {
    if (d.long) {
      wrap.append(longDetail(d));
      continue;
    }
    if (d.values?.length) {
      const list = el('div', { class: 'detail-list' });
      for (const v of d.values) list.append(el('div', { class: 'detail-item' }, v));
      wrap.append(el('div', { class: 'detail-row' },
        el('span', { class: 'dr-label' }, d.label), list));
      continue;
    }
    wrap.append(el('div', { class: 'detail-row' },
      el('span', { class: 'dr-label' }, d.label),
      el('span', { class: 'dr-value' }, d.value ?? '')));
  }
  return wrap;
}

// longDetail keeps cards scannable: a line count and an on-demand preview
// instead of a wall of text.
function longDetail(d) {
  const row = el('div', { class: 'detail-row' },
    el('span', { class: 'dr-label' }, d.label),
    el('span', { class: 'dr-value' }, `${d.long.lines} lines`),
    el('button', { class: 'mini-btn' }, 'Preview'));
  const preview = el('pre', { class: 'mono long-preview hidden' }, d.long.preview);
  row.querySelector('button').addEventListener('click', (e) => {
    const open = preview.classList.toggle('hidden');
    e.target.textContent = open ? 'Preview' : 'Hide preview';
  });
  const wrap = el('div', {}, row, preview);
  return wrap;
}

// fieldChanges renders only what differs, side by side.
function fieldChanges(fields) {
  const wrap = el('div', { class: 'field-changes' });
  for (const f of fields) {
    const block = el('div', { class: 'field-change' },
      el('div', { class: 'fc-label' }, f.label));
    if (f.valueKind === 'list') {
      block.append(listChange(f));
    } else if (f.fromLong || f.toLong) {
      block.append(el('div', { class: 'fc-long' },
        el('span', {}, f.fromLong ? `(${f.fromLong.lines} lines)` : (f.from || '(not set)')),
        el('span', { class: 'fc-arrow' }, ' → '),
        el('span', {}, f.toLong ? `(${f.toLong.lines} lines)` : (f.to || '(not set)'))));
    } else {
      block.append(el('div', { class: 'fc-values' },
        el('span', { class: 'fc-from' }, f.from || '(not set)'),
        el('span', { class: 'fc-arrow' }, '→'),
        el('span', { class: 'fc-to' }, f.to || '(not set)')));
    }
    wrap.append(block);
  }
  return wrap;
}

// listChange shows current vs proposed lists and highlights the actual
// additions/removals inside them.
function listChange(f) {
  const from = f.fromList ?? [];
  const to = f.toList ?? [];
  const cols = el('div', { class: 'fc-lists' });
  const mkCol = (title, items, other, kind) => {
    const col = el('div', { class: 'fc-list' }, el('div', { class: 'fc-list-title' }, title));
    for (const item of items) {
      const changed = !other.includes(item);
      col.append(el('div', { class: 'fc-list-item ' + (changed ? kind : '') }, item));
    }
    if (!items.length) col.append(el('div', { class: 'fc-list-item muted' }, '(empty)'));
    return col;
  };
  cols.append(mkCol('Current', from, to, 'removed-item'));
  cols.append(mkCol('Proposed', to, from, 'added-item'));
  return cols;
}

// techDetails is the reusable "Show Ansible details" expander.
function techDetails(v) {
  const wrap = el('div', { class: 'tech-block' });
  const btn = el('button', { class: 'mini-btn' }, '▸ Show Ansible details');
  const body = el('div', { class: 'ansible-details hidden' });

  const mk = (label, value) => el('div', { class: 'kv' },
    el('span', { class: 'k' }, label), el('span', { class: 'v' }, value));

  body.append(mk('Module', v.module || '(none)'));
  if (v.kind === 'removed' && v.currentModule && v.currentModule !== v.module) {
    body.append(mk('Current module', v.currentModule));
  }
  const args = v.kind === 'removed' ? v.currentArguments : v.arguments;
  for (const a of args ?? []) {
    if (a.long) {
      body.append(el('div', { class: 'kv' },
        el('span', { class: 'k' }, a.label),
        el('span', { class: 'v' }, `${a.long.lines} lines, ${a.long.chars} characters`)));
      const pre = el('pre', { class: 'mono long-preview hidden' }, a.long.preview);
      const pb = el('button', { class: 'mini-btn' }, 'Preview');
      pb.addEventListener('click', () => {
        const open = pre.classList.toggle('hidden');
        pb.textContent = open ? 'Preview' : 'Hide preview';
      });
      body.append(pb, pre);
      continue;
    }
    body.append(mk(a.label, a.values?.length ? a.values.join(', ') : (a.value ?? '')));
  }
  if (v.handler) {
    body.append(mk('Ansible handler', 'runs only when notified by a changed step'));
  }

  btn.addEventListener('click', () => {
    const open = body.classList.toggle('hidden');
    btn.textContent = (open ? '▸' : '▾') + ' Show Ansible details';
  });
  wrap.append(btn, body);
  return wrap;
}

function updateApplyButton() {
  const btn = $('#ai-apply');
  const n = [...accepted.values()].filter(Boolean).length;
  // Mirrors internal/present.ApplyLabel, which is unit-tested.
  if (n === 0) btn.textContent = 'Apply selected changes';
  else if (n === 1) btn.textContent = 'Apply 1 change';
  else btn.textContent = `Apply ${n} changes`;
  btn.disabled = n === 0;

  const note = $('#ai-foot-note');
  note.replaceChildren();
  if (n > 0) {
    const removals = [...accepted.entries()].filter(([id, v]) => v && id.includes(':removed:')).length;
    note.append(`${n} of ${(proposal?.view?.changes ?? []).length} selected`);
    if (removals) note.append(` · includes ${removals} removal${removals === 1 ? '' : 's'}`);
  }
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
    // Replace the document the proposal was generated for, keeping its
    // identity so the user stays in the same playbook after applying.
    const targetId = store.currentPlaybook()?.id;
    store.commit('ai apply', (proj) => {
      const i = (proj.playbooks ?? []).findIndex((pb) => pb.id === targetId);
      if (i >= 0) proj.playbooks[i] = { ...playbook, id: targetId, name: proj.playbooks[i].name };
      else if (proj.playbooks?.length) proj.playbooks[0] = playbook;
    });
    store.editor.selected = null;
    closeAI();
    goStage('build');
  } catch (e) {
    // The backend explains dependency conflicts in plain language; the raw
    // validation problem stays available underneath.
    showApplyError(e);
    btn.disabled = false;
  }
}

function showApplyError(e) {
  const note = $('#ai-foot-note');
  note.replaceChildren();
  const box = el('div', { class: 'apply-error' }, e.message);
  if (e.problems?.length) {
    const raw = el('div', { class: 'hidden' });
    for (const p of e.problems) raw.append(el('div', { class: 'mono small' }, p));
    const toggle = el('button', { class: 'mini-btn' }, 'Show details');
    toggle.addEventListener('click', () => {
      const open = raw.classList.toggle('hidden');
      toggle.textContent = open ? 'Show details' : 'Hide details';
    });
    box.append(toggle, raw);
  }
  note.append(box);
}
