/**
 * Clipboard Bridge: browser <-> Kit clipboard sharing via HTTP API
 *
 * Paste (Ctrl+V):
 *   1. 실제 Ctrl+V 가로채서 WebRTC에 안 보냄 (stopImmediatePropagation)
 *   2. 숨겨진 textarea에 paste 이벤트 발생시켜 클립보드 텍스트 획득
 *   3. Kit clipboard API로 텍스트 전달
 *   4. 합성 Ctrl+V를 dispatch해서 WebRTC → Kit paste 트리거
 *
 * Copy (Ctrl+C): Kit 클립보드를 가져와서 팝업으로 표시
 */

const CLIPBOARD_API = '/api/clipboard';
let skipNextCtrlV = false;

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
    .cb-toast {
      position: fixed; top: 16px; left: 50%; transform: translateX(-50%);
      background: #007acc; color: #fff; padding: 6px 16px; border-radius: 4px;
      font-size: 12px; z-index: 99999; pointer-events: none;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
  `;
  document.head.appendChild(style);
}

function showToast(msg: string): void {
  const el = document.createElement('div');
  el.className = 'cb-toast';
  el.textContent = msg;
  document.body.appendChild(el);
  setTimeout(() => el.remove(), 1500);
}

function setupPasteInterceptor(): void {
  const ghost = document.createElement('textarea');
  ghost.setAttribute('aria-hidden', 'true');
  ghost.tabIndex = -1;
  ghost.style.cssText =
    'position:fixed;left:0;top:0;width:1px;height:1px;opacity:0;pointer-events:none;z-index:-1;';
  document.body.appendChild(ghost);

  // 1) Ctrl+V keydown: 가로채서 WebRTC에 안 보냄, ghost에 포커스
  document.addEventListener('keydown', (e: KeyboardEvent) => {
    if (!((e.ctrlKey || e.metaKey) && e.key === 'v')) return;
    if ((e.target as HTMLElement)?.closest?.('.cb-dialog')) return;

    // 합성 이벤트는 통과시킴 (WebRTC가 받도록)
    if (skipNextCtrlV) {
      skipNextCtrlV = false;
      return;
    }

    // 실제 사용자 Ctrl+V → WebRTC에 안 보냄
    e.stopImmediatePropagation();
    // preventDefault 안 함 → 브라우저가 ghost textarea에 paste 실행

    ghost.value = '';
    ghost.focus();
  }, true); // capture phase — WebRTC보다 먼저 실행

  // 2) ghost에서 paste 발생 → Kit에 전달 → 합성 Ctrl+V dispatch
  ghost.addEventListener('paste', (e: ClipboardEvent) => {
    const text = e.clipboardData?.getData('text/plain') || '';
    e.preventDefault(); // ghost textarea에 텍스트 남기지 않음
    ghost.value = '';
    ghost.blur();

    if (!text) return;

    copyToKit(text).then(() => {
      showToast(`Pasted ${text.length} chars`);
      // Kit clipboard에 텍스트가 들어갔으니, 합성 Ctrl+V로 Kit에서 paste 실행
      setTimeout(() => {
        skipNextCtrlV = true;
        const down = new KeyboardEvent('keydown', {
          key: 'v', code: 'KeyV', keyCode: 86,
          ctrlKey: true, bubbles: true, cancelable: true,
        });
        const up = new KeyboardEvent('keyup', {
          key: 'v', code: 'KeyV', keyCode: 86,
          ctrlKey: true, bubbles: true, cancelable: true,
        });
        document.dispatchEvent(down);
        document.dispatchEvent(up);
      }, 150);
    });
  });
}

async function showCopyDialog(): Promise<void> {
  const text = await pasteFromKit();
  if (!text) { showToast('Kit clipboard is empty'); return; }

  const overlay = document.createElement('div');
  overlay.className = 'cb-overlay';
  overlay.innerHTML = `
    <div class="cb-dialog">
      <h3>Copy (Ctrl+C)</h3>
      <textarea readonly></textarea>
      <div class="cb-hint">Ctrl+C / Cmd+C 로 복사 후 닫기</div>
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

  document.addEventListener('keydown', (e: KeyboardEvent) => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'c') {
      if ((e.target as HTMLElement)?.closest?.('.cb-dialog')) return;
      e.preventDefault();
      e.stopImmediatePropagation();
      showCopyDialog();
    }
  }, true);

  console.info('[clipboard-bridge] Initialized (paste=transparent, copy=dialog)');
}
