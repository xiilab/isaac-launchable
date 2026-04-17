import omni.ext
import carb

from omni.services.core import main
from omni.kit.clipboard import copy, paste


class ClipboardServiceExtension(omni.ext.IExt):
    """HTTP endpoints for browser <-> Kit clipboard sharing."""

    def on_startup(self, ext_id):
        carb.log_info("[omni.clipboard.service] Starting clipboard HTTP service")
        app = main.get_app()

        @app.post("/clipboard/copy", tags=["clipboard"])
        async def clipboard_copy(request: dict):
            """Write text to Kit clipboard (browser -> Kit)."""
            text = request.get("text", "")
            copy(text)
            return {"status": "ok", "length": len(text)}

        @app.get("/clipboard/paste", tags=["clipboard"])
        async def clipboard_paste():
            """Read text from Kit clipboard (Kit -> browser)."""
            text = paste() or ""
            return {"text": text}

        self._routes = ["/clipboard/copy", "/clipboard/paste"]
        carb.log_info("[omni.clipboard.service] Registered: POST /clipboard/copy, GET /clipboard/paste")

    def on_shutdown(self):
        carb.log_info("[omni.clipboard.service] Shutting down")
