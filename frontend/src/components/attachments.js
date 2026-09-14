// Broadcast attachment — one file per batch, template becomes caption.
import { isWailsAvailable } from '../lib/wails.js';

let current = null; // descriptor from SaveAttachment {fileName, mime, size, path, kind}

const MAX_MEDIA = 16 * 1024 * 1024;
const MAX_DOC = 100 * 1024 * 1024;
const BLOCKED = ['.exe', '.bat', '.cmd', '.sh', '.ps1', '.js', '.jar', '.msi', '.com', '.scr', '.html', '.htm'];
const MEDIA_EXT = ['.jpg', '.jpeg', '.png', '.webp', '.gif', '.bmp', '.mp4', '.mov', '.avi', '.mkv', '.webm', '.mp3', '.ogg', '.oga', '.opus', '.m4a', '.wav', '.flac'];

function el(id) { return document.getElementById(id); }

export function humanSize(bytes) {
  if (!bytes && bytes !== 0) return '';
  if (bytes < 1024) return bytes + ' Б';
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' КБ';
  return (bytes / 1024 / 1024).toFixed(1) + ' МБ';
}

function extOf(name) {
  const i = String(name || '').lastIndexOf('.');
  return i >= 0 ? String(name).slice(i).toLowerCase() : '';
}

function clientValidate(file) {
  const ext = extOf(file.name);
  if (BLOCKED.includes(ext)) return `Тип ${ext} заборонено (виконувані/скрипти не відправляємо)`;
  if (ext === '.heic' || ext === '.heif') return 'HEIC не підтримується — збережи як jpg/png';
  const limit = MEDIA_EXT.includes(ext) ? MAX_MEDIA : MAX_DOC;
  if (file.size <= 0) return 'Порожній файл';
  if (file.size > limit) return `Завеликий: ліміт ${(limit / 1024 / 1024).toFixed(0)} МБ, файл ${humanSize(file.size)}`;
  return '';
}

function showError(msg) {
  const e = el('attach-error');
  if (!e) return;
  if (!msg) { e.classList.add('hidden'); e.textContent = ''; return; }
  e.textContent = msg;
  e.classList.remove('hidden');
}

function renderChip() {
  const chip = el('attach-chip');
  const nameEl = el('attach-name');
  const btn = el('btn-attach');
  if (!chip) return;
  if (!current) {
    chip.classList.add('hidden');
    if (btn) btn.disabled = false;
    return;
  }
  if (nameEl) nameEl.textContent = `${current.fileName} · ${humanSize(current.size)}`;
  chip.classList.remove('hidden');
  if (btn) btn.disabled = true;
}

function readAsDataURL(file) {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(r.result);
    r.onerror = () => reject(new Error('не вдалось прочитати файл'));
    r.readAsDataURL(file);
  });
}

async function handleFile(file) {
  showError('');
  if (!file) return;
  const bad = clientValidate(file);
  if (bad) { showError(bad); return; }
  const btn = el('btn-attach');
  if (btn) { btn.disabled = true; btn.textContent = 'Завантаження...'; }
  try {
    if (!isWailsAvailable()) { showError('Запусти через Wails для прикріплення'); return; }
    const dataUrl = await readAsDataURL(file);
    const res = await window.go.ui.App.SaveAttachment(file.name, String(dataUrl));
    // Wails returns struct + string: [attachment, errStr]
    const att = Array.isArray(res) ? res[0] : res;
    const errStr = Array.isArray(res) ? res[1] : '';
    if (errStr) { showError(errStr); return; }
    current = att;
    renderChip();
  } catch (e) {
    showError('Помилка: ' + String(e && e.message || e));
  } finally {
    if (btn) { btn.disabled = !!current; btn.textContent = '📎 Додати файл'; }
  }
}

async function removeCurrent() {
  if (current && current.path && isWailsAvailable()) {
    try { await window.go.ui.App.ClearAttachment(current.path); } catch {}
  }
  current = null;
  const input = el('attach-file-input');
  if (input) input.value = '';
  showError('');
  renderChip();
}

export function getAttachment() { return current; }

export function initAttachments() {
  const btn = el('btn-attach');
  const input = el('attach-file-input');
  const remove = el('btn-attach-remove');
  if (btn && input) {
    btn.addEventListener('click', () => input.click());
    input.addEventListener('change', () => {
      const f = input.files && input.files[0];
      if (f) handleFile(f);
    });
  }
  if (remove) remove.addEventListener('click', () => removeCurrent());
  renderChip();
}
