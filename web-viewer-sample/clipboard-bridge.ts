/**
 * Clipboard Bridge: browser <-> Kit clipboard sharing via HTTP API
 *
 * Ctrl+V → 팝업에 텍스트 붙여넣기 → Enter → Kit에 전달
 * Ctrl+C → Kit 클립보드 팝업 표시 → Ctrl+C로 복사
 */

const CLIPBOARD_API = '/api/clipboard';

async function copyToKit(text: string): Promise<void> {
  try {
    await fetch(`${CLIPBOARD_API}/copy`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ text }),
    });
  } catch (e) {
    console.warn('[clipboard-bridge] copyToKit failed:', e);
  }
}

async function pasteFromKit(): Promise<string> {
  try {
    const res = await fetch(`${CLIPBOARD_API}/paste`);
    const data = await res.json();
    return data.text || '';
  } catch (e) {
    console.warn('[clipboard-bridge] pasteFromKit failed:', e);
    return '';
  }
}

function injectStyles(): void {
  const style = document.createElement('style');
  style.textContent = `
    .cb-overlay {
      position: fixed; top: 0; left: 0; right: 0; bottom: 0;
      background: rgba(0,0,0,0.5); z-index: 99999;
      display: flex; align-items: flex-start; justify-content: center;
      padding-top: 80px;
    }
    .cb-dialog {
      background: #1e1e1e; border: 1px solid #555; border-radius: 8px;
      padding: 16px 20px; width: 480px; color: #ccc;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      font-size: 13px; box-shadow: 0 8px 32px rgba(0,0,0,0.6);
    }
    .cb-dialog h3 { margin: 0 0 10px; font-size: 14px; color: #fff; }
    .cb-dialog textarea {
      width: 100%; height: 120px; background: #2d2d2d; color: #eee;
      border: 1px solid #555; border-radius: 4px; padding: 8px;
      font-family: monospace; font-size: 13px; resize: vertical;
      box-sizing: border-box;
    }
    .cb-dialog textarea:focus { outline: none; border-color: #007acc; }
    .cb-dialog .cb-hint { margin-top: 8px; font-size: 11px; color: #888; }
    .cb-dialog .cb-btns {
      margin-top: 12px; display: flex; justify-content: flex-end; gap: 8px;
    }
    .cb-dialog button {
      padding: 5px 14px; border-radius: 4px; border: none;
      font-size: 12px; cursor: pointer;
    }
    .cb-dialog .cb-ok { background: #007acc; color: #fff; }
    .cb-dialog .cb-ok:hover { background: #005f9e; }
    .cb-dialog .cb-cancel { background: #3c3c3c; color: #ccc; }
    .cb-dialog .cb-cancel:hover { background: #505050; }
  `;
  document.head.appendChild(style);
}

function showPasteDialog(): void {
  const overlay = document.createElement('div');
  overlay.className = 'cb-overlay';
  overlay.innerHTML = `
    <div class="cb-dialog">
      <h3>Paste (Ctrl+V)</h3>
      <textarea placeholder="Ctrl+V"></textarea>
      <div class="cb-hint">Ctrl+V 후 Enter</div>
      <div class="cb-btns">
        <button class="cb-cancel">Cancel</button>
        <button class="cb-ok">OK</button>
      </div>
    </div>
  `;
  document.body.appendChild(overlay);

  const ta = overlay.querySelector('textarea') as HTMLTextAreaElement;
  const okBtn = overlay.querySelector('.cb-ok') as HTMLButtonElement;
  const cancelBtn = overlay.querySelector('.cb-cancel') as HTMLButtonElement;
  const close = () => overlay.remove();
  const submit = async () => {
    if (ta.value) await copyToKit(ta.value);
    close();
  };

  ta.focus();
  ta.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); submit(); }
    if (e.key === 'Escape') close();
  });
  okBtn.addEventListener('click', submit);
  cancelBtn.addEventListener('click', close);
  overlay.addEventListener('click', (e) => { if (e.target === overlay) close(); });
}

async function showCopyDialog(): Promise<void> {
  const text = await pasteFromKit();
  if (!text) return;

  const overlay = document.createElement('div');
  overlay.className = 'cb-overlay';
  overlay.innerHTML = `
    <div class="cb-dialog">
      <h3>Copy (Ctrl+C)</h3>
      <textarea readonly></textarea>
      <div class="cb-hint">Ctrl+C 후 Close</div>
      <div class="cb-btns">
        <button class="cb-cancel">Close</button>
      </div>
    </div>
  `;
  document.body.appendChild(overlay);

  const ta = overlay.querySelector('textarea') as HTMLTextAreaElement;
  const cancelBtn = overlay.querySelector('.cb-cancel') as HTMLButtonElement;
  ta.value = text;
  const close = () => overlay.remove();
  ta.focus();
  ta.select();
  ta.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') close();
    if ((e.ctrlKey || e.metaKey) && e.key === 'c') setTimeout(close, 100);
  });
  cancelBtn.addEventListener('click', close);
  overlay.addEventListener('click', (e) => { if (e.target === overlay) close(); });
}

export function initClipboardBridge(): void {
  injectStyles();

  document.addEventListener('keydown', (e: KeyboardEvent) => {
    const mod = e.ctrlKey || e.metaKey;
    if (!mod) return;
    if ((e.target as HTMLElement)?.closest?.('.cb-dialog')) return;

    if (e.key === 'v') {
      e.preventDefault();
      e.stopPropagation();
      showPasteDialog();
    }
  }, true);

  console.info('[clipboard-bridge] Initialized (dialog mode)');
}
