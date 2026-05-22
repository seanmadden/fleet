// fleet mobile web UI.
//
// Vanilla JS, no framework, no build step. Loads as a static asset; uses a
// bearer token stored in localStorage (prompted on first load) to call
// /api/*. Live updates come from /api/events (SSE) — pane content is
// polled every 1.1s while a detail view is open.
(function () {
  'use strict';

  // --- Auth ---
  const TOKEN_KEY = 'fleet.web.token';
  function getToken() {
    let t = localStorage.getItem(TOKEN_KEY) || '';
    if (!t) {
      t = window.prompt('fleet API token (set web.token in ~/.config/fleet/config.json):') || '';
      if (t) localStorage.setItem(TOKEN_KEY, t.trim());
    }
    return t.trim();
  }
  function clearToken() {
    localStorage.removeItem(TOKEN_KEY);
  }

  // --- HTTP helpers ---
  async function apiFetch(path, init) {
    const token = getToken();
    const headers = Object.assign({}, init && init.headers, {
      Authorization: 'Bearer ' + token,
    });
    if (init && init.body && !headers['Content-Type']) {
      headers['Content-Type'] = 'application/json';
    }
    const res = await fetch(path, Object.assign({}, init, { headers }));
    if (res.status === 401) {
      clearToken();
      throw new Error('Unauthorized — token cleared, refresh to retry');
    }
    if (!res.ok) {
      const txt = await res.text().catch(() => '');
      throw new Error(`${res.status} ${res.statusText}${txt ? ': ' + txt : ''}`);
    }
    return res;
  }
  async function apiJSON(path, init) {
    const res = await apiFetch(path, init);
    if (res.status === 204) return null;
    return res.json();
  }
  async function apiText(path, init) {
    const res = await apiFetch(path, init);
    return res.text();
  }

  // --- State ---
  let sessions = [];
  let currentId = null;
  let paneTimer = null;
  let sse = null;

  // --- DOM ---
  const $ = (id) => document.getElementById(id);
  const listView = $('list-view');
  const detailView = $('detail-view');
  const listEl = $('session-list');
  const titleEl = $('title');
  const backBtn = $('back-btn');
  const refreshBtn = $('refresh-btn');
  const newBtn = $('new-btn');
  const newDialog = $('new-dialog');
  const newConfirm = $('new-confirm');
  const paneEl = $('pane');
  const detailTitle = $('detail-title');
  const detailSub = $('detail-sub');
  const sendForm = $('sendkeys-form');
  const sendInput = $('sendkeys-input');
  const approveBtn = $('approve-btn');
  const restartBtn = $('restart-btn');
  const deleteBtn = $('delete-btn');
  const toastEl = $('toast');

  // --- Toast ---
  let toastTimer = null;
  function toast(msg, opts) {
    opts = opts || {};
    toastEl.textContent = msg;
    toastEl.classList.toggle('error', !!opts.error);
    toastEl.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { toastEl.hidden = true; }, opts.error ? 4500 : 2200);
  }

  // --- Rendering ---
  // Sessions are grouped by mainRepoPath, mirroring the TUI sidebar.
  // Worktree-backed sessions resolve to their main repo on the server
  // side (GetMainRepo) so they sit under the same header as their
  // siblings rather than appearing as separate groups.
  function groupSessions(list) {
    const groups = new Map();
    list.forEach((s) => {
      const key = s.mainRepoPath || s.projectPath || '';
      if (!groups.has(key)) {
        groups.set(key, { key, name: s.repoName || key, sessions: [] });
      }
      groups.get(key).sessions.push(s);
    });
    // Stable repo-name sort so the list doesn't jitter as sessions update.
    return Array.from(groups.values()).sort((a, b) =>
      a.name.localeCompare(b.name) || a.key.localeCompare(b.key));
  }

  function renderList() {
    if (!sessions.length) {
      // Build via DOM API rather than innerHTML — the string is static
      // today but innerHTML is a code-review trap in a file that
      // otherwise uses textContent / replaceChildren throughout.
      const empty = document.createElement('li');
      empty.className = 'empty';
      empty.textContent = 'No sessions. Tap + to create one.';
      listEl.replaceChildren(empty);
      return;
    }
    const frag = document.createDocumentFragment();
    groupSessions(sessions).forEach((group) => {
      const header = document.createElement('li');
      header.className = 'repo-header';
      // role=presentation so screen readers don't announce the header
      // as an interactive list item — only the session rows below are
      // tappable.
      header.setAttribute('role', 'presentation');
      const name = document.createElement('span');
      name.className = 'repo-header-name';
      name.textContent = group.name;
      const count = document.createElement('span');
      count.className = 'repo-header-count';
      count.textContent = String(group.sessions.length);
      header.appendChild(name);
      header.appendChild(count);
      frag.appendChild(header);

      group.sessions.forEach((s) => {
        const li = document.createElement('li');
        li.dataset.id = s.id;
        const meta = document.createElement('div');
        meta.className = 'session-meta';
        const t = document.createElement('div');
        t.className = 'session-title';
        t.textContent = s.title || '(untitled)';
        const sub = document.createElement('div');
        sub.className = 'session-sub';
        sub.textContent = s.workspaceName ? `${s.workspaceName} · ${s.projectPath}` : s.projectPath;
        meta.appendChild(t);
        meta.appendChild(sub);
        const status = document.createElement('div');
        status.className = 'session-status status-' + (s.status || 'idle');
        status.textContent = s.status || 'idle';
        li.appendChild(meta);
        li.appendChild(status);
        li.addEventListener('click', () => openDetail(s.id));
        frag.appendChild(li);
      });
    });
    listEl.replaceChildren(frag);
  }

  function findSession(id) {
    return sessions.find((s) => s.id === id);
  }

  function renderDetailMeta() {
    const s = findSession(currentId);
    if (!s) {
      detailTitle.textContent = '(unknown session)';
      detailSub.textContent = '';
      return;
    }
    detailTitle.textContent = s.title || '(untitled)';
    detailSub.textContent = `${s.status} · ${s.projectPath}`;
    titleEl.textContent = s.title || 'session';
  }

  // --- Pane polling ---
  async function fetchPane() {
    if (!currentId) return;
    try {
      const txt = await apiText(`/api/sessions/${encodeURIComponent(currentId)}/pane`);
      // textContent prevents XSS — pane content can contain raw bytes.
      paneEl.textContent = txt;
    } catch (err) {
      paneEl.textContent = 'Failed to load pane: ' + err.message;
    }
  }
  function startPanePolling() {
    stopPanePolling();
    fetchPane();
    paneTimer = setInterval(fetchPane, 1100);
  }
  function stopPanePolling() {
    if (paneTimer) {
      clearInterval(paneTimer);
      paneTimer = null;
    }
  }

  // --- Navigation ---
  function showList() {
    currentId = null;
    stopPanePolling();
    detailView.hidden = true;
    listView.hidden = false;
    backBtn.hidden = true;
    titleEl.textContent = 'fleet';
  }
  function openDetail(id) {
    currentId = id;
    listView.hidden = true;
    detailView.hidden = false;
    backBtn.hidden = false;
    renderDetailMeta();
    paneEl.textContent = 'Loading…';
    startPanePolling();
  }

  // --- Refresh ---
  async function refreshSessions() {
    try {
      sessions = await apiJSON('/api/sessions');
      renderList();
      if (currentId) renderDetailMeta();
    } catch (err) {
      toast('Refresh failed: ' + err.message, { error: true });
    }
  }

  // --- SSE ---
  // sseErrorNotified is reset every time the SSE connection successfully
  // emits an event, so each fresh disconnect surfaces exactly one toast
  // rather than spamming a new one for every onerror tick the browser
  // fires while it auto-reconnects.
  let sseErrorNotified = false;
  function connectSSE() {
    if (sse) sse.close();
    const token = getToken();
    if (!token) return;
    sseErrorNotified = false;
    sse = new EventSource('/api/events?token=' + encodeURIComponent(token));
    const onAnyEvent = () => { sseErrorNotified = false; };
    sse.addEventListener('refresh', () => { onAnyEvent(); refreshSessions(); });
    sse.addEventListener('created', () => { onAnyEvent(); refreshSessions(); });
    sse.addEventListener('deleted', () => { onAnyEvent(); refreshSessions(); });
    sse.addEventListener('updated', (e) => {
      onAnyEvent();
      try {
        const data = JSON.parse(e.data);
        if (data && data.snapshot) {
          const idx = sessions.findIndex((s) => s.id === data.sessionId);
          if (idx >= 0) {
            sessions[idx] = data.snapshot;
          } else {
            sessions.push(data.snapshot);
          }
          renderList();
          if (currentId === data.sessionId) renderDetailMeta();
        }
      } catch (_) {
        refreshSessions();
      }
    });
    sse.onerror = () => {
      // EventSource auto-reconnects; surface the gap so the user knows
      // live updates have paused. Only one toast per disconnect — the
      // browser fires onerror repeatedly during reconnect attempts.
      if (!sseErrorNotified) {
        sseErrorNotified = true;
        toast('Live updates disconnected — reconnecting…', { error: true });
      }
    };
  }

  // --- Mutation handlers ---
  async function postMutation(path, body, okMsg) {
    try {
      await apiJSON(path, { method: 'POST', body: body ? JSON.stringify(body) : undefined });
      if (okMsg) toast(okMsg);
    } catch (err) {
      toast(err.message, { error: true });
    }
  }

  // --- Events ---
  backBtn.addEventListener('click', showList);
  refreshBtn.addEventListener('click', refreshSessions);

  newBtn.addEventListener('click', () => {
    $('new-path').value = '';
    $('new-title').value = '';
    $('new-workspace').value = '';
    if (typeof newDialog.showModal === 'function') {
      newDialog.showModal();
    } else {
      // Fallback for browsers without <dialog>.
      const path = window.prompt('Path:');
      if (!path) return;
      postMutation('/api/sessions', { path, title: path }, 'Creating session…');
    }
  });
  newConfirm.addEventListener('click', (e) => {
    const path = $('new-path').value.trim();
    if (!path) {
      e.preventDefault();
      return;
    }
    postMutation('/api/sessions', {
      path,
      title: $('new-title').value.trim() || path,
      workspaceName: $('new-workspace').value.trim(),
    }, 'Creating session…');
  });

  approveBtn.addEventListener('click', () => {
    if (!currentId) return;
    postMutation(`/api/sessions/${encodeURIComponent(currentId)}/approve`, null, 'Approved');
  });
  restartBtn.addEventListener('click', () => {
    if (!currentId) return;
    if (!window.confirm('Restart this session?')) return;
    postMutation(`/api/sessions/${encodeURIComponent(currentId)}/restart`, null, 'Restarting…');
  });
  deleteBtn.addEventListener('click', () => {
    if (!currentId) return;
    if (!window.confirm('Delete this session? (5s undo only available in the TUI)')) return;
    postMutation(`/api/sessions/${encodeURIComponent(currentId)}/delete`, {}, 'Deleted');
    showList();
  });
  sendForm.addEventListener('submit', (e) => {
    e.preventDefault();
    if (!currentId) return;
    const keys = sendInput.value;
    if (!keys) return;
    postMutation(`/api/sessions/${encodeURIComponent(currentId)}/sendkeys`, { keys }, 'Sent');
    sendInput.value = '';
  });

  // --- Init ---
  refreshSessions().then(connectSSE);
  // Repaint when the tab comes back to the foreground — mobile browsers
  // suspend timers and SSE in the background.
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') {
      refreshSessions();
      connectSSE();
      if (currentId) startPanePolling();
    } else {
      stopPanePolling();
    }
  });
})();
