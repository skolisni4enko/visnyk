// Reusable modal — deduplicates confirm() for all messengers
export function createLogoutModal({ modalId = 'logout-modal', titleId = 'modal-title', msgId = 'modal-msg', confirmId = 'modal-confirm', cancelId = 'modal-cancel', backdropId = 'modal-backdrop' } = {}) {
  const modal = document.getElementById(modalId);
  const titleEl = document.getElementById(titleId);
  const msgEl = document.getElementById(msgId);
  const confirmBtn = document.getElementById(confirmId);
  const cancelBtn = document.getElementById(cancelId);
  const backdrop = document.getElementById(backdropId);
  let pending = null;

  function show(title, msg, onConfirm) {
    if (!modal) { if (confirm(msg)) onConfirm(); return; }
    titleEl.textContent = title;
    msgEl.textContent = msg;
    pending = onConfirm;
    modal.classList.remove('hidden');
  }
  function hide() {
    if (modal) modal.classList.add('hidden');
    pending = null;
  }
  if (cancelBtn) cancelBtn.addEventListener('click', hide);
  if (backdrop) backdrop.addEventListener('click', hide);
  if (confirmBtn) confirmBtn.addEventListener('click', async () => { const cb = pending; hide(); if (cb) await cb(); });
  document.addEventListener('keydown', e => { if (e.key === 'Escape' && modal && !modal.classList.contains('hidden')) hide(); });
  return { show, hide };
}
