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
