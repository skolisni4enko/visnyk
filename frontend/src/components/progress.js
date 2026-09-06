// Cascade progress overlay — blocking UI during SendBatchWithProgress
let total = 0;
let done = 0;
let isRunning = false;

function el(id) { return document.getElementById(id); }

function fmtETA(sec) {
  if (!sec || sec <= 0) return '';
  if (sec < 60) return `~${sec}с`;
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  if (s === 0) return `~${m}хв`;
  return `~${m}хв ${s}с`;
}

export function initCascadeProgress() {
  const overlay = el('cascade-overlay');
  const btnCancel = el('btn-cascade-cancel');
  const btnClose = el('btn-cascade-close');
  const btnLogs = el('btn-open-logs');
  const btnFolder = el('btn-open-folder');
  const btnLogs2 = el('btn-bulk-open-logs');
  const btnFolder2 = el('btn-bulk-open-folder');

  async function openLogs() {
    if (!window.go || !window.go.ui || !window.go.ui.App) {
      alert('Запусти через Wails для відкриття логів');
      return;
    }
    try {
      const err = await window.go.ui.App.OpenLogsFile();
      if (err) {
        const p = await window.go.ui.App.GetLogFilePath();
        alert(err + '\n' + p);
      }
    } catch (e) { alert(String(e)); }
  }
  async function openFolder() {
    if (!window.go || !window.go.ui || !window.go.ui.App) {
      alert('Запусти через Wails');
      return;
    }
    try {
      const err = await window.go.ui.App.OpenDataDir();
      if (err) alert(err);
    } catch (e) { alert(String(e)); }
  }

  if (btnLogs) btnLogs.addEventListener('click', openLogs);
  if (btnFolder) btnFolder.addEventListener('click', openFolder);
  if (btnLogs2) btnLogs2.addEventListener('click', openLogs);
  if (btnFolder2) btnFolder2.addEventListener('click', openFolder);

  if (btnCancel) btnCancel.addEventListener('click', async () => {
    if (!isRunning) return;
    btnCancel.disabled = true;
    btnCancel.textContent = 'Скасування...';
    try {
      const res = await window.go.ui.App.CancelCascadeBatch();
      console.log('CancelCascadeBatch', res);
      const statusEl = el('cascade-status');
      if (statusEl) statusEl.textContent = 'Скасування...';
    } catch (e) { console.error(e); }
  });

  if (btnClose) btnClose.addEventListener('click', () => {
    hideOverlay();
  });

  // prevent ESC from closing during running; allow after done
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && isRunning) {
      e.preventDefault();
      e.stopPropagation();
    }
  });

  // Wails events
  const tryBindEvents = () => {
    try {
      if (window.runtime && window.runtime.EventsOn) {
        window.runtime.EventsOn('cascade:progress', handleProgress);
        window.runtime.EventsOn('cascade:done', handleDone);
        window.runtime.EventsOn('cascade:start', (data) => {
          if (data && data.total) {
            // start already shown via showOverlay, but ensure total
            total = data.total;
          }
        });
        console.log('[progress] bound to window.runtime.EventsOn');
        return true;
      }
      if (window.wails && window.wails.EventsOn) {
        window.wails.EventsOn('cascade:progress', handleProgress);
        window.wails.EventsOn('cascade:done', handleDone);
        return true;
      }
    } catch (e) { console.log('bind events failed', e); }
    return false;
  };
  // try now and after Wails ready
  if (!tryBindEvents()) {
    let tries = 0;
    const iv = setInterval(() => {
      tries++;
      if (tryBindEvents() || tries > 20) clearInterval(iv);
    }, 500);
  }
}

function handleProgress(p) {
  // p: {index,total,contact:{Name,PhoneRaw,NormalizedPhone},channel,status,error,etaSeconds}
  // status: checking | sent | failed
  if (!p) return;
  total = p.total || total;
  const idx = p.index || 0;
  const status = p.status || '';
  const contact = p.contact || {};
  // show as entered (PhoneRaw) like in import preview, fallback to normalized
  const phone = contact.PhoneRaw || contact.phoneRaw || contact.Phone || contact.NormalizedPhone || contact.normalizedPhone || '';
  const name = contact.Name || contact.name || '';
  const channel = p.channel || '';
  const err = p.error || p.Error || '';
  const eta = p.etaSeconds || p.ETASeconds || 0;

  if (status === 'checking') {
    updateHeader(idx, total, eta);
    const cur = el('cascade-current');
    if (cur) cur.textContent = `Перевіряємо ${phone} ${name ? '('+name+')' : ''}...`;
    return;
  }
  // done for this contact
  done = Math.max(done, idx);
  updateHeader(done, total, eta);
  const cur = el('cascade-current');
  if (cur) cur.textContent = `${phone} → ${channel || '—'} ${status}${err ? ' · ' + err : ''}`;
  updateBar(done, total);
  appendRow(idx, name, phone, channel, status, err);
}

let currentMode = 'cascade';
function modeTitle(mode) {
  if (mode === 'whatsapp' || mode === 'whatsapp-direct') return 'Розсилка — тільки WhatsApp';
  if (mode === 'telegram' || mode === 'telegram-direct') return 'Розсилка — тільки Telegram';
  return 'Розсилка — у всі месенджери';
}
function modeChannelLabel(ch) {
  if (ch === 'whatsapp') return 'WhatsApp';
  if (ch === 'telegram') return 'Telegram';
  return ch || '';
}

function handleDone(data) {
  // data: {results, cancelled, total, channel, mode}
  isRunning = false;
  const results = (data && (data.results || data.Results)) || [];
  const cancelled = data && (data.cancelled || data.Cancelled);
  const ch = data && (data.channel || data.Channel) || '';
  const mode = (data && (data.mode || data.Mode)) || currentMode;
  const prog = el('cascade-status');
  const bar = el('cascade-bar-fill');
  const btnCancel = el('btn-cascade-cancel');
  const btnClose = el('btn-cascade-close');
  const titleEl = el('cascade-title');
  if (titleEl) titleEl.textContent = modeTitle(mode || ch);
  if (prog) {
    if (cancelled) prog.textContent = `Скасовано: ${results.length}/${total}`;
    else {
      let failed = 0;
      results.forEach(r => { const st = r.Status || r.status || ''; if (st !== 'sent') failed++; });
      if (failed > 0) prog.textContent = `Готово: ${results.length}/${total} — ${failed} не доставлено (нема в ${modeChannelLabel(ch) || 'месенджері'})`;
      else prog.textContent = `Готово: ${results.length}/${total}`;
    }
  }
  if (bar) bar.style.width = '100%';
  if (btnCancel) { btnCancel.classList.add('hidden'); btnCancel.disabled = false; btnCancel.textContent = 'Скасувати'; }
  if (btnClose) btnClose.classList.remove('hidden');
  document.body.classList.remove('sending');
  const bulkProgress = el('bulk-send-progress');
  if (bulkProgress) {
    if (cancelled) { bulkProgress.textContent = `Скасовано: ${results.length}/${total}`; bulkProgress.className = 'small err'; }
    else {
      let failed = 0;
      results.forEach(r => { const st = r.Status || r.status || ''; if (st !== 'sent') failed++; });
      if (failed > 0) { bulkProgress.textContent = `Готово: ${results.length}/${total} — ${failed} не доставлено`; bulkProgress.className = 'small err'; }
      else { bulkProgress.textContent = `Готово: ${results.length}/${total}`; bulkProgress.className = 'small ok'; }
    }
  }
  // ensure all results rendered (in case progress events missed)
  if (results.length > 0) {
    // if table empty, render all
    const tbody = el('cascade-table')?.querySelector('tbody');
    if (tbody && tbody.children.length === 0) {
      results.forEach((r, i) => {
        const c = r.Contact || r.contact || {};
        const ch = r.Channel || r.channel || '';
        const st = r.Status || r.status || '';
        const er = r.Error || r.error || '';
        const ph = c.PhoneRaw || c.phoneRaw || c.Phone || c.NormalizedPhone || c.normalizedPhone || '';
        const nm = c.Name || c.name || '';
        appendRow(i+1, nm, ph, ch, st, er);
      });
    }
  }
  // refresh main history table (server pagination, reset to page 1)
  const doReload = () => {
    try {
      if (window.reloadHistory) {
        console.log('handleDone: calling reloadHistory');
        window.reloadHistory();
      } else if (window.go?.ui?.App?.GetHistoryPaged) {
        Promise.all([window.go.ui.App.GetHistoryPaged(50, 0), window.go.ui.App.GetHistoryCount()]).then(([list, total]) => {
          if (window.renderHistory) window.renderHistory(Array.isArray(list) ? list : [], { total, page: 1, pageSize: 50 });
        }).catch(e=>console.error('fallback reload failed', e));
      }
    } catch (e) { console.error('reloadHistory failed', e); }
  };
  doReload();
  setTimeout(doReload, 600);
}

function updateHeader(idx, tot, eta) {
  total = tot || total;
  const statusEl = el('cascade-status');
  const etaEl = el('cascade-eta');
  if (statusEl) statusEl.textContent = `Відправляємо ${idx}/${total}`;
  if (etaEl) etaEl.textContent = fmtETA(eta);
  updateBar(idx, total);
}

function updateBar(idx, tot) {
  const fill = el('cascade-bar-fill');
  const pctEl = el('cascade-pct');
  const pct = tot > 0 ? Math.round((idx / tot) * 100) : 0;
  if (fill) fill.style.width = pct + '%';
  if (pctEl) pctEl.textContent = pct + '%';
}

function channelBadgeHtml(ch) {
  const raw = String(ch || 'none').toLowerCase();
  if (!raw || raw === 'none') return '<span class="badge badge-invalid">none</span>';
  const parts = raw.split(',').map(s => s.trim()).filter(Boolean);
  if (parts.length > 1) {
    return parts.map(p => {
      if (p === 'whatsapp') return '<span class="badge badge-valid">whatsapp</span>';
      if (p === 'telegram') return '<span class="badge badge-warn">telegram</span>';
      if (p === 'viber') return '<span class="badge" style="background:#f3e8ff;border-color:#e9d5ff;color:#6b21a8">viber</span>';
      return `<span class="badge">${escapeHtml(p)}</span>`;
    }).join(' ') + ' <span class="small ok" style="font-weight:700" title="всі доступні">✓ всі</span>';
  }
  if (raw === 'whatsapp') return '<span class="badge badge-valid">whatsapp</span>';
  if (raw === 'telegram') return '<span class="badge badge-warn">telegram</span>';
  if (raw === 'viber') return '<span class="badge" style="background:#f3e8ff;border-color:#e9d5ff;color:#6b21a8">viber</span>';
  return `<span class="badge">${escapeHtml(raw)}</span>`;
}

function appendRow(idx, name, phone, channel, status, err) {
  const tbody = el('cascade-table')?.querySelector('tbody');
  if (!tbody) return;
  const tr = document.createElement('tr');
  const isSent = status === 'sent';
  tr.innerHTML = `
    <td>${idx}</td>
    <td title="${escapeHtml(name)}">${escapeHtml(name || '—')}</td>
    <td class="mono">${escapeHtml(phone)}</td>
    <td>${channelBadgeHtml(channel)}</td>
    <td><span class="small ${isSent ? 'ok' : 'err'}" style="font-weight:600">${escapeHtml(status)}</span></td>
    <td class="small" title="${escapeHtml(err)}">${escapeHtml(err || '')}</td>
  `;
  tbody.appendChild(tr);
  // auto scroll
  const wrap = tbody.closest('.table-wrap');
  if (wrap) wrap.scrollTop = wrap.scrollHeight;
}

export function showOverlay(tot, mode = 'cascade') {
  total = tot;
  done = 0;
  isRunning = true;
  currentMode = mode;
  const overlay = el('cascade-overlay');
  const tbody = el('cascade-table')?.querySelector('tbody');
  if (tbody) tbody.innerHTML = '';
  const titleEl = el('cascade-title');
  const statusEl = el('cascade-status');
  const etaEl = el('cascade-eta');
  const pctEl = el('cascade-pct');
  const bar = el('cascade-bar-fill');
  const cur = el('cascade-current');
  const btnCancel = el('btn-cascade-cancel');
  const btnClose = el('btn-cascade-close');
  if (titleEl) titleEl.textContent = modeTitle(mode);
  if (statusEl) statusEl.textContent = `Відправляємо 0/${tot}`;
  if (etaEl) etaEl.textContent = fmtETA(tot * 11);
  if (pctEl) pctEl.textContent = '0%';
  if (bar) bar.style.width = '0%';
  if (cur) cur.textContent = 'Підготовка...';
  if (btnCancel) { btnCancel.classList.remove('hidden'); btnCancel.disabled = false; btnCancel.textContent = 'Скасувати'; }
  if (btnClose) btnClose.classList.add('hidden');
  if (overlay) overlay.classList.remove('hidden');
  document.body.classList.add('sending');
  document.getElementById('app')?.setAttribute('aria-hidden', 'true');
}

export function hideOverlay() {
  const overlay = el('cascade-overlay');
  if (overlay) overlay.classList.add('hidden');
  document.body.classList.remove('sending');
  document.getElementById('app')?.removeAttribute('aria-hidden');
  isRunning = false;
}

export function isCascadeRunning() { return isRunning; }

function escapeHtml(s) {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}
