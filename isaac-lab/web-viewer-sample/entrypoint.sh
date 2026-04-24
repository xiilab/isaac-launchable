#!/bin/bash
# SPDX-FileCopyrightText: Copyright (c) 2025 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -xeuo pipefail

ENV=${ENV:-instance}
FORCE_WSS=${FORCE_WSS:-true}

# Set default signaling port based on FORCE_WSS (443 for WSS, 80 for WS)
if [[ "${FORCE_WSS}" == "true" ]]; then
  DEFAULT_SIGNALING_PORT=443
else
  DEFAULT_SIGNALING_PORT=80
fi
SIGNALING_PORT=${SIGNALING_PORT:-${DEFAULT_SIGNALING_PORT}}

# WebRTC Gateway routing: signaling path (default '/sign_in' preserves upstream behavior)
# and coturn TURN credentials for forced-relay peer connections on the browser side.
SIGNAL_PATH=${SIGNAL_PATH:-/sign_in}
TURN_URI=${TURN_URI:-}
TURN_USERNAME=${TURN_USERNAME:-}
TURN_CREDENTIAL=${TURN_CREDENTIAL:-}

get_ip() {
  case ${ENV} in
    instance)
      nslookup $(hostname) | grep Address | head -2 | tail -1 | awk '{print $2}'
      ;;
    localhost)
      echo "127.0.0.1"
      ;;
    brev)
      curl -s https://icanhazip.com
      ;;
    *)
      echo "Env ${ENV} not understood" >&2
      exit 1
      ;;
  esac
}

main() {
  # Get IP address for media server
  IP=$(get_ip)
  
  # Signaling server defaults to window.location.hostname (evaluated in browser)
  # Can be overridden via SIGNALING_SERVER env var (e.g., for testing with specific IP)
  SIGNALING_SERVER=${SIGNALING_SERVER:-window.location.hostname}
  
  # Media server uses IP address (can be overridden via MEDIA_SERVER env var)
  MEDIA_SERVER=${MEDIA_SERVER:-${IP}}
  
  echo "Configuring stream settings:"
  echo "  SIGNALING_SERVER: ${SIGNALING_SERVER}"
  echo "  SIGNALING_PORT:   ${SIGNALING_PORT}"
  echo "  MEDIA_SERVER:     ${MEDIA_SERVER}"
  echo "  FORCE_WSS:        ${FORCE_WSS}"
  echo "  SIGNAL_PATH:      ${SIGNAL_PATH}"
  echo "  TURN_URI:         ${TURN_URI}"
  echo "  TURN_USERNAME:    ${TURN_USERNAME}"

  # Patch the stream config with actual values (search for keys, not placeholders)
  # This works on both first run and restarts where values may have changed
  sed -i "s/signalingServer: [^,]*/signalingServer: ${SIGNALING_SERVER}/" /app/web-viewer-sample/src/main.ts
  sed -i "s/signalingPort: [^,]*/signalingPort: ${SIGNALING_PORT}/" /app/web-viewer-sample/src/main.ts
  sed -i "s/mediaServer: '[^']*'/mediaServer: '${MEDIA_SERVER}'/" /app/web-viewer-sample/src/main.ts
  sed -i "s/forceWSS: [^,]*/forceWSS: ${FORCE_WSS}/" /app/web-viewer-sample/src/main.ts

  # Browser uses the library's default signalingPath ('/sign_in') and hits it
  # through the nginx sidecar's /sign_in -> 49100 proxy. The gateway sidecar
  # is unused on this path; we intentionally do NOT inject a signalingPath
  # override because the library appends its own '/sign_in' suffix, which
  # would produce a duplicated path.

  # Inject an RTCPeerConnection override at the top of main.ts so the browser
  # peer uses the coturn relay. DirectConfig does NOT expose iceServers /
  # iceTransportPolicy (verified against
  # @nvidia/omniverse-webrtc-streaming-library.d.ts in v1.x), so we can't pass
  # them via streamConfig. Overriding window.RTCPeerConnection before the
  # library imports is the supported hook to force
  # iceTransportPolicy='relay' with our TURN credentials on whatever
  # PeerConnection the library constructs internally.
  if [ -n "${TURN_URI}" ] && ! grep -q "isaac-launchable-turn-override" /app/web-viewer-sample/src/main.ts; then
    cat > /tmp/pc-override.snippet <<EOF
// isaac-launchable-turn-override: injected by entrypoint for coturn relay
;(function() {
  const OrigPC = window.RTCPeerConnection;
  const injected = {
    iceServers: [
      { urls: ['${TURN_URI}?transport=udp', '${TURN_URI}?transport=tcp'],
        username: '${TURN_USERNAME}', credential: '${TURN_CREDENTIAL}' }
    ],
    iceTransportPolicy: 'relay',
  };
  const Patched: any = function(cfg: any) {
    const merged = Object.assign({}, cfg || {}, injected);
    return new OrigPC(merged);
  };
  Patched.prototype = OrigPC.prototype;
  (window as any).RTCPeerConnection = Patched;
})();
EOF
    cat /tmp/pc-override.snippet /app/web-viewer-sample/src/main.ts > /tmp/main.ts.patched
    mv /tmp/main.ts.patched /app/web-viewer-sample/src/main.ts
    rm -f /tmp/pc-override.snippet
  fi

  exec npm run dev -- --host 0.0.0.0
}

main "${@}"
