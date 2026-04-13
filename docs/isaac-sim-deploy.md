# Isaac Launchable K8s 배포 가이드

> 날짜: 2026-04-04
> 환경: k0s 클러스터 (마스터: node75 10.61.3.75, 워커: ws-node074 10.61.3.74, RTX 6000 Ada x2)
> kubectl 접근: ssh root@10.61.3.75 → `k0s kubectl` 사용
> isaac-launchable 버전: 1.2.1

---

## 배경

Isaac Sim + ROS 2 nvblox 튜토리얼을 실행하려면 Isaac Sim과 ROS 2가 **같은 환경에서 localhost로 통신**해야 한다. OVAS는 WebRTC 영상 스트리밍만 제공하므로 ROS 2 토픽을 노출할 수 없다.

**isaac-launchable**은 3개 컨테이너(VSCode + Nginx + Web Viewer)를 1 Pod에 배치하여, VSCode 터미널에서 Isaac Sim을 실행하고 브라우저에서 뷰어를 볼 수 있게 해준다. VSCode 컨테이너 안에서 ROS 2 설치 및 nvblox 실행이 가능하다.

```
OVAS (불가능):
  Isaac Sim Pod → WebRTC 영상만 → 브라우저 (ROS 토픽 없음)

isaac-launchable (가능):
  Pod 안 VSCode 컨테이너:
    ├─ Isaac Sim 실행 (ROS 2 bridge 포함)
    ├─ ROS 2 Jazzy 설치 가능
    ├─ nvblox + Nav2 실행 가능
    └─ 모든 ROS 토픽이 localhost로 통신 ✅
```

## 전체 아키텍처

```
┌─ K8s Cluster ──────────────────────────────────────────────┐
│                                                             │
│  ┌─ Ingress (isaac-launchable.local) ───────────────────┐  │
│  │  nginx-community ingressClass (10.61.3.126)           │  │
│  │  모든 경로 → Service → Pod nginx :80                   │  │
│  └───────────────────────────────────────────────────────┘  │
│                                                             │
│  ┌─ isaac-launchable Pod (ws-node074) ──────────────────┐  │
│  │  GPU: RTX 6000 Ada (1개)                              │  │
│  │                                                       │  │
│  │  ┌─────────┐  ┌──────────┐  ┌───────────────────┐   │  │
│  │  │ nginx   │  │ vscode   │  │ web-viewer        │   │  │
│  │  │ :80     │  │ :8080    │  │ :5173             │   │  │
│  │  └────┬────┘  └──────────┘  └───────────────────┘   │  │
│  │       │                                               │  │
│  │       ├── /          → localhost:8080 (VSCode)        │  │
│  │       ├── /viewer/   → localhost:5173 (뷰어)          │  │
│  │       └── /sign_in   → localhost:49100 (WebRTC)       │  │
│  │                                                       │  │
│  │  hostPort (WebRTC 전용):                               │  │
│  │    :47998 UDP → WebRTC 미디어                          │  │
│  │    :49100 TCP → WebRTC 시그널링                        │  │
│  └───────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

## 사전 요구사항

| 항목 | 필요 여부 | 비고 |
|------|-----------|------|
| RTX GPU 노드 | 필수 | RT Core 필요 (A100/H100 불가) |
| GPU Operator | 필수 | device-plugin + container-toolkit |
| StorageClass | 필수 | 셰이더 캐시 PVC 용 |
| Ingress Controller | 필수 | nginx-community 사용 |
| MetalLB IP 할당 | **불필요** | Ingress + hostPort 방식 |
| nvcr.io 인증 | 빌드 시 필요 | NGC API Key |
| Harbor | 필수 | 이미지 저장소 |

## 1. 이미지 빌드

### 리포 클론 (빌드 서버)

```bash
git clone https://github.com/isaac-sim/isaac-launchable.git /root/isaac-launchable
cd /root/isaac-launchable/isaac-lab
```

### nvcr.io 로그인

```bash
docker login nvcr.io
# Username: $oauthtoken
# Password: <NGC API Key>
```

### 3개 이미지 빌드

```bash
# 1. Nginx (~48MB, 빠름)
docker build -t 10.61.3.124:30002/library/isaac-launchable-nginx:latest \
  --network host ./nginx/

# 2. VSCode + Isaac Lab (~8.8GB, isaac-lab:2.3.0 베이스)
docker build -t 10.61.3.124:30002/library/isaac-launchable-vscode:latest \
  --network host ./vscode/

# 3. Web Viewer (~560MB, Node.js + Vite)
docker build -t 10.61.3.124:30002/library/isaac-launchable-viewer:latest \
  --network host ./web-viewer-sample/
```

### Harbor Push

```bash
docker push 10.61.3.124:30002/library/isaac-launchable-nginx:latest
docker push 10.61.3.124:30002/library/isaac-launchable-vscode:latest
docker push 10.61.3.124:30002/library/isaac-launchable-viewer:latest
```

## 2. K8s 매니페스트

> k8s/ 디렉토리의 YAML 파일 참조:
> - `k8s/base/` — 공통 리소스 (namespace, configmaps, services, pvcs, secret)
> - `k8s/isaac-sim/` — Isaac Sim Deployment + Ingress
> - `k8s/storage/` — CSI-S3 MinIO 스토리지

## 3. 배포

```bash
# 순서대로 적용
k0s kubectl apply -f k8s/base/namespace.yaml
k0s kubectl apply -f k8s/base/configmaps.yaml
k0s kubectl apply -f k8s/base/secret.yaml      # VSCODE_PASSWORD 수정 후
k0s kubectl apply -f k8s/base/pvcs.yaml
k0s kubectl apply -f k8s/base/services.yaml
k0s kubectl apply -f k8s/base/memcached.yaml
k0s kubectl apply -f k8s/storage/storageclass.yaml
k0s kubectl apply -f k8s/storage/pv.yaml
k0s kubectl apply -f k8s/storage/pvc.yaml
k0s kubectl apply -f k8s/isaac-sim/deployment.yaml
k0s kubectl apply -f k8s/isaac-sim/ingress.yaml
```

## 4. 접속

| 경로 | 용도 |
|------|------|
| `http://10.61.3.125/` | VSCode 개발 환경 |
| `http://10.61.3.125/viewer/` | Isaac Sim 뷰어 (Kit App Streaming) |

## 5. Isaac Sim 실행

### VSCode 터미널에서:

```bash
# Isaac Sim 시작 (headless + 스트리밍)
/isaac-sim/runheadless.sh
# app ready 메시지 확인 후 /viewer 탭에서 확인
```

### nvblox 튜토리얼 실행 (향후):

```bash
# ROS 2 Jazzy 설치
sudo apt-get update
sudo apt-get install -y ros-jazzy-nav2-bringup ros-jazzy-nav2-route
sudo apt-get install -y ros-jazzy-rmw-cyclonedds-cpp
export RMW_IMPLEMENTATION=rmw_cyclonedds_cpp

# nvblox 실행
ros2 launch nvblox_examples_bringup isaac_sim_example.launch.py
```

## 검증 결과 (2026-04-04)

| 항목 | 결과 |
|------|------|
| Pod 3/3 Running | ✅ |
| PVC Bound (50Gi, astrago-nfs-csi) | ✅ |
| VSCode code-server 동작 | ✅ (127.0.0.1:8080) |
| Nginx 리버스 프록시 | ✅ |
| Web Viewer Vite 서버 | ✅ (0.0.0.0:5173) |
| Ingress HTTP 접속 (/) | ✅ 302 (로그인 리다이렉트) |
| Ingress HTTP 접속 (/viewer/) | ✅ 200 |

## Isaac Lab 실행 검증 (2026-04-06)

```bash
python isaaclab/scripts/reinforcement_learning/skrl/play.py \
  --task=Isaac-Ant-v0 --livestream 2
```

| 항목 | 결과 |
|------|------|
| GPU | RTX 6000 Ada (46GB VRAM) |
| CPU | AMD Threadripper PRO 7985WX 64-Core (16 Core 할당) |
| Memory | 32GB (25.5GB 가용) |
| Isaac Sim 버전 | 5.1.0-rc.19 |
| Isaac Lab 버전 | 0.47.1 |
| ROS 2 rclpy | ✅ Jazzy 로드 성공 |
| 환경 수 | 4096개 (Isaac-Ant-v0) |
| WebRTC 스트리밍 | ✅ `--livestream 2` 동작 확인 |

## 트러블슈팅: /viewer/ 응답 없음 (2026-04-06)

### 증상

`http://10.61.3.125/viewer/` 접속 시 페이지 로드는 되지만 Isaac Sim 영상이 표시되지 않음. WebRTC 연결 실패.

### 원인

Isaac Lab 프로세스(`play.py --livestream 2`)를 **2개 동시 실행**하여 WebRTC 시그널링 포트(49100)에 2개 프로세스가 바인딩됨.

```bash
# 포트 확인
ss -tlnp | grep 49100
```

### 해결

오래된 프로세스를 강제 종료:

```bash
kill -9 <old-pid>
```

### 예방

- Isaac Lab 실행 전 기존 프로세스 확인: `ps aux | grep play.py`
- WebRTC 포트(49100, 47998)는 단일 프로세스만 사용 가능

## OVAS vs isaac-launchable 비교

| 항목 | OVAS | isaac-launchable |
|------|------|-----------------|
| 목적 | 다중 사용자 Omniverse 앱 스트리밍 | 단일 사용자 개발 환경 |
| ROS 2 지원 | ❌ 불가 | ✅ VSCode 터미널에서 설치/실행 |
| 터미널 접근 | ❌ 없음 | ✅ VSCode (code-server) |
| GPU 필요 | RTX (RT Core) | RTX (RT Core) |
| IP 할당 | LoadBalancer 필요 | **불필요** (Ingress + hostPort) |
| nvblox 실행 | ❌ | ✅ |

## 관련 문서

- [도입 스펙 문서](spec.md)
- [USD Composer 배포 가이드](usd-composer-deploy.md)
- [MinIO 설치 가이드](minio-install.md)
