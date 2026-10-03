// Thin typed-ish API client for the Visualible backend.

async function request(path, opts = {}) {
  const resp = await fetch(path, {
    headers: { 'Content-Type': 'application/json' },
    ...opts,
  });
  const body = await resp.json().catch(() => null);
  if (!resp.ok) {
    const err = new Error(body?.error?.message ?? `HTTP ${resp.status}`);
    err.status = resp.status;
    err.code = body?.error?.code ?? 'unknown';
    err.problems = body?.error?.problems ?? [];
    throw err;
  }
  return body;
}

export const api = {
  health: () => request('/api/health'),
  modules: (refresh = false) => request(`/api/modules${refresh ? '?refresh=1' : ''}`),
  moduleDoc: (fqcn) => request(`/api/modules/${encodeURIComponent(fqcn)}`),
  render: (playbook) => request('/api/render', { method: 'POST', body: JSON.stringify({ playbook }) }),

  projects: () => request('/api/projects'),
  createProject: (name, description) =>
    request('/api/projects', { method: 'POST', body: JSON.stringify({ name, description }) }),
  project: (id) => request(`/api/projects/${encodeURIComponent(id)}`),
  saveProject: (project) =>
    request(`/api/projects/${encodeURIComponent(project.id)}`, {
      method: 'PUT',
      body: JSON.stringify(project),
    }),
  deleteProject: (id) =>
    request(`/api/projects/${encodeURIComponent(id)}`, { method: 'DELETE' }),
};
