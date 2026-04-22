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
  
  # TURN server URL (internal WebRTC relay). Override via TURN_URL env if needed.
  TURN_URL=${TURN_URL:-turn:10.61.3.74:3478}
  TURN_USER=${TURN_USER:-isaac}
  TURN_PASS=${TURN_PASS:-isaac}

  echo "Configuring stream settings:"
  echo "  SIGNALING_SERVER: ${SIGNALING_SERVER}"
  echo "  SIGNALING_PORT: ${SIGNALING_PORT}"
  echo "  MEDIA_SERVER: ${MEDIA_SERVER}"
  echo "  FORCE_WSS: ${FORCE_WSS}"
  echo "  TURN_URL: ${TURN_URL}"

  # Patch the stream config with actual values (search for keys, not placeholders)
  # This works on both first run and restarts where values may have changed
  sed -i "s/signalingServer: [^,]*/signalingServer: ${SIGNALING_SERVER}/" /app/web-viewer-sample/src/main.ts
  sed -i "s/signalingPort: [^,]*/signalingPort: ${SIGNALING_PORT}/" /app/web-viewer-sample/src/main.ts
  sed -i "s/mediaServer: '[^']*'/mediaServer: '${MEDIA_SERVER}'/" /app/web-viewer-sample/src/main.ts
  sed -i "s/forceWSS: [^,]*/forceWSS: ${FORCE_WSS}/" /app/web-viewer-sample/src/main.ts

  # RTCPeerConnection monkey-patch으로 iceServers(TURN) 강제 주입.
  # @nvidia/omniverse-webrtc-streaming-library 의 DirectConfig 는 iceServers 미지원이라
  # 라이브러리 내부에서 호출하는 new RTCPeerConnection(config) 를 가로채 TURN relay 강제.
  # main.ts 최상단에 멱등 삽입.
  if ! grep -q "__ISAAC_TURN_PATCH__" /app/web-viewer-sample/src/main.ts; then
    cat > /tmp/turn-patch.ts <<EOF
// __ISAAC_TURN_PATCH__ — RTCPeerConnection monkey-patch for TURN relay
const _OrigRTCPC = (window as any).RTCPeerConnection;
(window as any).RTCPeerConnection = function (cfg: any, ...rest: any[]) {
  cfg = cfg || {};
  cfg.iceServers = [
    { urls: ['${TURN_URL}?transport=udp', '${TURN_URL}?transport=tcp'],
      username: '${TURN_USER}', credential: '${TURN_PASS}' }
  ];
  cfg.iceTransportPolicy = 'relay';
  return new _OrigRTCPC(cfg, ...rest);
};
(window as any).RTCPeerConnection.prototype = _OrigRTCPC.prototype;
EOF
    # main.ts 맨 앞에 prepend (clipboard-bridge import 라인 이후에 넣기 위해 sed 사용)
    cat /tmp/turn-patch.ts /app/web-viewer-sample/src/main.ts > /tmp/main-new.ts
    mv /tmp/main-new.ts /app/web-viewer-sample/src/main.ts
  fi

  exec npm run dev -- --host 0.0.0.0
}

main "${@}"
