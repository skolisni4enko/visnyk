// History table — server pagination + grouping by date (Today/Yesterday/Date)
import { isWailsAvailable } from '../lib/wails.js';
import { createLogoutModal } from './modal.js';

function el(id) { return document.getElementById(id); }

function escapeHtml(s) {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function formatDate(v) {
  if (!v) return '—';
  let d;
  try {
    if (typeof v === 'string') d = new Date(v);
    else if (v instanceof Date) d = v;
    else d = new Date(String(v));
  } catch { return String(v); }
  if (isNaN(d.getTime())) return String(v);
  if (d.getFullYear() < 1970) return '—';
  const dd = String(d.getDate()).padStart(2, '0');
  const mm = String(d.getMonth() + 1).padStart(2, '0');
  const yyyy = d.getFullYear();
  const hh = String(d.getHours()).padStart(2, '0');
  const mi = String(d.getMinutes()).padStart(2, '0');
  const ss = String(d.getSeconds()).padStart(2, '0');
  return `${dd}.${mm}.${yyyy} ${hh}:${mi}:${ss}`;
}

function formatDateOnly(v) {
  if (!v) return '';
  let d;
  try {
    if (typeof v === 'string') d = new Date(v);
    else if (v instanceof Date) d = v;
    else d = new Date(String(v));
  } catch { return ''; }
  if (isNaN(d.getTime())) return '';
  const yyyy = d.getFullYear();
  if (yyyy < 1970) return ''; // handle zero time 0001-01-01 from old failed rows
  const dd = String(d.getDate()).padStart(2, '0');
  const mm = String(d.getMonth() + 1).padStart(2, '0');
  return `${dd}.${mm}.${yyyy}`;
}

function dateLabel(sentAt) {
  const only = formatDateOnly(sentAt);
  if (!only) return 'Без дати';
  const now = new Date();
  const today = formatDateOnly(now);
  const y = new Date(now);
  y.setDate(y.getDate() - 1);
  const yesterday = formatDateOnly(y);
  if (only === today) return `Сьогодні · ${only}`;
  if (only === yesterday) return `Вчора · ${only}`;
  return only;
}

function channelBadge(ch) {
  const c = String(ch || '').toLowerCase();
  if (c === 'whatsapp') return '<span class="badge badge-valid">whatsapp</span>';
  if (c === 'telegram') return '<span class="badge badge-warn">telegram</span>';
  if (c === 'viber') return '<span class="badge" style="background:#f3e8ff;border-color:#e9d5ff;color:#6b21a8">viber</span>';
  if (c === 'none' || c === '') return '<span class="badge badge-invalid">none</span>';
  return `<span class="badge">${escapeHtml(c)}</span>`;
}

function statusBadge(st) {
  const s = String(st || '');
  const isSent = s === 'sent';
  const cls = isSent ? 'ok' : 'err';
  return `<span class="small ${cls}" style="font-weight:600">${escapeHtml(s || '—')}</span>`;
}

async function copyText(text) {
  if (!text) return false;
  // 1. Wails clipboard via Go (most reliable in WebKit2GTK)
  try {
    if (isWailsAvailable() && window.go?.ui?.App?.ClipboardSetText) {
      const res = await window.go.ui.App.ClipboardSetText(text);
      if (!res || res === '') return true;
    }
  } catch {}
  // 2. navigator.clipboard
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {}
  // 3. fallback execCommand
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.left = '-9999px';
    ta.style.top = '0';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(ta);
    if (ok) return true;
  } catch {}
  return false;
}

let toastTimer = null;
function showHistoryToast(msg) {
  const t = el('history-toast');
  if (!t) return;
  t.textContent = msg;
  t.classList.remove('hidden');
  void t.offsetWidth;
  t.classList.add('show');
  if (toastTimer) clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    t.classList.remove('show');
    setTimeout(() => t.classList.add('hidden'), 250);
  }, 1800);
}

let cascadeToastTimer = null;
function showCascadeToast(msg) {
  let t = document.getElementById('cascade-toast');
  if (!t) {
    const wrap = document.querySelector('#cascade-table')?.closest('.table-wrap');
    if (!wrap) return;
    t = document.createElement('div');
    t.id = 'cascade-toast';
    t.className = 'copy-toast hidden';
    t.setAttribute('role', 'status');
    t.setAttribute('aria-live', 'polite');
    wrap.style.position = 'relative';
    wrap.appendChild(t);
  }
  t.textContent = msg;
  t.classList.remove('hidden');
  void t.offsetWidth;
  t.classList.add('show');
  if (cascadeToastTimer) clearTimeout(cascadeToastTimer);
  cascadeToastTimer = setTimeout(() => {
    t.classList.remove('show');
    setTimeout(() => t.classList.add('hidden'), 250);
  }, 1800);
}

function normalizeHistoryEntry(e) {
  return {
    id: e.id ?? e.ID ?? 0,
    phone: e.phone ?? e.Phone ?? '',
    normalized: e.normalized ?? e.Normalized ?? '',
    name: e.name ?? e.Name ?? '',
    channel: e.channel ?? e.Channel ?? '',
    status: e.status ?? e.Status ?? '',
    error: e.error ?? e.Error ?? '',
    sentAt: e.sentAt ?? e.SentAt ?? e.sent_at ?? '',
  };
}

// pagination + filter state — server side, best practice
const PAGE_SIZE = 50;
let curPage = 1;
let totalCount = 0;
let totalPages = 1;
let filterChannel = 'all';
let filterStatus = 'all';

function updatePaginationUI() {
  const prev = el('btn-history-prev');
  const next = el('btn-history-next');
  const info = el('history-page-info');
  const totalEl = el('history-total');
  const pag = el('history-pagination');
  const badge = el('history-badge');
  if (totalEl) totalEl.textContent = String(totalCount);
  if (info) info.textContent = `${curPage} / ${Math.max(totalPages, 1)}`;
  if (prev) prev.disabled = curPage <= 1;
  if (next) next.disabled = curPage >= totalPages;
  if (pag) {
    if (totalCount === 0) pag.classList.add('hidden');
    else pag.classList.remove('hidden');
  }
  if (badge) badge.textContent = String(totalCount);
}

export function renderHistory(rawList, opts = {}) {
  const tbody = el('history-table')?.querySelector('tbody');
  const empty = el('history-empty');
  const countEl = el('history-count');
  const meta = el('history-meta');
  if (!tbody) return;

  // opts: { total, page, pageSize } — when called from paged loader, use server total; otherwise fallback
  if (opts.total != null) totalCount = opts.total;
  if (opts.page != null) curPage = opts.page;
  // totalPages from server total
  totalPages = Math.max(1, Math.ceil(totalCount / PAGE_SIZE));

  const list = (rawList || []).map(normalizeHistoryEntry);
  // server already sorted, but keep stable sort for safety
  list.sort((a, b) => {
    const da = a.sentAt ? new Date(a.sentAt).getTime() : 0;
    const db = b.sentAt ? new Date(b.sentAt).getTime() : 0;
    if (db !== da) return db - da;
    return (b.id || 0) - (a.id || 0);
  });

  tbody.innerHTML = '';
  if (list.length === 0 && totalCount === 0) {
    if (empty) empty.classList.remove('hidden');
    if (countEl) countEl.textContent = 'Всього: 0';
    if (meta) meta.textContent = '';
    updatePaginationUI();
    return;
  }
  if (empty) empty.classList.add('hidden');

  // group by date within page
  let currentDateKey = null;
  // global start index for numbering (best practice: continuous across pages)
  const globalOffset = (curPage - 1) * PAGE_SIZE;
  let renderedInGroup = 0;
  let groupStartIdx = 0;

  // precompute groups for header counts
  const groups = new Map();
  list.forEach(h => {
    const k = formatDateOnly(h.sentAt) || '—';
    groups.set(k, (groups.get(k) || 0) + 1);
  });

  list.forEach((h, idx) => {
    const dateKey = formatDateOnly(h.sentAt) || '—';
    if (dateKey !== currentDateKey) {
      currentDateKey = dateKey;
      groupStartIdx = idx;
      const label = dateLabel(h.sentAt);
      const cnt = groups.get(dateKey) || 0;
      const trHead = document.createElement('tr');
      trHead.className = 'history-date-header';
      trHead.innerHTML = `<td colspan="8">${escapeHtml(label)}<span class="count">— ${cnt} ${cnt === 1 ? 'відправлення' : 'відправлень'}</span></td>`;
      tbody.appendChild(trHead);
    }
    const phoneDisplay = h.phone || h.normalized || '';
    const normTitle = h.normalized || '';
    const name = h.name || '—';
    const tr = document.createElement('tr');
    tr.dataset.phone = phoneDisplay;
    tr.dataset.id = String(h.id);
    tr.title = 'Клік — копіювати номер';
    const globalIdx = globalOffset + idx + 1;
    tr.innerHTML = `
      <td>${globalIdx}</td>
      <td class="mono small" style="white-space:nowrap">${escapeHtml(formatDate(h.sentAt))}</td>
      <td title="${escapeHtml(name)}">${escapeHtml(name)}</td>
      <td class="mono" title="${escapeHtml(normTitle)}">${escapeHtml(phoneDisplay)}</td>
      <td>${channelBadge(h.channel)}</td>
      <td>${statusBadge(h.status)}</td>
      <td class="small" title="${escapeHtml(h.error)}" style="max-width:120px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${escapeHtml(h.error || '')}</td>
      <td style="padding:4px 6px;white-space:nowrap"><button class="btn ghost danger small btn-history-delete" data-id="${h.id}" data-phone="${escapeHtml(phoneDisplay)}" title="Видалити запис" style="padding:2px 6px;min-height:24px;line-height:1">✕</button></td>
    `;
    tbody.appendChild(tr);
  });

  if (countEl) countEl.textContent = `Сторінка ${curPage}/${totalPages} · Показано ${list.length} з ${totalCount}`;
  if (meta) {
    const sentCnt = list.filter(x => x.status === 'sent').length;
    const failCnt = list.length - sentCnt;
    meta.textContent = `sent: ${sentCnt} · fail: ${failCnt}`;
  }
  updatePaginationUI();
  // scroll history table to top on page change
  const wrap = document.querySelector('#history-overlay .history-wrap');
  if (wrap) wrap.scrollTop = 0;
  else {
    const fallback = document.querySelector('.history-wrap');
    if (fallback) fallback.scrollTop = 0;
  }
}

function getFilters() {
  return { channel: filterChannel, status: filterStatus };
}

export async function loadHistory(page = 1) {
  if (!isWailsAvailable()) {
    const tbody = el('history-table')?.querySelector('tbody');
    if (tbody && tbody.children.length === 0) {
      const empty = el('history-empty');
      if (empty) empty.classList.remove('hidden');
    }
    console.warn('loadHistory: Wails not available');
    return;
  }
  try {
    curPage = Math.max(1, page);
    const offset = (curPage - 1) * PAGE_SIZE;
    const { channel, status } = getFilters();
    const useFiltered = channel !== 'all' || status !== 'all';
    console.log(`loadHistory: page=${curPage} offset=${offset} limit=${PAGE_SIZE} channel=${channel} status=${status}`);
    let raw, total;
    if (useFiltered) {
      [raw, total] = await Promise.all([
        window.go.ui.App.GetHistoryFiltered(PAGE_SIZE, offset, channel, status),
        window.go.ui.App.GetHistoryCountFiltered(channel, status),
      ]);
    } else {
      [raw, total] = await Promise.all([
        window.go.ui.App.GetHistoryPaged(PAGE_SIZE, offset),
        window.go.ui.App.GetHistoryCount(),
      ]);
    }
    const list = Array.isArray(raw) ? raw : (raw ? [raw] : []);
    totalCount = typeof total === 'number' ? total : list.length;
    totalPages = Math.max(1, Math.ceil(totalCount / PAGE_SIZE));
    // clamp page if total shrank (e.g., after clear or filter change)
    if (curPage > totalPages) {
      curPage = totalPages;
      return loadHistory(curPage);
    }
    console.log('loadHistory: got', list.length, 'total', totalCount);
    renderHistory(list, { total: totalCount, page: curPage, pageSize: PAGE_SIZE });
  } catch (e) {
    console.error('loadHistory failed', e);
    const empty = el('history-empty');
    if (empty) { empty.textContent = 'Помилка завантаження історії: ' + String(e); empty.classList.remove('hidden'); }
  }
}

// legacy alias — for callers that still call reloadHistory() without args
export async function reloadHistory() {
  curPage = 1;
  await loadHistory(1);
}

function openHistoryOverlay() {
  const ov = el('history-overlay');
  if (!ov) return;
  ov.classList.remove('hidden');
  // ensure table height recalculated
  loadHistory(curPage);
}

function closeHistoryOverlay() {
  const ov = el('history-overlay');
  if (!ov) return;
  ov.classList.add('hidden');
}

export function initHistory() {
  // overlay open/close
  el('btn-open-history')?.addEventListener('click', openHistoryOverlay);
  el('btn-history-overlay-close')?.addEventListener('click', closeHistoryOverlay);
  el('btn-history-close')?.addEventListener('click', closeHistoryOverlay);
  el('history-backdrop')?.addEventListener('click', closeHistoryOverlay);
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      const ov = el('history-overlay');
      if (ov && !ov.classList.contains('hidden')) closeHistoryOverlay();
    }
  });

  let pendingDeleteId = null;
  const { show: showDeleteModal } = createLogoutModal({
    modalId: 'history-delete-modal',
    titleId: 'history-delete-title',
    msgId: 'history-delete-msg',
    confirmId: 'history-delete-confirm',
    cancelId: 'history-delete-cancel',
    backdropId: 'history-delete-backdrop',
  });

  const tbody = el('history-table')?.querySelector('tbody');
  if (tbody) {
    tbody.addEventListener('click', async (e) => {
      const delBtn = e.target.closest('.btn-history-delete');
      if (delBtn) {
        e.stopPropagation();
        const id = parseInt(delBtn.dataset.id, 10);
        const phone = delBtn.dataset.phone || '';
        const tr = delBtn.closest('tr');
        const dateCell = tr ? tr.children[1]?.textContent.trim() : '';
        pendingDeleteId = id;
        showDeleteModal(
          'Видалити запис?',
          `Видалити ${phone} ${dateCell ? '('+dateCell+')' : ''} ? Дію неможливо скасувати.`,
          async () => {
            const delId = pendingDeleteId;
            pendingDeleteId = null;
            if (!delId) return;
            try {
              const err = await window.go.ui.App.DeleteHistory(delId);
              if (err && err !== '') { alert('Помилка: '+err); return; }
              // if last item on page deleted and page becomes empty, go to prev page
              const willBeEmpty = tbody.querySelectorAll('tr:not(.history-date-header)').length <= 1;
              if (willBeEmpty && curPage > 1) {
                await loadHistory(curPage - 1);
              } else {
                await loadHistory(curPage);
              }
            } catch (err2) { alert(String(err2)); }
          }
        );
        // also fill dedicated spans for nicer bold (after show, because show overwrites msg)
        setTimeout(() => {
          const phoneEl = el('history-delete-phone');
          const dateEl = el('history-delete-date');
          if (phoneEl) phoneEl.textContent = phone;
          if (dateEl) dateEl.textContent = dateCell ? `(${dateCell})` : '';
        }, 0);
        return;
      }
      e.stopPropagation();
      const tr = e.target.closest('tr');
      if (!tr || !tbody.contains(tr)) return;
      if (tr.classList.contains('history-date-header')) return;
      tbody.querySelectorAll('tr.selected').forEach(r => r.classList.remove('selected'));
      tr.classList.add('selected');
      const phone = tr.dataset.phone || '';
      if (!phone) return;
      const ok = await copyText(phone);
      showHistoryToast(ok ? `Скопійовано: ${phone}` : 'Помилка копіювання');
      try { if (isWailsAvailable()) await window.go.ui.App.LogApp('INFO', 'ui', `history copy ${phone}`); } catch {}
    });
  }
  // prevent clicks inside sheet from closing overlay via backdrop bubbling
  el('history-overlay')?.querySelector('.history-sheet')?.addEventListener('click', (e) => e.stopPropagation());

  const ctbody = document.querySelector('#cascade-table tbody');
  if (ctbody) {
    ctbody.addEventListener('click', async (e) => {
      const tr = e.target.closest('tr');
      if (!tr || !ctbody.contains(tr)) return;
      ctbody.querySelectorAll('tr.selected').forEach(r => r.classList.remove('selected'));
      tr.classList.add('selected');
      const phoneCell = tr.children[2];
      const phone = phoneCell ? phoneCell.textContent.trim() : '';
      if (!phone) return;
      const ok = await copyText(phone);
      showCascadeToast(ok ? `Скопійовано: ${phone}` : 'Помилка копіювання');
    });
  }

  el('btn-history-refresh')?.addEventListener('click', () => loadHistory(curPage));
  el('btn-history-prev')?.addEventListener('click', () => {
    if (curPage > 1) loadHistory(curPage - 1);
  });
  el('btn-history-next')?.addEventListener('click', () => {
    if (curPage < totalPages) loadHistory(curPage + 1);
  });
  // filters
  const chSel = el('history-filter-channel');
  const stSel = el('history-filter-status');
  if (chSel) chSel.addEventListener('change', () => { filterChannel = chSel.value || 'all'; curPage = 1; loadHistory(1); });
  if (stSel) stSel.addEventListener('change', () => { filterStatus = stSel.value || 'all'; curPage = 1; loadHistory(1); });
  el('btn-history-filter-reset')?.addEventListener('click', () => {
    filterChannel = 'all'; filterStatus = 'all';
    if (chSel) chSel.value = 'all';
    if (stSel) stSel.value = 'all';
    curPage = 1; loadHistory(1);
  });
  // modal for clear — reuse generic modal logic
  const { show: showClearModal } = createLogoutModal({
    modalId: 'history-clear-modal',
    titleId: 'history-clear-title',
    msgId: 'history-clear-msg',
    confirmId: 'history-clear-confirm',
    cancelId: 'history-clear-cancel',
    backdropId: 'history-clear-backdrop',
  });
  // also support backdrop click on the overlay's backdrop id is same, but createLogoutModal already binds it
  el('btn-history-clear')?.addEventListener('click', async () => {
    if (!isWailsAvailable()) return;
    const countEl = el('history-clear-count');
    const msgEl = el('history-clear-msg');
    if (countEl) countEl.textContent = String(totalCount);
    // update msg with count if filtered - warn that it clears ALL, not filtered
    showClearModal(
      'Очистити історію?',
      `Буде видалено ${totalCount} записів історії (вся база локальної історії). Фільтри і пагінація ігноруються — чиститься все. Дію неможливо скасувати.`,
      async () => {
        try {
          const res = await window.go.ui.App.ClearHistoryOnly();
          if (res && res.toLowerCase().includes('error')) alert(res);
          curPage = 1;
          filterChannel = 'all'; filterStatus = 'all';
          const chSel = el('history-filter-channel'); if (chSel) chSel.value = 'all';
          const stSel = el('history-filter-status'); if (stSel) stSel.value = 'all';
          await loadHistory(1);
        } catch (e) { alert(String(e)); }
      }
    );
  });

  // sync selects with state on init
  const initCh = el('history-filter-channel'); if (initCh) initCh.value = filterChannel;
  const initSt = el('history-filter-status'); if (initSt) initSt.value = filterStatus;

  let tries = 0;
  const tryLoad = async () => {
    tries++;
    if (isWailsAvailable()) {
      // preload count for badge even when overlay closed (respect filters if not default)
      try {
        const useF = filterChannel !== 'all' || filterStatus !== 'all';
        const c = useF ? await window.go.ui.App.GetHistoryCountFiltered(filterChannel, filterStatus) : await window.go.ui.App.GetHistoryCount();
        totalCount = typeof c === 'number' ? c : 0;
        totalPages = Math.max(1, Math.ceil(totalCount / PAGE_SIZE));
        const badge = el('history-badge');
        if (badge) badge.textContent = String(totalCount);
      } catch {}
      // also preload first page in background (so overlay opens instantly)
      try { await loadHistory(1); } catch {}
      return;
    }
    if (tries < 12) setTimeout(tryLoad, 700);
  };
  tryLoad();

  window.reloadHistory = reloadHistory;
  window.loadHistory = loadHistory;
  window.renderHistory = renderHistory;
  window.openHistoryOverlay = openHistoryOverlay;
  window.closeHistoryOverlay = closeHistoryOverlay;
  // for progress.js compatibility: reloadHistory() resets to page 1
  window.loadHistoryPage = loadHistory;
}
