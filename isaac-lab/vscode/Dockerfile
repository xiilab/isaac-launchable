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
FROM nvcr.io/nvidia/isaac-lab:2.3.0

RUN apt update && \
    apt install -y \
	build-essential \
	cmake \
	curl \
	git

RUN curl -fsSL https://code-server.dev/install.sh | sh -s -- --version=4.96.4

RUN apt update && \
	apt install -y \
	unzip \
	dnsutils \
	iproute2 \
	jq

ENV WORKSPACE_DIR=/workspace

COPY --chown=isaac-sim:isaac-sim ./README.md ${WORKSPACE_DIR}/README.md
COPY --chown=isaac-sim:isaac-sim ./settings.json ${WORKSPACE_DIR}/.vscode/settings.json


RUN code-server \
    --install-extension ms-python.python \
    --install-extension ms-toolsai.jupyter \
    --force ${WORKSPACE_DIR}

COPY entrypoint.sh /entrypoint.sh

# OV Cache Directories
RUN mkdir -p /home/isaac-sim/.cache/ov
RUN mkdir -p /home/isaac-sim/.local/share/ov

# Fix permissions for ubuntu user
RUN chown -R isaac-sim /workspace /home/isaac-sim/.cache/ov /home/isaac-sim/.local/share/ov /isaac-sim/kit/cache

# Copy bashrc to get aliases
RUN cp /root/.bashrc /isaac-sim/.root-bashrc
RUN echo 'source $HOME/.root-bashrc' >> /isaac-sim/.bashrc

# HAMi vGPU enforcement via system-wide preload.
# HAMi device-plugin mounts libvgpu.so at /usr/local/vgpu/libvgpu.so when the pod requests
# nvidia.com/gpumem or nvidia.com/gpucores. Registering it in /etc/ld.so.preload survives
# scripts (e.g. isaaclab.sh) that overwrite LD_PRELOAD, so every child process still gets
# the CUDA/Vulkan hooks. ld.so silently skips the entry when the file is absent, so images
# run unchanged on nodes without HAMi.
RUN echo "/usr/local/vgpu/libvgpu.so" > /etc/ld.so.preload

USER isaac-sim:isaac-sim

ENTRYPOINT ["/bin/bash"]

CMD ["/entrypoint.sh"]
