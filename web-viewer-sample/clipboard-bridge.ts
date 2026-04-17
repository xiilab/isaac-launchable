/**
 * Clipboard Bridge: browser <-> Kit clipboard sharing via HTTP API
 *
 * Works in both HTTPS (navigator.clipboard) and HTTP (textarea fallback).
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

/** Write text to browser clipboard — works in both secure and insecure contexts. */
async function writeToBrowserClipboard(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // Secure context not available — fall through to textarea approach
    }
  }
  // Fallback: hidden textarea + execCommand('copy')
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.position = 'fixed';
  ta.style.left = '-9999px';
  ta.style.top = '-9999px';
  ta.style.opacity = '0';
  document.body.appendChild(ta);
  ta.focus();
  ta.select();
  try {
    document.execCommand('copy');
  } finally {
    document.body.removeChild(ta);
  }
}

/** Read text from browser clipboard — works in both secure and insecure contexts. */
function readFromBrowserClipboard(): Promise<string> {
  if (navigator.clipboard?.readText) {
    return navigator.clipboard.readText().catch(() => '');
  }
  // In insecure context, readText is not available.
  // We cannot read the clipboard without user permission in HTTP.
  // Return empty — the user can use the paste prompt approach instead.
  return Promise.resolve('');
}

export function initClipboardBridge(): void {
  // Handle paste events for HTTP fallback (browser fires 'paste' with clipboard data)
  document.addEventListener('paste', async (e: ClipboardEvent) => {
    const text = e.clipboardData?.getData('text/plain');
    if (text) {
      await copyToKit(text);
    }
  });

  document.addEventListener('keydown', async (e: KeyboardEvent) => {
    const mod = e.ctrlKey || e.metaKey;
    if (!mod) return;

    // Ctrl+V: push browser clipboard into Kit before the keydown reaches WebRTC
    if (e.key === 'v') {
      const text = await readFromBrowserClipboard();
      if (text) {
        await copyToKit(text);
      }
      // Note: if readFromBrowserClipboard returns empty in HTTP,
      // the 'paste' event handler above will catch it instead.
    }

    // Ctrl+C: after Kit processes the copy, pull Kit clipboard into browser
    if (e.key === 'c') {
      setTimeout(async () => {
        const text = await pasteFromKit();
        if (text) {
          await writeToBrowserClipboard(text);
        }
      }, 250);
    }
  });

  console.info('[clipboard-bridge] Initialized: browser <-> Kit clipboard sharing');
}
