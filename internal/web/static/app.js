(() => {
  const one = (s, r = document) => r.querySelector(s);
  const all = (s, r = document) => Array.from(r.querySelectorAll(s));

  function parseRanges(text) {
    const out = [];
    for (const part of text.split(',')) {
      const p = part.trim();
      if (!p) continue;
      const m = p.match(/^(\d+(?:\.\d+)?)\s*(?:-\s*(\d+(?:\.\d+)?))?$/);
      if (!m) return null;
      const lo = parseFloat(m[1]);
      const hi = m[2] !== undefined ? parseFloat(m[2]) : lo;
      if (hi < lo) return null;
      out.push([lo, hi]);
    }
    return out.length ? out : null;
  }

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
    const input = one('[data-range-input]');
    const apply = () => {
      const r = parseRanges(input.value);
      if (!r) {
        input.setCustomValidity('Use episode numbers and ranges, e.g. 1-12, 15');
        input.reportValidity();
        return;
      }
      input.setCustomValidity('');
      boxes.forEach(b => {
        const n = parseFloat(b.dataset.ep);
        b.checked = r.some(([lo, hi]) => n >= lo && n <= hi);
      });
      update();
    };
    const btn = one('[data-range-apply]');
    if (btn && input) {
      btn.addEventListener('click', apply);
      input.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); apply(); } });
      input.addEventListener('input', () => input.setCustomValidity(''));
    }
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

  document.addEventListener('DOMContentLoaded', () => { initEpisodes(); initToggles(); });
})();
