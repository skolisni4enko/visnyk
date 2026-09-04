// Reusable badge/messenger UI — deduplicates updateConnectedUI vs updateTgUI
export function updateBadge(el, text, kind) {
  if (!el) return;
  el.textContent = text;
  el.className = 'badge' + (kind ? ' ' + kind : '');
}

// Generic messenger card toggle. Replaces 2x duplicated updateConnectedUI/updateTgUI
export function updateMessengerUI({ cardId, notConnId, badgeId, logoutId, qrWrapId }, isConnected) {
  const card = document.getElementById(cardId);
  const notConn = document.getElementById(notConnId);
  const badge = document.getElementById(badgeId);
  const btnLogout = document.getElementById(logoutId);
  const qrWrap = qrWrapId ? document.getElementById(qrWrapId) : null;
  if (isConnected) {
    if (card) card.classList.add('connected-mode');
    if (notConn) notConn.classList.add('hidden');
    if (qrWrap) qrWrap.classList.add('hidden');
    if (badge) badge.classList.add('hidden');
    if (btnLogout) btnLogout.classList.remove('hidden');
  } else {
    if (card) card.classList.remove('connected-mode');
    if (notConn) notConn.classList.remove('hidden');
    if (badge) badge.classList.remove('hidden');
    if (btnLogout) btnLogout.classList.add('hidden');
  }
}

export function setStateFactory(badgeEl) {
  return (text, kind) => updateBadge(badgeEl, text, kind);
}
