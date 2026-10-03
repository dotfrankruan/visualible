// Curated Actions: beginner-friendly forms over action definitions from
// the backend. Actions generate ordinary IR tasks — the same IR powers
// Simple and Advanced views, and any task that can no longer be
// represented faithfully falls back to the Advanced editor.

import { api } from './api.js';
import * as store from './store.js';
import { el } from './form.js';
import { renderAdvancedTaskForm } from './build.js';

// ---------- Form for a new (draft) action ----------

export function renderActionForm(container, draft, onDone) {
  const { def, params } = draft;
  container.replaceChildren();

  container.append(el('div', { class: 'props-section' },
    el('div', { class: 'props-heading' }, `${def.icon ?? ''} ${def.name}`)));

  if (def.explanation) {
    container.append(el('div', { class: 'action-explain' }, def.explanation));
  }

  const form = el('div', {});
  for (const f of def.fields ?? []) {
    form.append(actionField(f, params));
  }
  container.append(form);

  const errEl = el('div', { class: 'yaml-error', style: 'margin-bottom:8px' });
  const addBtn = el('button', { class: 'primary' }, 'Add automation');
  addBtn.addEventListener('click', async () => {
    errEl.replaceChildren();
    const missing = (def.fields ?? []).filter((f) => f.required && isEmpty(params[f.id]));
    if (missing.length) {
      errEl.textContent = `${missing[0].label} is required.`;
      return;
    }
    addBtn.disabled = true;
    try {
      const gen = await api.actionGenerate(def.id, params);
      applyGenerated(gen);
      onDone();
    } catch (e) {
      errEl.textContent = e.problems?.length ? e.problems.join('\n') : e.message;
      addBtn.disabled = false;
    }
  });
  const cancelBtn = el('button', { class: 'mini-btn', style: 'margin-left:6px' }, 'Cancel');
  cancelBtn.addEventListener('click', onDone);
  container.append(el('div', { style: 'margin-top:12px' }, addBtn, cancelBtn));
}

function applyGenerated(gen) {
  const tasks = gen.tasks ?? [];
  const handlers = gen.handlers ?? [];
  if (!tasks.length && !handlers.length) return;
  store.commit('add action', (proj) => {
    const play = store.mutatePlay(proj);
    if (!play) return;
    play.tasks = play.tasks ?? [];
    play.tasks.push(...tasks);
    if (handlers.length) {
      play.handlers = play.handlers ?? [];
      // Only add handlers that do not exist yet (by name).
      const existing = new Set(play.handlers.map((h) => h.name));
      for (const h of handlers) {
        if (!existing.has(h.name)) play.handlers.push(h);
      }
    }
  });
  store.editor.selected = { kind: 'task', id: tasks[0]?.id ?? handlers[0]?.id };
}

// ---------- Editing a recognized action ----------

export function renderRecognizedForm(container, task, kind, rec) {
  container.replaceChildren();
  const def = (store.server.actions ?? []).find((a) => a.id === rec.action);
  if (!def) {
    renderAdvancedTaskForm(container, task, kind);
    return;
  }

  container.append(el('div', { class: 'props-section' },
    el('div', { class: 'props-heading' }, `${rec.icon ?? def.icon ?? ''} ${def.name}`)));
  if (def.explanation) {
    container.append(el('div', { class: 'action-explain' }, def.explanation,
      el('span', { class: 'powered' }, `powered by ${task.module}`)));
  }

  const params = { ...rec.params };
  const form = el('div', {});
  for (const f of def.fields ?? []) {
    form.append(actionField(f, params, () => regenerate(task, def, params)));
  }
  container.append(form);

  // "Show Ansible details" learning path.
  const advBtn = el('button', { class: 'mini-btn' }, '▸ Show Ansible details');
  const advWrap = el('div', { class: 'hidden' });
  advBtn.addEventListener('click', () => {
    const open = advWrap.classList.toggle('hidden');
    advBtn.textContent = (open ? '▸' : '▾') + ' Show Ansible details';
    if (!open) {
      advWrap.replaceChildren();
      const advContainer = el('div', {});
      renderAdvancedTaskForm(advContainer, task, kind);
      advWrap.append(advContainer);
    }
  });
  container.append(el('div', { class: 'advanced-block' }, advBtn, advWrap));
}

// regenerate re-renders the curated task from its action definition,
// preserving identity and position in the IR.
async function regenerate(task, def, params) {
  try {
    const gen = await api.actionGenerate(def.id, params);
    const fresh = gen.tasks?.[0];
    if (!fresh) return;
    store.commit('edit action', () => {
      task.name = fresh.name;
      task.module = fresh.module;
      task.args = fresh.args;
      task.become = fresh.become;
      if (fresh.notify !== undefined) task.notify = fresh.notify;
    });
  } catch { /* keep current values on transient failures */ }
}

// ---------- Field widgets (shared by draft + recognized forms) ----------

function actionField(f, params, onChange) {
  const value = params[f.id];
  const label = el('label', {},
    el('span', { class: 'fname' }, f.label),
    f.required ? el('span', { class: 'badge required' }, 'required') : null);
  const wrap = el('div', { class: 'field' }, label);
  if (f.help) wrap.append(el('div', { class: 'fdesc' }, f.help));

  const set = (v) => {
    if (isEmpty(v)) delete params[f.id];
    else params[f.id] = v;
    onChange?.();
  };

  switch (f.type) {
    case 'bool': {
      const cb = el('input', { type: 'checkbox' });
      cb.checked = value === true;
      cb.addEventListener('change', () => set(cb.checked));
      wrap.append(cb);
      break;
    }
    case 'select': {
      const sel = el('select', {});
      for (const c of f.choices ?? []) {
        const o = el('option', { value: String(c.value) }, c.label);
        if (String(c.value) === String(value)) o.selected = true;
        sel.append(o);
      }
      sel.addEventListener('change', () => {
        const chosen = (f.choices ?? []).find((c) => String(c.value) === sel.value);
        set(chosen ? chosen.value : sel.value);
      });
      wrap.append(sel);
      break;
    }
    case 'textarea': {
      const ta = el('textarea', { placeholder: f.placeholder ?? '' });
      ta.value = value ?? '';
      ta.addEventListener('change', () => set(ta.value));
      wrap.append(ta);
      break;
    }
    default: { // text
      const input = el('input', { type: 'text', value: value ?? '', placeholder: f.placeholder ?? '' });
      input.addEventListener('change', () => set(input.value));
      wrap.append(input);
    }
  }
  return wrap;
}

function isEmpty(v) {
  return v === undefined || v === null || v === '';
}
