import { isWailsAvailable } from './src/lib/wails.js';
import { updateBadge, updateMessengerUI } from './src/components/badge.js';
import { createLogoutModal } from './src/components/modal.js';
import { initTabs, switchToTab } from './src/components/tabs.js';
import { initBulkImport, getBulkContacts, getBulkTemplate } from './src/components/import.js';
import { initRichEditors } from './src/components/editor.js';
import { initConnectionsTabs, initConnectionsCollapse, updateConnectionsFooter } from './src/components/connections.js';
import { initCascadeProgress, showOverlay } from './src/components/progress.js';
import { initHistory } from './src/components/history.js';
import { initAttachments, getAttachment } from './src/components/attachments.js';

const $ = (s) => document.querySelector(s);
const waState = $('#wa-state');
const waError = $('#wa-error');
const qrWrap = $('#qr-wrap');
const qrImg = $('#qr-img');
const qrStatus = $('#qr-status');
const btnConnect = $('#btn-connect');
const btnDisconnect = $('#btn-disconnect');

function setState(text, kind){ updateBadge(waState, text, kind); }
function showError(msg){ if(!msg){waError.classList.add('hidden');waError.textContent='';return;} waError.textContent=msg; waError.classList.remove('hidden'); }

initTabs();
initConnectionsTabs();
initConnectionsCollapse();

function updateConnectedUI(isConnected){
  updateMessengerUI({ cardId:'wa-card', notConnId:'wa-not-connected', badgeId:'wa-state', logoutId:'btn-logout', qrWrapId:'qr-wrap' }, isConnected);
}
async function refreshState(){
  if(!isWailsAvailable()){ setState('Не підключено',''); updateConnectedUI(false); updateConnectionsFooter(false, null); return; }
  try{
    const connected=await window.go.ui.App.IsWhatsAppConnected();
    const loggedIn=await window.go.ui.App.IsWhatsAppLoggedIn();
    const isFullyConnected = connected && loggedIn;
    if(isFullyConnected) setState('Підключено ✓','ok');
    else if(connected) setState("З’єднання...",'');
    else setState('Не підключено','');
    updateConnectedUI(isFullyConnected);
    updateConnectionsFooter(isFullyConnected, null);
    const st=await window.go.ui.App.GetQRStatus();
    if(st) qrStatus.textContent='Статус: '+st;
  } catch(e){ setState('Помилка','err'); showError(String(e)); updateConnectionsFooter(false, null); }
}

btnConnect.addEventListener('click', async()=>{
  if(!isWailsAvailable()){ showError('Запустіть програму через Wails (Go (Wails) desktop) щоб підключити WhatsApp'); return; }
  btnConnect.disabled=true; showError(''); setState('Підключення...','');
  try{
    const res=await window.go.ui.App.ConnectWhatsApp();
    if(res && res!=='connecting') showError(res);
    pollQR();
  }catch(e){ showError(String(e)); setState('Помилка','err'); }
  btnConnect.disabled=false;
  if(!window._refreshInterval) window._refreshInterval = setInterval(refreshState, 1500);
});
const { show: showLogoutModal } = createLogoutModal();

async function doWaLogout(){
  if(!isWailsAvailable()){ setState('Не підключено',''); updateConnectedUI(false); return; }
  const res=await window.go.ui.App.LogoutWhatsApp();
  setState('Не підключено',''); qrWrap.classList.add('hidden'); showError('');
  updateConnectedUI(false);
  const pr=document.getElementById('pair-result'); if(pr) pr.classList.add('hidden');
  const pe=document.getElementById('pair-error'); if(pe){ pe.classList.add('hidden'); pe.textContent=''; }
  if(res && res!=='logged out') showError(res);
}
function requestWaLogout(){
  showLogoutModal('Вийти з WhatsApp?', 'Сесію WhatsApp буде видалено. Потрібно буде знову сканувати QR/код.', doWaLogout);
}
btnDisconnect.addEventListener('click', requestWaLogout);
const btnLogout=document.getElementById('btn-logout');
if(btnLogout) btnLogout.addEventListener('click', requestWaLogout);
async function pollQR(){
  if(!isWailsAvailable()) return;
  try{
    const png=await window.go.ui.App.GetQRCodePNG();
    if(png){ qrImg.src=png; qrWrap.classList.remove('hidden'); }
    const st=await window.go.ui.App.GetQRStatus();
    qrStatus.textContent=st?('Статус: '+st):'Відскануйте протягом 20 сек — код оновлюється автоматично';
    const loggedIn=await window.go.ui.App.IsWhatsAppLoggedIn();
    if(!loggedIn) setTimeout(pollQR, 1200);
    else { qrWrap.classList.add('hidden'); await refreshState(); }
  }catch{}
}

// pair code
const btnPair=$('#btn-pair');
const pairPhone=$('#pair-phone');
const pairResult=$('#pair-result');
const pairCode=$('#pair-code');
const pairError=$('#pair-error');
let pairPollInterval = null;
function startPairPolling(){
  if(pairPollInterval) clearInterval(pairPollInterval);
  pairPollInterval = setInterval(async()=>{
    try{
      const loggedIn = await window.go.ui.App.IsWhatsAppLoggedIn();
      if(loggedIn){
        clearInterval(pairPollInterval); pairPollInterval=null;
        pairResult.classList.add('hidden');
        await refreshState();
      }
    }catch{}
  }, 1500);
}
btnPair.addEventListener('click', async()=>{
  const phone=pairPhone.value.trim();
  if(!phone){ pairError.textContent='Введіть номер у форматі +380'; pairError.classList.remove('hidden'); return; }
  if(!isWailsAvailable()){ pairError.textContent='Запустіть через Wails'; pairError.classList.remove('hidden'); return; }
  btnPair.disabled=true; pairError.classList.add('hidden'); pairResult.classList.add('hidden');
  const connected=await window.go.ui.App.IsWhatsAppConnected();
  if(!connected){
    await window.go.ui.App.ConnectWhatsApp();
    await new Promise(r=>setTimeout(r,2000));
    // start global refresh polling if not already
    if(!window._refreshInterval) window._refreshInterval = setInterval(refreshState, 1500);
  }
  try{
    const res=await window.go.ui.App.RequestPairCode(phone);
    console.log('RequestPairCode raw:', res, JSON.stringify(res));
    let code='', err='';
    if(Array.isArray(res)){ [code, err]=res; } else if(typeof res==='string'){ code=res; }
    else if(res && typeof res==='object'){ code=res[0]||''; err=res[1]||''; }
    else if(res==null || res===''){ err='Порожня відповідь від сервера — натисніть Підключити, зачекайте 3 сек поки зʼявиться QR, потім знову Отримати код'; }
    console.log('parsed code:', JSON.stringify(code), 'err:', JSON.stringify(err));
    if(err && err.length>0){ pairError.textContent='Помилка: '+err; pairError.classList.remove('hidden'); }
    else if(code && code.length>=4){
      pairCode.textContent=code; pairResult.classList.remove('hidden'); pairError.classList.add('hidden');
      startPairPolling();
    }
    else if(!code && !err){
      pairError.textContent='QR ще не готовий — натисніть Підключити, дочекайтесь QR (2-3с), потім знову Отримати код. Якщо тільки очистили базу — перезапустіть програму.';
      pairError.classList.remove('hidden');
    }
    else { pairError.textContent='Невідома відповідь: '+JSON.stringify(res); pairError.classList.remove('hidden'); }
  }catch(e){ console.error('RequestPairCode catch:', e); pairError.textContent='Помилка: '+e; pairError.classList.remove('hidden'); }
  btnPair.disabled=false;
});

refreshState();
if(!window._refreshInterval) window._refreshInterval = setInterval(refreshState, 1500);
if(pairPollInterval) clearInterval(pairPollInterval);

// Telegram
const tgState=$('#tg-state');
const tgError=$('#tg-error');
function setTgState(t,k){ updateBadge(tgState, t, k); }
function updateTgUI(isConn){
  updateMessengerUI({ cardId:'tg-card', notConnId:'tg-not-connected', badgeId:'tg-state', logoutId:'btn-tg-logout' }, isConn);
}
async function loadTgConfig(retries=10){
  for(let i=0;i<retries;i++){
    if(isWailsAvailable()){
      try{
        const res = await window.go.ui.App.GetTelegramConfig();
        console.log('GetTelegramConfig raw', res, JSON.stringify(res));
        let apiId='', apiHash='';
        if(res && typeof res==='object' && 'apiId' in res){ apiId=res.apiId||''; apiHash=res.apiHash||''; }
        else if(Array.isArray(res)){ [apiId, apiHash]=res; }
        else if(res && typeof res==='object'){ apiId=res[0]||''; apiHash=res[1]||''; }
        console.log('parsed TG config', apiId, apiHash ? '***' : '(empty)');
        if(apiId && apiHash){
          const idEl=$('#tg-api-id'), hashEl=$('#tg-api-hash');
          if(idEl) idEl.value=apiId;
          if(hashEl) hashEl.value=apiHash;
          const hint=document.getElementById('tg-config-hint');
          if(hint){ hint.textContent='✓ api_id/api_hash збережено — можна вводити тільки телефон'; hint.classList.remove('hidden'); }
          return;
        } else {
          console.log('TG config empty, retry', i);
        }
      }catch(e){ console.log('loadTgConfig try', i, e); }
    } else {
      console.log('loadTgConfig not ready try', i);
    }
    await new Promise(r=>setTimeout(r,700));
  }
  console.log('TG config not found after retries');
}
async function refreshTg(){
  if(!isWailsAvailable()){ setTgState('Не підключено',''); updateTgUI(false); updateConnectionsFooter(null, false); return; }
  try{
    const c=await window.go.ui.App.IsTelegramConnected();
    const l=await window.go.ui.App.IsTelegramLoggedIn();
    const isFully = c && l;
    if(isFully) setTgState('Підключено ✓','ok');
    else if(c) setTgState('З’єднання...','');
    else setTgState('Не підключено','');
    updateTgUI(isFully);
    updateConnectionsFooter(null, isFully);
    if(isFully){
      document.getElementById('tg-pwd-wrap').classList.add('hidden');
      document.getElementById('tg-pwd-hint').classList.add('hidden');
    } else {
      // check if 2FA needed while not yet logged in
      checkTgPwd();
    }
  }catch(e){ setTgState('Помилка','err'); updateConnectionsFooter(null, false); }
}
loadTgConfig();
refreshTg();
if(!window._tgInterval) window._tgInterval=setInterval(refreshTg, 2000);

// Background session healthcheck: single backend call validates WA + TG
// sessions live (detects kicked/revoked TG sessions) and updates the
// footer. Cheap enough every 10s; backend logs only on transitions.
async function refreshConnectionsStatus(){
  if(!isWailsAvailable()) return;
  try{
    const st=await window.go.ui.App.GetConnectionsStatus();
    if(!st) return;
    const waOk=!!(st.whatsapp && (st.whatsapp.ok ?? (st.whatsapp.connected && st.whatsapp.loggedIn)));
    const tgOk=!!(st.telegram && (st.telegram.ok ?? (st.telegram.connected && st.telegram.loggedIn)));
    const viberOk=!!(st.viber && st.viber.ok);
    updateConnectionsFooter(waOk, tgOk, viberOk);
    // banner on fresh session errors (e.g. TG kicked the session)
    const tgErrEl=document.getElementById('tg-error');
    const tgErr=(st.telegram && st.telegram.error) || '';
    if(tgErrEl){
      if(tgErr && !tgOk){ tgErrEl.textContent='Сесія втрачена: '+tgErr+' — перепідключи Telegram'; tgErrEl.classList.remove('hidden'); }
      else if(!tgErr){ tgErrEl.classList.add('hidden'); }
    }
  }catch{}
}
if(!window._connHealthInterval) window._connHealthInterval=setInterval(refreshConnectionsStatus, 10000);

let tgQrPoll = null;
function startTgQrPoll(){
  if(tgQrPoll) clearInterval(tgQrPoll);
  tgQrPoll = setInterval(async()=>{
    try{
      const png = await window.go.ui.App.GetTelegramQR();
      const st = await window.go.ui.App.GetTelegramQRStatus();
      const img=$('#tg-qr-img'); const wrap=$('#tg-qr-wrap'); const stEl=$('#tg-qr-status');
      if(png && img){ img.src=png; wrap.classList.remove('hidden'); }
      if(stEl) stEl.textContent = st ? 'Статус: '+st : 'Відскануйте QR телефоном';
      const logged = await window.go.ui.App.IsTelegramLoggedIn();
      if(logged){ clearInterval(tgQrPoll); tgQrPoll=null; refreshTg(); }
    }catch{}
  }, 1200);
}
$('#btn-tg-qr').addEventListener('click', async()=>{
  let apiId=$('#tg-api-id').value.trim();
  let apiHash=$('#tg-api-hash').value.trim();
  // allow empty if saved config exists — backend will use saved
  if(!isWailsAvailable()){ tgError.textContent='Запустіть через Wails'; tgError.classList.remove('hidden'); return; }
  const btn=$('#btn-tg-qr'); btn.disabled=true; tgError.classList.add('hidden'); setTgState('Генерація QR...','');
  try{
    const res=await window.go.ui.App.ConnectTelegramQR(apiId, apiHash);
    if(res && (res.includes('failed') || res.includes('required'))){ tgError.textContent=res; tgError.classList.remove('hidden'); setTgState('Помилка','err'); }
    else { setTgState('Скануйте QR',''); startTgQrPoll(); }
  }catch(e){ tgError.textContent='Помилка: '+e; tgError.classList.remove('hidden'); }
  btn.disabled=false;
});
$('#btn-tg-connect').addEventListener('click', async()=>{
  let apiId=$('#tg-api-id').value.trim();
  let apiHash=$('#tg-api-hash').value.trim();
  const phone=$('#tg-phone').value.trim();
  if(!phone){ tgError.textContent='Введіть телефон +380'; tgError.classList.remove('hidden'); return; }
  if(!isWailsAvailable()){ tgError.textContent='Запустіть через Wails'; tgError.classList.remove('hidden'); return; }
  // if already logged in, don't send new code
  try{
    const lg=await window.go.ui.App.IsTelegramLoggedIn();
    if(lg){ tgError.textContent='Вже підключено ✓ — код не потрібен'; tgError.classList.remove('hidden'); tgError.className='result small ok'; setTgState('Підключено ✓','ok'); return; }
  }catch{}
  const btn=$('#btn-tg-connect'); btn.disabled=true; tgError.classList.add('hidden'); setTgState('Підключення...','');
  try{
    const res=await window.go.ui.App.ConnectTelegram(apiId, apiHash, phone);
    if(res && res!=='code sent — check Telegram' && !res.includes('code sent')){
      tgError.textContent=res; tgError.classList.remove('hidden'); setTgState('Помилка','err');
    } else {
      switchToTab('#tg-card', 'code', true);
      document.getElementById('tg-code-wrap').classList.remove('hidden');
      try{
        const t=await window.go.ui.App.GetTelegramLastCodeType();
        console.log('TG last code type', t);
        if(t.includes('App')) tgError.textContent='Код надіслано в Telegram (відкрий Telegram → чат від Telegram, 5 цифр, не SMS)'; 
        else if(t.includes('Sms')) tgError.textContent='Код надіслано SMS на '+phone;
        else tgError.textContent='Код надіслано — перевір Telegram (5 цифр) та SMS (чекай 10с)';
        tgError.classList.remove('hidden'); tgError.className='result small ok';
      }catch{}
      setTgState('Код надіслано','');
      // also poll for auto-login in case of instant auth
      setTimeout(refreshTg, 1500);
      setTimeout(checkTgPwd, 800);
    }
  }catch(e){ tgError.textContent='Помилка: '+e; tgError.classList.remove('hidden'); }
  btn.disabled=false;
});
$('#btn-tg-code').addEventListener('click', async()=>{
  const code=$('#tg-code').value.trim();
  if(!code){ tgError.textContent='Введіть код'; tgError.classList.remove('hidden'); return; }
  const btn=$('#btn-tg-code'); btn.disabled=true;
  try{
    const res=await window.go.ui.App.ProvideTelegramCode(code);
    if(res && res.includes('Помилка')){ tgError.textContent=res; tgError.classList.remove('hidden'); }
    else {
      tgError.textContent='Код прийнято, чекаємо... (якщо 2FA — введіть пароль нижче)'; tgError.classList.remove('hidden');
      setTimeout(refreshTg, 1000);
      // poll for login or 2FA
      let tries=0;
      const iv=setInterval(async()=>{
        tries++;
        const lg=await window.go.ui.App.IsTelegramLoggedIn();
        const needPwd=await window.go.ui.App.IsTelegramPasswordNeeded().catch(()=>false);
        if(needPwd){
          document.getElementById('tg-pwd-wrap').classList.remove('hidden');
          document.getElementById('tg-pwd-hint').classList.remove('hidden');
          tgError.textContent='Потрібен хмарний пароль 2FA — введіть нижче';
          tgError.className='result small err';
          clearInterval(iv);
        }
        if(lg || tries>30){ clearInterval(iv); refreshTg(); checkTgPwd(); }
      }, 1000);
    }
  }catch(e){ tgError.textContent='Помилка: '+e; tgError.classList.remove('hidden'); }
  btn.disabled=false;
});
async function checkTgPwd(){
  try{
    const need=await window.go.ui.App.IsTelegramPasswordNeeded();
    const wrap=document.getElementById('tg-pwd-wrap');
    const hint=document.getElementById('tg-pwd-hint');
    if(need){
      wrap.classList.remove('hidden'); hint.classList.remove('hidden');
      // keep current tab, just ensure pwd visible (now outside panels)
      tgError.textContent='🔒 Потрібен хмарний пароль 2FA — введіть нижче (працює і для QR, і для коду)'; tgError.className='result small err'; tgError.classList.remove('hidden');
      const err=await window.go.ui.App.GetTelegramLastError().catch(()=> '');
      if(err && !err.includes('SESSION_PASSWORD_NEEDED') && err.length>0) { tgError.textContent='2FA: '+err; }
    }
    else { wrap.classList.add('hidden'); hint.classList.add('hidden'); }
  }catch{}
}
// poll pwd needed every 2s
if(!window._tgPwdInterval) window._tgPwdInterval=setInterval(checkTgPwd, 2000);

$('#btn-tg-pwd').addEventListener('click', async()=>{
  const pwd=$('#tg-pwd').value;
  if(!pwd){ tgError.textContent='Введіть пароль 2FA'; tgError.classList.remove('hidden'); return; }
  const btn=$('#btn-tg-pwd'); btn.disabled=true;
  try{
    const res=await window.go.ui.App.ProvideTelegramPassword(pwd);
    if(res && res.includes('Помилка') || res.includes('empty')){ tgError.textContent=res; tgError.classList.remove('hidden'); }
    else {
      tgError.textContent='Пароль прийнято, чекаємо...'; tgError.className='result small ok'; tgError.classList.remove('hidden');
      let tries=0;
      const iv=setInterval(async()=>{
        tries++;
        const lg=await window.go.ui.App.IsTelegramLoggedIn();
        if(lg || tries>20){ clearInterval(iv); refreshTg(); checkTgPwd(); if(lg){ tgError.textContent='✅ Підключено ✓ (2FA)'; } }
      }, 1000);
    }
  }catch(e){ tgError.textContent='Помилка: '+e; tgError.classList.remove('hidden'); }
  btn.disabled=false;
});
document.getElementById('btn-tg-logout').addEventListener('click', ()=>{
  showLogoutModal('Вийти з Telegram?', 'Сесію Telegram буде видалено. Потрібно буде знову ввести код/QR та пароль 2FA.', async()=>{
    await window.go.ui.App.LogoutTelegram();
    document.getElementById('tg-code-wrap').classList.add('hidden');
    document.getElementById('tg-pwd-wrap').classList.add('hidden');
    document.getElementById('tg-pwd-hint').classList.add('hidden');
    document.getElementById('tg-qr-wrap').classList.add('hidden');
    $('#tg-pwd').value='';
    if(tgQrPoll){ clearInterval(tgQrPoll); tgQrPoll=null; }
    refreshTg();
  });
});

// Bulk import + rich editors (bold/italic/link/emoji)
initBulkImport();
initRichEditors();
initCascadeProgress();
initHistory();
initAttachments();

// show log path where available
(async () => {
  if (!isWailsAvailable()) return;
  try {
    const p = await window.go.ui.App.GetLogFilePath();
    const a = document.getElementById('bulk-log-path');
    const b = document.getElementById('cascade-log-path');
    if (a) a.textContent = p;
    if (b) b.textContent = p;
  } catch {}
})();

// Bulk cascade + direct send — via overlay + async events + file logging
const btnBulkSend = document.getElementById('btn-bulk-send');
const btnBulkSendWa = document.getElementById('btn-bulk-send-wa');
const btnBulkSendTg = document.getElementById('btn-bulk-send-tg');
const bulkSendBtns = [btnBulkSend, btnBulkSendWa, btnBulkSendTg].filter(Boolean);
function setBulkBtnsDisabled(disabled) { bulkSendBtns.forEach(b => { if (b) b.disabled = disabled; }); }

async function startBulkSend(mode) {
  const contacts = getBulkContacts();
  const template = getBulkTemplate();
  const progress = document.getElementById('bulk-send-progress');
  if (!contacts || contacts.length === 0) {
    if (progress) { progress.textContent = 'Немає валідних контактів'; progress.className = 'small err'; }
    return;
  }
  if (!template.trim()) {
    if (progress) { progress.textContent = 'Введи шаблон повідомлення'; progress.className = 'small err'; }
    return;
  }
  if (!isWailsAvailable()) { if (progress) progress.textContent = 'Запусти через Wails для відправки'; return; }
  // pre-check connection per mode
  try {
    if (mode === 'whatsapp') {
      const waOk = await window.go.ui.App.IsWhatsAppLoggedIn();
      if (!waOk) { if (progress) { progress.textContent = '⚠️ Підключи WhatsApp перед відправкою в WhatsApp'; progress.className = 'small err'; } return; }
    } else if (mode === 'telegram') {
      const tgOk = await window.go.ui.App.IsTelegramLoggedIn();
      if (!tgOk) { if (progress) { progress.textContent = '⚠️ Підключи Telegram перед відправкою в Telegram'; progress.className = 'small err'; } return; }
    } else {
      const waOk = await window.go.ui.App.IsWhatsAppLoggedIn();
      const tgOk = await window.go.ui.App.IsTelegramLoggedIn();
      if (!waOk && !tgOk) {
        if (progress) { progress.textContent = '⚠️ Підключи WhatsApp або Telegram перед розсилкою'; progress.className = 'small err'; }
        return;
      }
    }
  } catch {}
  try {
    const running = await window.go.ui.App.IsCascadeRunning();
    if (running) { if (progress) { progress.textContent = 'Розсилка вже виконується'; progress.className = 'small err'; } return; }
  } catch {}
  showOverlay(contacts.length, mode);
  if (progress) { progress.textContent = `Відправляємо 0/${contacts.length}...`; progress.className = 'small'; }
  setBulkBtnsDisabled(true);
  try { await window.go.ui.App.LogApp('INFO', 'ui', `ui start batch mode=${mode} total=${contacts.length}`); } catch {}
  try {
    let errStr = '';
    const att = getAttachment();
    if (mode === 'whatsapp') errStr = await window.go.ui.App.StartWhatsAppBatch(contacts, template, att);
    else if (mode === 'telegram') errStr = await window.go.ui.App.StartTelegramBatch(contacts, template, att);
    else errStr = await window.go.ui.App.StartCascadeBatch(contacts, template, att);
    if (errStr && errStr.length > 0) {
      if (progress) { progress.textContent = 'Помилка старту: ' + errStr; progress.className = 'small err'; }
      const ov = document.getElementById('cascade-overlay');
      if (ov) ov.classList.add('hidden');
      document.body.classList.remove('sending');
      setBulkBtnsDisabled(false);
      // re-enable based on valid contacts
      const hasValid = getBulkContacts().length > 0;
      if (!hasValid) setBulkBtnsDisabled(true);
      else bulkSendBtns.forEach(b => { if (b) b.disabled = false; });
      return;
    }
    const waitDone = () => {
      const check = async () => {
        try {
          const running = await window.go.ui.App.IsCascadeRunning();
          if (!running) {
            const hasValid = getBulkContacts().length > 0;
            bulkSendBtns.forEach(b => { if (b) b.disabled = !hasValid; });
            return;
          }
        } catch {
          const hasValid = getBulkContacts().length > 0;
          bulkSendBtns.forEach(b => { if (b) b.disabled = !hasValid; });
          return;
        }
        setTimeout(check, 800);
      };
      setTimeout(check, 1000);
    };
    waitDone();
  } catch (e) {
    if (progress) { progress.textContent = 'Помилка: ' + String(e); progress.className = 'small err'; }
    const ov = document.getElementById('cascade-overlay');
    if (ov) ov.classList.add('hidden');
    document.body.classList.remove('sending');
    const hasValid = getBulkContacts().length > 0;
    bulkSendBtns.forEach(b => { if (b) b.disabled = !hasValid; });
  }
}

if (btnBulkSend) btnBulkSend.addEventListener('click', () => startBulkSend('cascade'));
if (btnBulkSendWa) btnBulkSendWa.addEventListener('click', () => startBulkSend('whatsapp'));
if (btnBulkSendTg) btnBulkSendTg.addEventListener('click', () => startBulkSend('telegram'));


