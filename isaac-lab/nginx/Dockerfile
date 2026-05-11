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
FROM openresty/openresty:1.21.4.1-0-alpine

RUN wget 'https://openresty.org/package/admin@openresty.com-5ea678a6.rsa.pub' && \
    mv 'admin@openresty.com-5ea678a6.rsa.pub' /etc/apk/keys/ && \
    . /etc/os-release && \
    export MAJOR_VER=`echo $VERSION_ID | sed 's/\.[0-9]\+$//'` && \
    echo "https://openresty.org/package/alpine/v$MAJOR_VER/main" \
    | tee -a /etc/apk/repositories

RUN apk update && apk add --no-cache argon2 nginx-mod-http-lua luajit openresty lua-resty-core openssl

COPY entrypoint.sh entrypoint.sh
COPY nginx.conf /etc/nginx/nginx.conf
RUN mkdir -p /etc/nginx/tls

CMD ["sh", "/entrypoint.sh"]

# Build & push (OCI annotations consumed by astrago builtin catalog).
# 컨테이너 스펙은 k8s/isaac-sim/deployment-0.yaml 의 nginx 컨테이너 (port 80/TCP, env 없음) 기준.
# docker buildx build \
#     --tag harbor.xx/astrago_built_in/isaac-launchable-nginx:latest \
#     --annotation "manifest:astrago.builtin.display_name=isaac-launchable-nginx" \
#     --annotation "manifest:astrago.builtin.command=sh /entrypoint.sh" \
#     --annotation "manifest:astrago.builtin.ports=[{\"name\":\"http\",\"port\":80}]" \
#     --annotation "manifest:astrago.builtin.env=[]" \
#     --annotation "manifest:astrago.builtin.description=Nginx reverse proxy for Isaac Launchable" \
#     --output type=registry,oci-mediatypes=true \
#     -f isaac-lab/nginx/Dockerfile isaac-lab/nginx
