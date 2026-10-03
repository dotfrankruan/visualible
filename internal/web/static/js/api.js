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
  parseYaml: (yaml, name) => request('/api/parse', { method: 'POST', body: JSON.stringify({ yaml, name }) }),

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

  credentials: () => request('/api/credentials'),
  createCredential: (name, kind, secret) =>
    request('/api/credentials', { method: 'POST', body: JSON.stringify({ name, kind, secret }) }),
  deleteCredential: (id) =>
    request(`/api/credentials/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  deployBackend: () => request('/api/deploy/backend'),
  deployments: () => request('/api/deployments'),
  deployment: (id) => request(`/api/deployments/${encodeURIComponent(id)}`),
  createDeployment: (plan) =>
    request('/api/deployments', { method: 'POST', body: JSON.stringify(plan) }),
  cancelDeployment: (id) =>
    request(`/api/deployments/${encodeURIComponent(id)}/cancel`, { method: 'POST' }),
};
