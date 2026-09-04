// Reusable tabs — deduplicates WA (data-tab) vs TG (data-tg-tab) handlers
// conn-tabs (data-conn-tab) handled separately in connections.js
export function initTabs() {
  document.querySelectorAll('.tab').forEach(tab => {
    if (tab.hasAttribute('data-conn-tab')) return;
    tab.addEventListener('click', () => {
      const card = tab.closest('.card, .messenger');
      if (!card) return;
      const isTg = tab.hasAttribute('data-tg-tab');
      const attr = isTg ? 'data-tg-tab' : 'data-tab';
      const prefix = isTg ? 'tg-panel-' : 'panel-';
      card.querySelectorAll('.tab:not([data-conn-tab])').forEach(t => { t.classList.remove('active'); t.setAttribute('aria-selected', 'false'); });
      // hide only inner panels, not conn-panels
      card.querySelectorAll('.panel:not(.conn-panel)').forEach(p => { p.classList.remove('active'); p.setAttribute('aria-hidden', 'true'); });
      tab.classList.add('active'); tab.setAttribute('aria-selected', 'true');
      const val = tab.getAttribute(attr);
      const panel = document.getElementById(prefix + val);
      if (panel) { panel.classList.add('active'); panel.removeAttribute('aria-hidden'); }
    });
  });
}

export function switchToTab(cardSelector, tabValue, isTg = false) {
  const card = document.querySelector(cardSelector);
  if (!card) return;
  const attr = isTg ? 'data-tg-tab' : 'data-tab';
  const prefix = isTg ? 'tg-panel-' : 'panel-';
  card.querySelectorAll('.tab').forEach(t => { t.classList.remove('active'); t.setAttribute('aria-selected', 'false'); });
  card.querySelectorAll('.panel').forEach(p => { p.classList.remove('active'); p.setAttribute('aria-hidden', 'true'); });
  const tab = card.querySelector(`[${attr}="${tabValue}"]`);
  if (tab) { tab.classList.add('active'); tab.setAttribute('aria-selected', 'true'); }
  const panel = document.getElementById(prefix + tabValue);
  if (panel) { panel.classList.add('active'); panel.removeAttribute('aria-hidden'); }
}
