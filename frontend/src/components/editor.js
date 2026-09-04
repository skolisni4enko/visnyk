// Rich editor — Quill 2 instead of custom implementation
import Quill from 'quill';
import 'quill/dist/quill.snow.css';

const EMOJIS = ['😊','😂','❤️','🔥','👍','👋','✅','⚡️','🎉','🙏','😍','🤔','👌','💪','✨','🌟','📅','📞','💬','📌','😇','🥳','🤝','👏','🙌','😎','🤗','🫶','💡','🚀','❗','❓','➡️','✔️','⭐','🔔','📢','💼','🏢','🎯'];

const instances = {};

function createQuill(container, hiddenId, initialText) {
  const toolbarOptions = [
    ['bold', 'italic', 'strike'],
    ['code', 'link'],
    ['clean'],
  ];

  const quill = new Quill(container, {
    theme: 'snow',
    placeholder: 'Вітаю! Нагадуємо про співбесіду завтра о 10:00. Чекаємо на вас! Вставте посилання https://...',
    modules: {
      toolbar: {
        container: toolbarOptions,
        handlers: {
          link: function(value) {
            const range = this.quill.getSelection(true);
            const selectedText = range && range.length > 0 ? this.quill.getText(range.index, range.length) : '';
            // if already linked, unlink
            if (value) {
              const url = prompt('Вставте посилання (https://...)', 'https://');
              if (!url) return;
              this.quill.format('link', url.trim());
              // if no selection, insert link text as url
              if (!selectedText) {
                const idx = range ? range.index : this.quill.getLength() - 1;
                this.quill.insertText(idx, url.trim(), 'link', url.trim());
                this.quill.setSelection(idx + url.trim().length, 0);
              }
            } else {
              this.quill.format('link', false);
            }
          },
        },
      },
      clipboard: { matchVisual: false },
    },
    formats: ['header','bold','italic','strike','underline','code','link','list','clean'],
  });

  // set initial content correctly via Quill API (keeps Delta in sync)
  if (initialText) {
    quill.setText(initialText);
  }

  // sync hidden textarea for backward compat with import.js getBulkTemplate()
  const hidden = document.getElementById(hiddenId);
  const sync = () => {
    if (hidden) hidden.value = quill.root.innerHTML;
  };
  quill.on('text-change', sync);
  // initial sync after setText
  sync();

  // Add emoji button + picker to toolbar
  addEmojiPicker(quill, container);

  return quill;
}

function escapeHtml(s) {
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

function addEmojiPicker(quill, container) {
  // find toolbar
  const toolbar = container.parentElement.querySelector('.ql-toolbar') || container.previousElementSibling;
  // Quill creates toolbar as previous sibling of container; locate correctly
  const qlToolbar = container.closest('.quill-wrapper')?.querySelector('.ql-toolbar') || document.querySelector(`#${container.id}`)?.parentElement?.querySelector('.ql-toolbar');
  // fallback: query global
  const tb = document.querySelector('.ql-toolbar');
  const targetToolbar = qlToolbar || tb;
  if (!targetToolbar) return;

  // create emoji button
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'ql-emoji';
  btn.innerHTML = '😊';
  btn.title = 'Смайлики';
  btn.setAttribute('aria-label', 'Смайлики');
  btn.style.fontSize = '16px';
  targetToolbar.appendChild(btn);

  // create picker
  const wrapper = container.closest('.quill-wrapper') || container.parentElement;
  wrapper.style.position = 'relative';
  const picker = document.createElement('div');
  picker.className = 'emoji-picker hidden';
  picker.style.position = 'absolute';
  picker.style.top = '42px';
  picker.style.right = '8px';
  picker.style.zIndex = '20';
  picker.innerHTML = EMOJIS.map(e => `<button type="button" class="emoji-btn" data-emoji="${e}">${e}</button>`).join('');
  wrapper.appendChild(picker);

  btn.addEventListener('click', (e) => {
    e.preventDefault();
    picker.classList.toggle('hidden');
  });

  picker.querySelectorAll('.emoji-btn').forEach(b => {
    b.addEventListener('click', (e) => {
      e.preventDefault();
      const emoji = b.getAttribute('data-emoji');
      const range = quill.getSelection(true);
      const idx = range ? range.index : quill.getLength() - 1;
      quill.insertText(idx, emoji, 'user');
      quill.setSelection(idx + emoji.length, 0);
      picker.classList.add('hidden');
      quill.focus();
    });
  });

  // close on outside click
  document.addEventListener('click', (e) => {
    if (!wrapper.contains(e.target)) picker.classList.add('hidden');
  });
}

export function initRichEditors() {
  const bulkEl = document.getElementById('bulk-quill');
  if (bulkEl && !instances['bulk']) {
    instances['bulk'] = createQuill(bulkEl, 'bulk-template', 'Вітаю! Нагадуємо про співбесіду завтра о 10:00. Чекаємо на вас!');
  }
}

export function getEditorHTML(id) {
  const hidden = document.getElementById(id);
  if (hidden && hidden.value && hidden.value !== '<p><br></p>') {
    return hidden.value;
  }
  if (id === 'bulk-template' && instances['bulk']) {
    const html = instances['bulk'].root.innerHTML;
    return html === '<p><br></p>' ? '' : html;
  }
  return hidden ? hidden.value : '';
}

export function getBulkHTML() { return getEditorHTML('bulk-template'); }
