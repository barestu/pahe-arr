(() => {
  const systemTheme = window.matchMedia('(prefers-color-scheme: dark)');
  let theme;
  try { theme = localStorage.getItem('pahe-theme'); } catch (_) {}
  if (theme !== 'light' && theme !== 'dark') theme = null;
  function applyTheme() {
    const dark = theme ? theme === 'dark' : systemTheme.matches;
    document.documentElement.dataset.theme = dark ? 'dark' : 'light';
    const button = document.querySelector('[data-theme-toggle]');
    if (button) {
      button.hidden = false;
      const label = button.querySelector('[data-theme-label]');
      if (label) label.textContent = dark ? 'Light mode' : 'Dark mode';
      button.setAttribute('aria-label', dark ? 'Switch to light mode' : 'Switch to dark mode');
    }
  }
  applyTheme();
  systemTheme.addEventListener('change', applyTheme);
  window.addEventListener('storage', event => {
    if (event.key !== 'pahe-theme' && event.key !== null) return;
    theme = event.newValue === 'light' || event.newValue === 'dark' ? event.newValue : null;
    applyTheme();
  });
  const one = (s, r = document) => r.querySelector(s);
  const all = (s, r = document) => Array.from(r.querySelectorAll(s));

  function initEpisodes() {
    const boxes = all('input[data-ep]');
    if (!boxes.length) return;
    const count = one('[data-count]');
    const update = () => { if (count) count.textContent = boxes.filter(b => b.checked).length; };
    let last = null;
    boxes.forEach((b, i) => b.addEventListener('click', e => {
      if (e.shiftKey && last !== null) {
        const [a, z] = [Math.min(last, i), Math.max(last, i)];
        for (let k = a; k <= z; k++) boxes[k].checked = b.checked;
      }
      last = i;
      update();
    }));
    all('[data-select]').forEach(btn => btn.addEventListener('click', () => {
      const on = btn.dataset.select === 'all';
      boxes.forEach(b => { b.checked = on; });
      update();
    }));
    update();
  }

  function initToggles() {
    all('[data-toggles]').forEach(cb => {
      const target = one(cb.dataset.toggles);
      const sync = () => { if (target) target.hidden = !cb.checked; };
      cb.addEventListener('change', sync);
      sync();
    });
    const series = one('#series');
    if (series) series.addEventListener('change', () => {
      const opt = series.selectedOptions[0];
      if (!opt || !opt.dataset.type) return;
      const v = opt.dataset.type === 'anime' ? 'absolute' : 'season';
      const radio = one(`input[name="numbering"][value="${v}"]`);
      if (radio) radio.checked = true;
    });
  }

  document.addEventListener('click', async event => {
    const button = event.target.closest('[data-key-toggle]');
    if (!button) return;
    const field = document.getElementById(button.getAttribute('aria-controls'));
    const status = one(field.id === 'generated-key' ? '#copy-status' : '#sonarrApiKey-status');
    const show = button.getAttribute('aria-pressed') === 'false';
    status.textContent = '';
    button.disabled = true;
    try {
      if (show && !field.name) {
        const response = await fetch(button.dataset.keyToggle, { method: 'POST', redirect: 'error' });
        if (!response.ok) throw new Error();
        const key = await response.text();
        if (!field.name) field.value = key;
      }
      if (!show && !field.name) field.value = field.defaultValue;
      field.type = show || field.id === 'generated-key' ? 'text' : 'password';
      button.setAttribute('aria-pressed', String(show));
      button.title = button.getAttribute('aria-label').replace('Show', show ? 'Hide' : 'Show');
    } catch {
      status.textContent = 'Could not show the key. Try again.';
    } finally {
      button.disabled = false;
    }
  });

  document.addEventListener('click', async event => {
    const button = event.target.closest('#copy-key');
    if (!button) return;
    const status = one('#copy-status');
    try {
      const response = await fetch('/settings/integration-key/copy', { method: 'POST', redirect: 'error' });
      if (!response.ok) throw new Error();
      const key = await response.text();
      try {
        await navigator.clipboard.writeText(key);
      } catch {
        const temp = document.createElement('textarea');
        temp.className = 'sr-only';
        temp.value = key;
        document.body.append(temp);
        try {
          temp.select();
          if (!document.execCommand('copy')) throw new Error();
        } finally {
          temp.remove();
          button.focus();
        }
      }
      status.textContent = 'Key copied.';
    } catch {
      status.textContent = 'Could not copy the key. Check clipboard permissions and try again.';
    }
  });

  // A saved key starts as a mask. The first edit replaces it as one value.
  document.addEventListener('beforeinput', event => {
    const field = event.target;
    if (field.id === 'sonarrApiKey' && !field.name && field.type === 'password') field.select();
  });

  document.addEventListener('input', event => {
    const field = event.target;
    if (field.id !== 'sonarrApiKey') return;
    field.name = 'sonarrApiKey';
    const toggle = one('[aria-controls="sonarrApiKey"]');
    toggle.hidden = !field.value;
    if (!field.value) {
      field.type = 'password';
      toggle.setAttribute('aria-pressed', 'false');
      toggle.title = toggle.getAttribute('aria-label');
    }
  });

  document.addEventListener('DOMContentLoaded', () => {
    applyTheme();
    const button = one('[data-theme-toggle]');
    if (button) button.addEventListener('click', () => {
      theme = document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark';
      try { localStorage.setItem('pahe-theme', theme); } catch (_) {}
      applyTheme();
    });
    initEpisodes();
    initToggles();
  });
})();
