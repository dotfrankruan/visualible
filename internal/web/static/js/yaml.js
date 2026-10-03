// Minimal YAML display: HTML escaping, per-line highlighting and line
// numbers. Highlighting tokenizes each line structurally (comment split
// outside quotes, key split, value scan) so spans never nest into each
// other. Deliberately simple — validation comes from the backend, not
// from a fragile client-side parser.

function escapeHtml(s) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

// splitComment finds a `#` outside quotes preceded by whitespace (or at
// line start) and returns [content, comment].
function splitComment(line) {
  let sq = false, dq = false;
  for (let i = 0; i < line.length; i++) {
    const c = line[i];
    if (sq) { if (c === "'") sq = false; continue; }
    if (dq) { if (c === '"') dq = false; continue; }
    if (c === "'") { sq = true; continue; }
    if (c === '"') { dq = true; continue; }
    if (c === '#' && (i === 0 || line[i - 1] === ' ' || line[i - 1] === '\t')) {
      return [line.slice(0, i), line.slice(i)];
    }
  }
  return [line, ''];
}

// highlightValue wraps quoted strings and scalar literals in the value
// part of a line. Input is raw (unescaped) text; output is escaped HTML.
function highlightValue(text) {
  let out = '';
  let i = 0;
  while (i < text.length) {
    const c = text[i];
    if (c === '"' || c === "'") {
      const end = text.indexOf(c, i + 1);
      const stop = end === -1 ? text.length : end + 1;
      out += `<span class="ys">${escapeHtml(text.slice(i, stop))}</span>`;
      i = stop;
      continue;
    }
    // Plain run until the next quote.
    const next = (() => {
      const a = text.indexOf('"', i), b = text.indexOf("'", i);
      if (a === -1) return b === -1 ? text.length : b;
      if (b === -1) return a;
      return Math.min(a, b);
    })();
    out += escapeHtml(text.slice(i, next));
    i = next;
  }
  // Scalar literals (only when the whole value is one token).
  out = out.replace(/(:\s|\s|- )(-?\d+(?:\.\d+)?|true|false|null|yes|no|~|on|off|\{\})(\s*)$/,
    '$1<span class="yn">$2</span>$3');
  return out;
}

function highlightLine(line) {
  if (line.trim() === '') return ' ';
  if (/^\s*#/.test(line)) return `<span class="yc">${escapeHtml(line)}</span>`;

  const [content, comment] = splitComment(line);
  const commentHtml = comment ? `<span class="yc">${escapeHtml(comment)}</span>` : '';

  // Leading whitespace + optional list dash.
  const leadMatch = content.match(/^(\s*)(- )?(.*)$/s);
  const [, indent, dash, rest] = leadMatch;
  let out = escapeHtml(indent);
  if (dash) out += `<span class="yd">-</span> `;

  // Key split: `key: value` or bare scalar.
  const keyMatch = rest.match(/^([A-Za-z0-9_.\-/]+)(:)([\s\S]*)$/);
  if (keyMatch) {
    out += `<span class="yk">${escapeHtml(keyMatch[1])}</span>:`;
    out += highlightValue(keyMatch[3]);
  } else {
    out += highlightValue(rest);
  }
  return out + commentHtml;
}

export function renderYaml(pre, yamlText) {
  const lines = yamlText.replace(/\n$/, '').split('\n');
  pre.innerHTML = lines
    .map((l) => `<span class="yl">${highlightLine(l)}</span>`)
    .join('');
}
