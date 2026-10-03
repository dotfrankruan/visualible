// Schema-driven form generator. Builds module argument forms from
// normalized ansible-doc metadata — never bespoke per-module UI.
//
// Mapping (per architecture spec):
//   bool        -> checkbox
//   int/float   -> numeric input
//   str/path    -> text input
//   choices     -> select
//   list        -> list editor (per-element rows; dict elements recurse)
//   dict        -> nested form (suboptions) or key/value rows (free-form)
//   required    -> visible badge
//   default     -> displayed badge
//
// Coercion rules (explicit, no magic):
//   - numbers are parsed with Number(); empty input clears the key;
//   - free-form dict values: bare YAML scalars are interpreted
//     (true/false/null/numbers), everything else stays a string.

export function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v === undefined || v === null) continue;
    if (k === 'class') node.className = v;
    else if (k === 'text') node.textContent = v;
    else if (k.startsWith('on')) node.addEventListener(k.slice(2), v);
    else if (['checked', 'disabled', 'required', 'multiple', 'selected'].includes(k)) {
      node[k] = Boolean(v);
    } else node.setAttribute(k, v);
  }
  for (const c of children.flat()) {
    if (c == null) continue;
    node.append(c.nodeType ? c : document.createTextNode(String(c)));
  }
  return node;
}

// interpretScalar converts a user-typed scalar using YAML-like rules.
export function interpretScalar(s) {
  const t = s.trim();
  if (t === '' ) return '';
  if (/^(true|yes|on)$/i.test(t)) return true;
  if (/^(false|no|off)$/i.test(t)) return false;
  if (/^(null|~)$/i.test(t)) return null;
  if (/^-?\d+$/.test(t)) return parseInt(t, 10);
  if (/^-?\d*\.\d+$/.test(t)) return parseFloat(t);
  return s;
}

function formatDefault(v) {
  if (v === undefined) return null;
  const s = typeof v === 'object' ? JSON.stringify(v) : String(v);
  return s.length > 40 ? s.slice(0, 37) + '…' : s;
}

// sortedOptions returns option schemas ordered: required first, then
// alphabetical — the most important fields land at the top of the form.
export function sortedOptions(options) {
  const all = Object.values(options ?? {});
  const byName = (a, b) => a.name.localeCompare(b.name);
  return [
    ...all.filter((o) => o.required).sort(byName),
    ...all.filter((o) => !o.required).sort(byName),
  ];
}

// buildOptionsForm renders all options of a module schema into a container.
// values is the live args object; onChange(key, value|undefined) is called
// for every mutation (undefined clears the key).
export function buildOptionsForm(container, options, values, onChange) {
  container.replaceChildren();
  const opts = sortedOptions(options);
  if (opts.length === 0) {
    container.append(el('div', { class: 'empty-state' }, 'This module exposes no documented options.'));
    return;
  }
  for (const opt of opts) {
    container.append(buildField(opt, values, onChange));
  }
}

function buildField(opt, values, onChange) {
  const has = Object.prototype.hasOwnProperty.call(values, opt.name);
  const value = values[opt.name];

  const label = el('label', {},
    el('span', { class: 'fname' }, opt.name),
    opt.required ? el('span', { class: 'badge required' }, 'required') : null,
    el('span', { class: 'badge type' }, typeLabel(opt)),
    opt.default !== undefined
      ? el('span', { class: 'badge default', title: 'Default value' }, `default: ${formatDefault(opt.default)}`)
      : null,
  );

  const parts = [label];
  if (opt.description?.length) {
    parts.push(el('div', { class: 'fdesc' }, opt.description.join(' ')));
  }

  const set = (v) => onChange(opt.name, v);
  const control = buildControl(opt, has ? value : undefined, set);
  parts.push(control);

  if (has && !opt.required) {
    parts.push(el('div', { style: 'margin-top:3px' },
      el('button', { class: 'mini-btn', type: 'button', onclick: () => set(undefined) }, 'clear')));
  }

  return el('div', { class: 'field' }, ...parts);
}

function typeLabel(opt) {
  if (opt.type === 'list' && opt.elements) return `list[${opt.elements}]`;
  return opt.type ?? 'raw';
}

function buildControl(opt, value, set) {
  // choices always render as a select, regardless of declared scalar type.
  if (opt.choices?.length) return choicesControl(opt, value, set);

  switch (opt.type) {
    case 'bool':
      return boolControl(value, set);
    case 'int':
    case 'float':
      return numberControl(opt, value, set);
    case 'list':
      return listControl(opt, value, set);
    case 'dict':
      return opt.suboptions ? suboptionsControl(opt, value, set) : dictControl(value, set);
    case 'raw':
      return textareaControl(value, set);
    case 'str':
    case 'path':
    default:
      return textControl(opt, value, set);
  }
}

function boolControl(value, set) {
  const cb = el('input', { type: 'checkbox' });
  cb.checked = value === true;
  cb.addEventListener('change', () => set(cb.checked));
  return el('div', {}, cb);
}

function numberControl(opt, value, set) {
  const input = el('input', {
    type: 'number',
    step: opt.type === 'float' ? 'any' : '1',
    value: value ?? '',
  });
  input.addEventListener('change', () => {
    if (input.value === '') return set(undefined);
    const n = Number(input.value);
    if (Number.isNaN(n)) return; // keep old value; browser blocks most junk
    set(n);
  });
  return input;
}

function textControl(opt, value, set) {
  const input = el('input', {
    type: 'text',
    value: value ?? '',
    placeholder: opt.default !== undefined ? `default: ${formatDefault(opt.default)}` : '',
  });
  input.addEventListener('change', () => {
    if (input.value === '') set(undefined);
    else set(input.value);
  });
  return input;
}

function textareaControl(value, set) {
  const ta = el('textarea', {});
  ta.value = value ?? '';
  ta.addEventListener('change', () => {
    if (ta.value === '') set(undefined);
    else set(ta.value);
  });
  return ta;
}

function choicesControl(opt, value, set) {
  const select = el('select', {});
  const unset = el('option', { value: '' }, opt.required ? '— choose —' : '(unset)');
  unset.value = '__unset__';
  select.append(unset);
  for (const c of opt.choices) {
    const o = el('option', { value: String(c) }, String(c));
    o.dataset.raw = JSON.stringify(c);
    select.append(o);
  }
  if (value !== undefined) select.value = String(value);
  else select.value = '__unset__';
  select.addEventListener('change', () => {
    if (select.value === '__unset__') return set(undefined);
    const chosen = select.selectedOptions[0];
    try { set(JSON.parse(chosen.dataset.raw)); } catch { set(chosen.value); }
  });
  return select;
}

// --- List editor ---

function listControl(opt, value, set) {
  const items = Array.isArray(value) ? [...value] : [];
  const wrap = el('div', { class: 'list-editor' });

  const commit = (next) => set(next.length ? next : undefined);

  const renderItems = () => {
    wrap.replaceChildren();
    items.forEach((item, i) => {
      wrap.append(listItemRow(opt, item, (newItem) => {
        if (newItem === undefined) { items.splice(i, 1); commit(items); renderItems(); }
        else { items[i] = newItem; commit(items); }
      }));
    });
    wrap.append(el('div', { style: 'margin-top:4px' },
      el('button', {
        class: 'mini-btn', type: 'button',
        onclick: () => {
          items.push(newListItem(opt));
          commit(items);
          renderItems();
        },
      }, '+ add item')));
  };
  renderItems();
  return wrap;
}

function newListItem(opt) {
  if (opt.elements === 'dict' && opt.suboptions) {
    const obj = {};
    for (const sub of Object.values(opt.suboptions)) {
      if (sub.required) obj[sub.name] = sub.default ?? '';
    }
    return obj;
  }
  return '';
}

function listItemRow(opt, item, setItem) {
  if (opt.elements === 'dict' && opt.suboptions) {
    const obj = typeof item === 'object' && item !== null ? { ...item } : {};
    const sub = el('div', { class: 'subform' },
      el('div', { class: 'subform-title' }, 'item'),
    );
    const body = el('div', {});
    buildOptionsForm(body, opt.suboptions, obj, (key, v) => {
      if (v === undefined) delete obj[key]; else obj[key] = v;
      setItem(obj);
    });
    sub.append(body,
      el('button', { class: 'mini-btn', type: 'button', onclick: () => setItem(undefined) }, 'remove item'));
    return sub;
  }
  const input = el('input', { type: 'text', value: scalarToString(item) });
  input.addEventListener('change', () => setItem(interpretScalar(input.value)));
  return el('div', { class: 'field-row', style: 'margin-bottom:4px' },
    input,
    el('button', { class: 'mini-btn', type: 'button', onclick: () => setItem(undefined) }, '✕'));
}

function scalarToString(v) {
  if (v == null) return '';
  if (typeof v === 'object') return JSON.stringify(v);
  return String(v);
}

// --- Dict editors ---

function suboptionsControl(opt, value, set) {
  const obj = typeof value === 'object' && value !== null && !Array.isArray(value) ? { ...value } : {};
  const sub = el('div', { class: 'subform' },
    el('div', { class: 'subform-title' }, opt.name));
  const body = el('div', {});
  buildOptionsForm(body, opt.suboptions, obj, (key, v) => {
    if (v === undefined) delete obj[key]; else obj[key] = v;
    set(Object.keys(obj).length ? obj : undefined);
  });
  sub.append(body);
  return sub;
}

// kvEditor exposes the free-form dict editor for non-module uses
// (inventory host/group variables).
export function kvEditor(value, set) {
  return dictControl(value, set);
}

function dictControl(value, set) {
  const obj = typeof value === 'object' && value !== null && !Array.isArray(value) ? { ...value } : {};
  const wrap = el('div', { class: 'dict-editor' });

  const commit = () => set(Object.keys(obj).length ? obj : undefined);

  const renderRows = () => {
    wrap.replaceChildren();
    for (const [k, v] of Object.entries(obj)) {
      const keyInput = el('input', { type: 'text', value: k, placeholder: 'key' });
      const valInput = el('input', { type: 'text', value: scalarToString(v), placeholder: 'value' });
      keyInput.addEventListener('change', () => {
        const nk = keyInput.value.trim();
        if (!nk || nk === k) return;
        obj[nk] = obj[k];
        delete obj[k];
        commit(); renderRows();
      });
      valInput.addEventListener('change', () => {
        obj[k] = interpretScalar(valInput.value);
        commit();
      });
      wrap.append(el('div', { class: 'field-row', style: 'margin-bottom:4px' },
        keyInput, valInput,
        el('button', {
          class: 'mini-btn', type: 'button',
          onclick: () => { delete obj[k]; commit(); renderRows(); },
        }, '✕')));
    }
    wrap.append(el('div', { style: 'margin-top:4px' },
      el('button', {
        class: 'mini-btn', type: 'button',
        onclick: () => {
          let n = 1;
          while (`key${n}` in obj) n++;
          obj[`key${n}`] = '';
          commit(); renderRows();
        },
      }, '+ add entry')));
  };
  renderRows();
  return wrap;
}
