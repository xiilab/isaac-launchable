/**
 * Clipboard Bridge: browser <-> Kit clipboard sharing via HTTP API
 *
 * Ctrl+V (browser -> Kit): reads browser clipboard, POSTs to Kit, then lets the
 *   WebRTC keydown event trigger Kit's internal paste.
 * Ctrl+C (Kit -> browser): waits for Kit to process the copy, then GETs the Kit
 *   clipboard and writes it to the browser clipboard.
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
    console.warn('[clipboard-bridge] Failed to copy to Kit:', e);
  }
}

async function pasteFromKit(): Promise<string> {
  try {
    const res = await fetch(`${CLIPBOARD_API}/paste`);
    const data = await res.json();
    return data.text || '';
  } catch (e) {
    console.warn('[clipboard-bridge] Failed to paste from Kit:', e);
    return '';
  }
}

export function initClipboardBridge(): void {
  document.addEventListener('keydown', async (e: KeyboardEvent) => {
    const mod = e.ctrlKey || e.metaKey;
    if (!mod) return;

    // Ctrl+V: push browser clipboard into Kit before the keydown reaches WebRTC
    if (e.key === 'v') {
      try {
        const text = await navigator.clipboard.readText();
        if (text) {
          await copyToKit(text);
        }
      } catch (err) {
        // Clipboard API may be blocked without HTTPS / user gesture
        console.warn('[clipboard-bridge] clipboard.readText() denied:', err);
      }
    }

    // Ctrl+C: after Kit processes the copy, pull Kit clipboard into browser
    if (e.key === 'c') {
      setTimeout(async () => {
        const text = await pasteFromKit();
        if (text) {
          try {
            await navigator.clipboard.writeText(text);
          } catch (err) {
            console.warn('[clipboard-bridge] clipboard.writeText() denied:', err);
          }
        }
      }, 250);
    }
  });

  console.info('[clipboard-bridge] Initialized: browser <-> Kit clipboard sharing');
}