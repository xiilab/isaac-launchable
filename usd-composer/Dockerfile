# USD Composer 109.0.4 - annotation overlay (pass-through).
# nvcr.io 베이스 이미지에 빌드 단계 추가 없이 OCI annotation 만 새로운 매니페스트에 부착하기 위한 Dockerfile.
# 컨테이너 스펙은 k8s/usd-composer/deployment.yaml 의 kit-streaming 컨테이너 기준.
# 제외 항목:
#   - NVDA_KIT_ARGS: publicIp=10.61.3.74 노드 IP 가 박혀있어 노드 의존
#   - AUTO_ENABLE_DRIVER_SHADER_CACHE_WRAPPER: 네임스페이스별 memcached URL
#   - command/args: deployment 에서 override 하지 않으므로 이미지 기본값 사용
FROM nvcr.io/nvidia/omniverse/usd-composer-sample:109.0.4

# Build & push (OCI annotations consumed by astrago builtin catalog).
# docker buildx build \
#     --tag harbor.xx/astrago_omniverse/usd-composer-sample:109.0.4 \
#     --annotation "manifest:astrago.builtin.omniverse_type=USD_COMPOSER" \
#     --annotation "manifest:astrago.builtin.display_name=usd-composer-sample" \
#     --annotation "manifest:astrago.builtin.ports=[{\"name\":\"webrtc-media\",\"port\":47999,\"protocol\":\"UDP\"},{\"name\":\"webrtc-signal\",\"port\":49100,\"protocol\":\"TCP\"}]" \
#     --annotation "manifest:astrago.builtin.env=[{\"name\":\"ACCEPT_EULA\",\"value\":\"Y\"},{\"name\":\"NVIDIA_VISIBLE_DEVICES\",\"value\":\"all\"},{\"name\":\"NVIDIA_DRIVER_CAPABILITIES\",\"value\":\"all\"}]" \
#     --annotation "manifest:astrago.builtin.description=Omniverse USD Composer 109.0.4 (Kit livestream)" \
#     --output type=registry,oci-mediatypes=true \
#     -f usd-composer/Dockerfile usd-composer
