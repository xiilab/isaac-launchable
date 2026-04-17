/**
 * Clipboard Bridge: browser <-> Kit clipboard sharing via HTTP API
 *
 * Paste (Ctrl+V): 보이지 않는 textarea로 paste 이벤트를 가로채서 Kit에 전달
 * Copy (Ctrl+C): Kit 클립보드를 가져와서 팝업으로 표시
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
      width: 100%; height: 100px; background: #2d2d2d; color: #eee;
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
    .cb-dialog .cb-cancel { background: #3c3c3c; color: #ccc; }
    .cb-dialog .cb-cancel:hover { background: #505050; }
  `;
  document.head.appendChild(style);
}

/**
 * Paste: 보이지 않는 textarea를 순간적으로 포커스해서
 * 브라우저의 native paste 이벤트를 가로챈다. 팝업 없음.
 */
function setupPasteInterceptor(): void {
  // 항상 존재하는 숨겨진 textarea
  const ghost = document.createElement('textarea');
  ghost.setAttribute('aria-hidden', 'true');
  ghost.style.cssText =
    'position:fixed;left:-9999px;top:-9999px;width:1px;height:1px;opacity:0;';
  document.body.appendChild(ghost);

  // Ctrl+V keydown → ghost에 포커스 (capture phase, 이벤트 전파 유지)
  document.addEventListener('keydown', (e: KeyboardEvent) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'v') {
      // 이미 다이얼로그 안이면 무시
      if ((e.target as HTMLElement)?.closest?.('.cb-dialog')) return;
      ghost.value = '';
      ghost.focus();
      // keydown은 전파시켜서 WebRTC도 받게 함
    }
  }, true);

  // ghost에서 paste 이벤트 발생 → Kit에 전달
  ghost.addEventListener('paste', async (e: ClipboardEvent) => {
    const text = e.clipboardData?.getData('text/plain') || '';
    if (text) {
      await copyToKit(text);
      console.info(`[clipboard-bridge] Pasted ${text.length} chars to Kit`);
    }
    // ghost 초기화
    ghost.value = '';
    ghost.blur();
  });
}

/**
 * Copy: Kit 클립보드를 가져와서 팝업으로 보여준다.
 * (async fetch가 필요하므로 팝업 방식 유지)
 */
async function showCopyDialog(): Promise<void> {
  const text = await pasteFromKit();
  if (!text) return;

  const overlay = document.createElement('div');
  overlay.className = 'cb-overlay';
  overlay.innerHTML = `
    <div class="cb-dialog">
      <h3>Copy (Ctrl+C)</h3>
      <textarea readonly></textarea>
      <div class="cb-hint">Ctrl+C로 복사한 후 닫으세요</div>
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
  setupPasteInterceptor();

  // Ctrl+C → copy dialog (capture phase)
  document.addEventListener('keydown', (e: KeyboardEvent) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'c') {
      if ((e.target as HTMLElement)?.closest?.('.cb-dialog')) return;
      e.preventDefault();
      e.stopPropagation();
      showCopyDialog();
    }
  }, true);

  console.info('[clipboard-bridge] Initialized: paste=transparent, copy=dialog');
}
