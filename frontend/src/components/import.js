// Bulk import component — paste + file → preview → cascade send
import { isWailsAvailable } from '../lib/wails.js';

let currentContacts = []; // last parsed valid contacts
let currentInvalid = [];
let showInvalid = true;

function el(id) { return document.getElementById(id); }

function normalizeResult(res) {
  // Wails may return ParseResult directly or wrapped; handle both
  if (!res) return { contacts: [], invalid: [], duplicates: 0, total: 0 };
  // Go struct JSON keys are lowercase? contacts.ParseResult uses json tags lower
  // But Wails may use PascalCase. Handle both.
  const contacts = res.contacts || res.Contacts || [];
  const invalid = res.invalid || res.Invalid || [];
  const duplicates = res.duplicates ?? res.Duplicates ?? 0;
  const total = res.total ?? res.Total ?? (contacts.length + invalid.length + duplicates);
  // Normalize contact fields too (Wails may send NormalizedPhone vs normalizedPhone)
  const normContacts = contacts.map(c => ({
    Name: c.Name || c.name || '',
    PhoneRaw: c.PhoneRaw || c.phoneRaw || c.Phone || '',
    NormalizedPhone: c.NormalizedPhone || c.normalizedPhone || c.normalized_phone || ''
  }));
  const normInvalid = invalid.map(e => ({
    Row: e.Row ?? e.row ?? 0,
    Raw: e.Raw ?? e.raw ?? '',
    Name: e.Name ?? e.name ?? '',
    Error: e.Error ?? e.error ?? ''
  }));
  return { contacts: normContacts, invalid: normInvalid, duplicates, total };
}

function renderPreview(result) {
  const wrap = el('bulk-preview-wrap');
  const tbody = el('bulk-table')?.querySelector('tbody');
  const countsEl = el('bulk-counts');
  const invalidList = el('bulk-invalid-list');
  const warning = el('bulk-warning');
  const btnSend = el('btn-bulk-send');
  if (!wrap || !tbody) return;
  const { contacts, invalid, duplicates, total } = result;
  currentContacts = contacts;
  currentInvalid = invalid;

  if (total === 0 && contacts.length === 0 && invalid.length === 0) {
    wrap.classList.add('hidden');
    if (btnSend) btnSend.disabled = true;
    return;
  }
  wrap.classList.remove('hidden');

  // counts
  const valid = contacts.length;
  const inv = invalid.length;
  countsEl.innerHTML = `
    <span class="count-chip">Всього: <b>${total}</b></span>
    <span class="count-chip ok">Валідних: <b>${valid}</b></span>
    ${inv ? `<span class="count-chip err">Невалідних: <b>${inv}</b></span>` : ''}
    ${duplicates ? `<span class="count-chip warn">Дублікатів: <b>${duplicates}</b></span>` : ''}
  `;

  // warning for >100
  if (warning) {
    if (valid > 100) {
      warning.textContent = `⚠️ Валідних ${valid} — безпечно 30–50/год. Розбий на кілька партій або відправляй з паузою 8–15с (займе ~${Math.ceil(valid * 11 / 60)} хв).`;
      warning.classList.remove('hidden');
    } else if (valid > 50) {
      warning.textContent = `⏱ ${valid} контактів займе ~${Math.ceil(valid * 11 / 60)} хв (пауза 8–15с між повідомленнями).`;
      warning.classList.remove('hidden');
    } else {
      warning.classList.add('hidden');
    }
  }

  // table rows — статус зберігає check-state, в останній колонці поруч з × додаємо поштучну перевірку
  tbody.innerHTML = '';
  contacts.forEach((c, idx) => {
    const tr = document.createElement('tr');
    tr.dataset.idx = String(idx);
    tr.innerHTML = `
      <td>${idx + 1}</td>
      <td title="${escapeHtml(c.Name)}">${escapeHtml(c.Name || '—')}</td>
      <td class="mono" title="${escapeHtml(c.PhoneRaw)}">${escapeHtml(c.PhoneRaw)}</td>
      <td class="mono">${escapeHtml(c.NormalizedPhone)}</td>
      <td data-check-cell><span class="badge badge-valid">✓ валідний</span></td>
      <td style="white-space:nowrap"><button class="btn ghost small btn-check-single" data-check-idx="${idx}" title="Перевірити цей номер в WA/TG (без спаму — 1 запит)" style="padding:2px 6px; min-height:24px; margin-right:4px">🔍</button><button class="btn ghost small" data-remove-idx="${idx}" title="Видалити" style="padding:2px 6px; min-height:24px">×</button></td>
    `;
    tbody.appendChild(tr);
  });
  if (showInvalid) {
    invalid.forEach(inv => {
      const tr = document.createElement('tr');
      tr.className = 'invalid';
      tr.innerHTML = `
        <td>${inv.Row || '—'}</td>
        <td>${escapeHtml(inv.Name || '')}</td>
        <td class="mono">${escapeHtml(inv.Raw)}</td>
        <td class="mono">—</td>
        <td><span class="badge badge-invalid">✗ ${escapeHtml(inv.Error)}</span></td>
        <td></td>
      `;
      tbody.appendChild(tr);
    });
  }
  // delegate remove + single check
  tbody.querySelectorAll('[data-remove-idx]').forEach(btn => {
    btn.addEventListener('click', () => {
      const idx = parseInt(btn.getAttribute('data-remove-idx'), 10);
      currentContacts.splice(idx, 1);
      renderPreview({ contacts: currentContacts, invalid: currentInvalid, duplicates, total: currentContacts.length + currentInvalid.length + duplicates });
    });
  });
  tbody.querySelectorAll('[data-check-idx]').forEach(btn => {
    btn.addEventListener('click', async () => {
      const idx = parseInt(btn.getAttribute('data-check-idx'), 10);
      await checkSingle(idx, btn);
    });
  });

  if (invalidList) {
    if (inv > 0 && !showInvalid) {
      invalidList.textContent = `Приховано невалідних: ${inv}. Натисни "Показати невалідні" щоб побачити.`;
      invalidList.classList.remove('hidden');
    } else {
      invalidList.classList.add('hidden');
    }
  }

  if (btnSend) btnSend.disabled = valid === 0;
  el('bulk-parse-stats').textContent = valid ? `Готово: ${valid} валідних` : '';
}

function escapeHtml(s) {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function getCurrentContacts() { return currentContacts; }
function clearPreview() {
  currentContacts = [];
  currentInvalid = [];
  const wrap = el('bulk-preview-wrap');
  if (wrap) wrap.classList.add('hidden');
  const tbody = el('bulk-table')?.querySelector('tbody');
  if (tbody) tbody.innerHTML = '';
  const counts = el('bulk-counts');
  if (counts) counts.innerHTML = '';
  const btnSend = el('btn-bulk-send');
  if (btnSend) btnSend.disabled = true;
  const stats = el('bulk-parse-stats');
  if (stats) stats.textContent = '';
}

async function parsePaste() {
  const raw = el('bulk-input')?.value || '';
  if (!raw.trim()) {
    el('bulk-parse-stats').textContent = 'Встав номери в поле вище';
    clearPreview();
    return;
  }
  if (!isWailsAvailable()) {
    el('bulk-parse-stats').textContent = 'Запусти через Wails для парсингу (або перевір go/contacts локально)';
    // fallback: simple local split for preview in browser dev
    const lines = raw.split(/[\n,;\t]+/).map(s => s.trim()).filter(Boolean);
    const fake = lines.slice(0, 100).map((p, i) => ({ Name: '', PhoneRaw: p, NormalizedPhone: p }));
    renderPreview({ contacts: fake, invalid: [], duplicates: 0, total: lines.length });
    return;
  }
  el('bulk-parse-stats').textContent = 'Розпізнаємо...';
  try {
    const res = await window.go.ui.App.ParseContactsText(raw);
    renderPreview(normalizeResult(res));
  } catch (e) {
    el('bulk-parse-stats').textContent = 'Помилка: ' + String(e);
  }
}

async function parseFile(file) {
  if (!file) return;
  const nameEl = el('bulk-file-name');
  const errEl = el('bulk-file-error');
  if (nameEl) { nameEl.textContent = `Файл: ${file.name} (${(file.size / 1024).toFixed(1)} KB)`; nameEl.classList.remove('hidden'); }
  if (errEl) errEl.classList.add('hidden');
  const stats = el('bulk-parse-stats');
  if (stats) stats.textContent = 'Читаємо файл...';

  const buf = await file.arrayBuffer();
  // encode as base64
  const bytes = new Uint8Array(buf);
  let binary = '';
  for (let i = 0; i < bytes.length; i++) binary += String.fromCharCode(bytes[i]);
  const b64 = btoa(binary);

  if (!isWailsAvailable()) {
    // browser dev fallback: try to parse as text
    try {
      const text = new TextDecoder().decode(bytes);
      const lines = text.split(/\r?\n/).slice(0, 100).map(s => s.trim()).filter(Boolean);
      renderPreview({ contacts: lines.map(p => ({ Name: '', PhoneRaw: p, NormalizedPhone: p })), invalid: [], duplicates: 0, total: lines.length });
      if (stats) stats.textContent = `Файл прочитано (dev режим, без валідації): ${lines.length}`;
    } catch {}
    return;
  }
  try {
    const res = await window.go.ui.App.ParseContactsFile(file.name, b64);
    // res is [ParseResult, errorString] due to (ParseResult,string) return
    let parsed = res;
    let errStr = '';
    if (Array.isArray(res)) { parsed = res[0]; errStr = res[1]; }
    else if (res && typeof res === 'object' && '0' in res) { parsed = res[0]; errStr = res[1]; }
    if (errStr) {
      if (errEl) { errEl.textContent = errStr; errEl.classList.remove('hidden'); }
      if (stats) stats.textContent = 'Помилка: ' + errStr;
      return;
    }
    renderPreview(normalizeResult(parsed));
    if (stats) stats.textContent = `Файл: ${file.name} — готово`;
  } catch (e) {
    if (errEl) { errEl.textContent = String(e); errEl.classList.remove('hidden'); }
    if (stats) stats.textContent = 'Помилка: ' + String(e);
  }
}

export function initBulkImport() {
  const tabBtns = document.querySelectorAll('[data-bulk-tab]');
  const pastePanel = el('bulk-panel-paste');
  const filePanel = el('bulk-panel-file');
  tabBtns.forEach(btn => {
    btn.addEventListener('click', () => {
      const tab = btn.getAttribute('data-bulk-tab');
      tabBtns.forEach(b => { b.classList.remove('active'); b.setAttribute('aria-selected', 'false'); });
      btn.classList.add('active'); btn.setAttribute('aria-selected', 'true');
      if (tab === 'paste') {
        pastePanel.classList.add('active'); pastePanel.removeAttribute('aria-hidden');
        filePanel.classList.remove('active'); filePanel.setAttribute('aria-hidden', 'true');
      } else {
        filePanel.classList.add('active'); filePanel.removeAttribute('aria-hidden');
        pastePanel.classList.remove('active'); pastePanel.setAttribute('aria-hidden', 'true');
      }
    });
  });

  el('btn-bulk-parse')?.addEventListener('click', parsePaste);
  el('btn-bulk-clear')?.addEventListener('click', () => {
    const input = el('bulk-input');
    if (input) input.value = '';
    clearPreview();
    el('bulk-parse-stats').textContent = '';
    el('bulk-file-name')?.classList.add('hidden');
  });
  // auto-parse on paste with debounce
  let debounce = null;
  el('bulk-input')?.addEventListener('input', () => {
    if (debounce) clearTimeout(debounce);
    debounce = setTimeout(() => {
      const v = el('bulk-input').value.trim();
      if (v.split(/[\n,;\t]+/).filter(Boolean).length >= 2) parsePaste();
    }, 700);
  });

  // file input
  const fileInput = el('bulk-file-input');
  fileInput?.addEventListener('change', () => {
    const f = fileInput.files?.[0];
    if (f) parseFile(f);
  });
  // dropzone
  const dz = el('bulk-dropzone');
  if (dz) {
    ['dragenter', 'dragover'].forEach(ev => dz.addEventListener(ev, e => { e.preventDefault(); dz.classList.add('dragover'); }));
    ['dragleave', 'drop'].forEach(ev => dz.addEventListener(ev, e => { e.preventDefault(); dz.classList.remove('dragover'); }));
    dz.addEventListener('drop', e => {
      const f = e.dataTransfer?.files?.[0];
      if (f) parseFile(f);
    });
    dz.addEventListener('click', () => fileInput?.click());
    dz.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); fileInput?.click(); } });
  }

  // hide/show invalid
  el('btn-hide-invalid')?.addEventListener('click', () => {
    showInvalid = !showInvalid;
    el('btn-hide-invalid').textContent = showInvalid ? 'Приховати невалідні' : 'Показати невалідні';
    renderPreview({ contacts: currentContacts, invalid: currentInvalid, duplicates: 0, total: currentContacts.length + currentInvalid.length });
  });
  el('btn-bulk-edit')?.addEventListener('click', () => {
    // put valid phones back into textarea for editing
    const lines = currentContacts.map(c => c.Name ? `${c.Name} ${c.PhoneRaw}` : c.PhoneRaw).join('\n');
    const input = el('bulk-input');
    if (input) { input.value = lines; }
    // switch to paste tab
    document.querySelector('[data-bulk-tab="paste"]')?.click();
    input?.focus();
  });
  el('btn-bulk-close')?.addEventListener('click', () => {
    const wrap = el('bulk-preview-wrap');
    if (wrap) wrap.classList.add('hidden');
    const statusEl = el('bulk-check-status');
    if (statusEl) { statusEl.classList.add('hidden'); statusEl.textContent = ''; }
  });
  el('btn-check-contacts')?.addEventListener('click', checkContacts);
}

function normalizeCheckResult(r) {
  if (!r) return null;
  return {
    onWhatsApp: r.onWhatsApp ?? r.OnWhatsApp ?? false,
    onTelegram: r.onTelegram ?? r.OnTelegram ?? false,
    onWhatsAppChecked: r.onWhatsAppChecked ?? r.OnWhatsAppChecked ?? r.onWhatsAppOK ?? r.OnWhatsAppOK ?? false,
    onTelegramChecked: r.onTelegramChecked ?? r.OnTelegramChecked ?? r.onTelegramOK ?? r.OnTelegramOK ?? false,
    error: r.error ?? r.Error ?? '',
  };
}

function formatCheckBadge(r) {
  const n = normalizeCheckResult(r) || r;
  const parts = [];
  if (n.onWhatsAppChecked) {
    parts.push(n.onWhatsApp ? '<span class="badge badge-valid" title="Є в WhatsApp">WA ✓</span>' : '<span class="badge badge-invalid" title="Нема в WhatsApp">WA ✗</span>');
  } else {
    parts.push('<span class="badge" title="WhatsApp не підключено">WA —</span>');
  }
  if (n.onTelegramChecked) {
    parts.push(n.onTelegram ? '<span class="badge badge-warn" title="Є в Telegram">TG ✓</span>' : '<span class="badge" title="Нема в Telegram">TG ✗</span>');
  } else {
    parts.push('<span class="badge" title="Telegram не підключено">TG —</span>');
  }
  let cascade = '';
  if (n.onWhatsApp) cascade = '<span class="badge badge-valid">→ WA</span>';
  else if (n.onTelegram) cascade = '<span class="badge badge-warn">→ TG</span>';
  else if (n.onWhatsAppChecked || n.onTelegramChecked) cascade = '<span class="badge badge-invalid">нема</span>';
  return `<span style="display:inline-flex;gap:4px;flex-wrap:wrap;align-items:center">${parts.join('')} ${cascade}${n.error ? ` <span class="small err" title="${escapeHtml(n.error)}">⚠</span>` : ''}</span>`;
}

async function checkSingle(idx, btn) {
  const c = currentContacts[idx];
  const statusEl = el('bulk-check-status');
  if (!c) return;
  if (!isWailsAvailable()) {
    if (statusEl) { statusEl.textContent = 'Запусти через Wails — перевірка недоступна'; statusEl.className = 'small err'; statusEl.classList.remove('hidden'); }
    return;
  }
  const tr = document.querySelector(`#bulk-table tbody tr[data-idx="${idx}"]`);
  const cell = tr?.querySelector('[data-check-cell]');
  const origBtnText = btn ? btn.textContent : '';
  if (btn) { btn.disabled = true; btn.textContent = '…'; }
  if (cell) cell.innerHTML = '<span class="small mono">перевірка...</span>';
  if (statusEl) { statusEl.textContent = `Перевіряємо ${c.NormalizedPhone}... (1 запит, без спаму)`; statusEl.className = 'small'; statusEl.classList.remove('hidden'); }
  try {
    const res = await window.go.ui.App.CheckContacts([c]);
    const list = Array.isArray(res) ? res : (res ? [res] : []);
    const r = list[0] || null;
    if (!r) {
      if (cell) cell.innerHTML = '<span class="badge badge-invalid">помилка</span>';
      if (statusEl) { statusEl.textContent = 'Немає результату — підключи WA або TG'; statusEl.className = 'small err'; }
    } else {
      if (cell) cell.innerHTML = formatCheckBadge(r);
      const n = normalizeCheckResult(r);
      if (statusEl) {
        const where = n.onWhatsApp ? 'WA' : n.onTelegram ? 'TG' : 'нема в WA/TG';
        statusEl.innerHTML = `${escapeHtml(c.NormalizedPhone)} → <b>${where}</b> · 1 перевірка (безпечно, не спам)`;
        statusEl.className = n.onWhatsApp || n.onTelegram ? 'small ok' : 'small err';
      }
      try { await window.go.ui.App.LogApp('INFO', 'check', `ui single ${c.NormalizedPhone} → ${n.onWhatsApp ? 'WA' : n.onTelegram ? 'TG' : 'none'}`); } catch {}
    }
  } catch (e) {
    if (cell) cell.innerHTML = `<span class="small err" title="${escapeHtml(String(e))}">помилка</span>`;
    if (statusEl) { statusEl.textContent = 'Помилка: ' + String(e); statusEl.className = 'small err'; }
    try { await window.go.ui.App.LogApp('WARN', 'check', `ui single ${c.NormalizedPhone} error: ${String(e)}`); } catch {}
  } finally {
    if (btn) { btn.disabled = false; btn.textContent = origBtnText || '🔍'; }
  }
}

async function checkContacts() {
  const btn = el('btn-check-contacts');
  const statusEl = el('bulk-check-status');
  if (!currentContacts || currentContacts.length === 0) {
    if (statusEl) { statusEl.textContent = 'Немає валідних контактів для перевірки'; statusEl.className = 'small err'; statusEl.classList.remove('hidden'); }
    return;
  }
  if (!isWailsAvailable()) {
    if (statusEl) { statusEl.textContent = 'Запусти через Wails — без десктопа перевірка недоступна'; statusEl.className = 'small err'; statusEl.classList.remove('hidden'); }
    return;
  }
  if (btn) { btn.disabled = true; btn.textContent = 'Перевіряємо...'; }
  if (statusEl) { statusEl.textContent = `Перевіряємо ${currentContacts.length} номерів (WA → TG)...`; statusEl.className = 'small'; statusEl.classList.remove('hidden'); }
  // optimistic: mark all rows as checking
  document.querySelectorAll('#bulk-table tbody tr[data-idx] [data-check-cell]').forEach(cell => {
    cell.innerHTML = '<span class="small mono">перевірка...</span>';
  });
  try {
    const res = await window.go.ui.App.CheckContacts(currentContacts);
    // Wails may return array directly
    const list = Array.isArray(res) ? res : (res ? [res] : []);
    if (list.length === 0) {
      if (statusEl) { statusEl.textContent = 'Немає результату — підключи WhatsApp або Telegram'; statusEl.className = 'small err'; }
    } else {
      // map by NormalizedPhone for quick update
      const byPhone = new Map();
      list.forEach(r => {
        const c = r.contact || r.Contact || {};
        const key = c.NormalizedPhone || c.normalizedPhone || c.normalized_phone || '';
        byPhone.set(key, r);
      });
      let waCnt = 0, tgCnt = 0, noneCnt = 0;
      document.querySelectorAll('#bulk-table tbody tr[data-idx]').forEach(tr => {
        const idx = parseInt(tr.dataset.idx, 10);
        const c = currentContacts[idx];
        if (!c) return;
        const r = byPhone.get(c.NormalizedPhone) || list[idx];
        if (!r) return;
        const cell = tr.querySelector('[data-check-cell]');
        if (cell) cell.innerHTML = formatCheckBadge(r);
        const onWA = r.onWhatsApp || r.OnWhatsApp;
        const onTG = r.onTelegram || r.OnTelegram;
        if (onWA) waCnt++; else if (onTG) tgCnt++; else noneCnt++;
      });
      if (statusEl) {
        statusEl.innerHTML = `Готово: <b>${waCnt}</b> WA · <b>${tgCnt}</b> TG · <b>${noneCnt}</b> нема | Всього ${list.length}`;
        statusEl.className = 'small ok';
      }
      try { await window.go.ui.App.LogApp('INFO', 'check', `ui batch done WA=${waCnt} TG=${tgCnt} none=${noneCnt} total=${list.length}`); } catch {}
    }
  } catch (e) {
    if (statusEl) { statusEl.textContent = 'Помилка перевірки: ' + String(e); statusEl.className = 'small err'; }
    try { await window.go.ui.App.LogApp('WARN', 'check', `ui batch error: ${String(e)}`); } catch {}
    // fallback: restore valid badge
    document.querySelectorAll('#bulk-table tbody tr[data-idx] [data-check-cell]').forEach(cell => {
      if (cell.textContent.includes('перевірка')) cell.innerHTML = '<span class="badge badge-valid">✓ валідний</span>';
    });
  } finally {
    if (btn) { btn.disabled = false; btn.textContent = 'Перевірити в месенджерах'; }
  }
}

export function getBulkContacts() { return currentContacts; }
export function getBulkTemplate() {
  const v = el('bulk-template')?.value || '';
  // quill empty placeholder is <p><br></p> -> treat as empty
  if (!v || v === '<p><br></p>' || v.trim() === '') return 'Вітаю! Нагадуємо про співбесіду завтра о 10:00. Чекаємо на вас!';
  return v;
}
