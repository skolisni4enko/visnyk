// Connections card: верхні вкладки WA/TG/Viber + колапс + футер текст+крапка (дубль в табі і внизу)
const LS_KEY = 'visnyk:conn-collapsed';
const TAB_LS_KEY = 'visnyk:conn-tab';

export function initConnectionsTabs() {
  const card = document.getElementById('connections-card');
  if (!card) return;
  const tabs = card.querySelectorAll('.conn-tabs [data-conn-tab]');
  const panels = {
    wa: document.getElementById('conn-panel-wa'),
    tg: document.getElementById('conn-panel-tg'),
    viber: document.getElementById('conn-panel-viber'),
  };
  function switchConn(tabVal) {
    if (!tabVal || tabVal === 'viber') return; // disabled
    tabs.forEach(t => {
      const isActive = t.getAttribute('data-conn-tab') === tabVal;
      t.classList.toggle('active', isActive);
      t.setAttribute('aria-selected', isActive ? 'true' : 'false');
    });
    Object.entries(panels).forEach(([k, el]) => {
      if (!el) return;
      const isActive = k === tabVal;
      el.classList.toggle('active', isActive);
      if (isActive) el.removeAttribute('aria-hidden');
      else el.setAttribute('aria-hidden', 'true');
    });
    try { localStorage.setItem(TAB_LS_KEY, tabVal); } catch {}
  }
  tabs.forEach(tab => {
    if (tab.hasAttribute('disabled')) return;
    tab.addEventListener('click', () => {
      const v = tab.getAttribute('data-conn-tab');
      switchConn(v);
    });
  });
  // restore last tab
  let saved = null;
  try { saved = localStorage.getItem(TAB_LS_KEY); } catch {}
  if (saved && panels[saved] && saved !== 'viber') switchConn(saved);
  else switchConn('wa');
}

export function initConnectionsCollapse() {
  const card = document.getElementById('connections-card');
  let btn = document.getElementById('btn-conn-toggle');
  if (!card || !btn) return;
  function apply(collapsed) {
    card.classList.toggle('collapsed', collapsed);
    btn.setAttribute('aria-expanded', collapsed ? 'false' : 'true');
    btn.textContent = collapsed ? '▼' : '▲';
    btn.title = collapsed ? 'Розгорнути' : 'Згорнути';
  }
  let collapsed = true; // default згорнутий (вимога)
  try {
    const saved = localStorage.getItem(LS_KEY);
    if (saved !== null) collapsed = saved === '1';
  } catch {}
  apply(collapsed);
  btn.addEventListener('click', () => {
    const isCollapsed = card.classList.contains('collapsed');
    const next = !isCollapsed;
    apply(next);
    try { localStorage.setItem(LS_KEY, next ? '1' : '0'); } catch {}
  });
}

export function updateConnectionsFooter(waOk, tgOk) {
  const waDot = document.querySelector('.conn-dot.wa');
  const tgDot = document.querySelector('.conn-dot.tg');
  const waFooter = document.getElementById('conn-footer-wa');
  const tgFooter = document.getElementById('conn-footer-tg');
  if (waOk !== null && waOk !== undefined) {
    const cls = waOk ? 'ok' : 'err';
    if (waDot) { waDot.classList.remove('ok','err'); waDot.classList.add(cls); }
    if (waFooter) {
      waFooter.classList.remove('ok','err'); waFooter.classList.add(cls);
      const b = waFooter.querySelector('b'); if (b) b.textContent = waOk ? 'Підключено ✓' : 'Не підключено';
    }
  }
  if (tgOk !== null && tgOk !== undefined) {
    const cls = tgOk ? 'ok' : 'err';
    if (tgDot) { tgDot.classList.remove('ok','err'); tgDot.classList.add(cls); }
    if (tgFooter) {
      tgFooter.classList.remove('ok','err'); tgFooter.classList.add(cls);
      const b = tgFooter.querySelector('b'); if (b) b.textContent = tgOk ? 'Підключено ✓' : 'Не підключено';
    }
  }
}
