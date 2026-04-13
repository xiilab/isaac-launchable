# Isaac Launchable USD Composer 배포 가이드

> 날짜: 2026-04-10
> 이미지: `nvcr.io/nvidia/omniverse/usd-composer-sample:109.0.4` → Harbor 재태깅
> 네임스페이스: `isaac-launchable` (ws-node074, 10.61.3.74)
> 접속 URL: `http://10.61.3.126/viewer/`

---

## 배경

Isaac Sim Full 이미지가 아닌 **USD Composer** 풀 에디터 UI를 브라우저에서 WebRTC 스트리밍으로 제공하기 위한 배포. NGC에서 `usd-composer-sample:109.0.4` 이미지를 가져와 로컬 Harbor에 등록 후 Isaac Launchable 인프라(nginx + web-viewer 사이드카)를 재활용한다.

---

## 아키텍처

```
브라우저
  │
  └── HTTP → 10.61.3.126 (nginx-community Ingress)
                │
                └── kit-streaming-svc:80
                      │
                      └── kit-streaming Pod (ws-node074)
                            ├── kit-streaming (USD Composer 109.0.4)
                            │     ├── Signal: TCP 49100 (WS signaling)
                            │     ├── Media:  UDP 47999 (WebRTC, hostPort)
                            │     └── REST:   TCP 8011 (NVCF 세션 API)
                            ├── nginx :80
                            │     ├── /viewer/ → localhost:5173
                            │     └── /sign_in → localhost:49100
                            └── web-viewer (Vite) :5173
```

---

## 핵심 차이: Isaac Sim vs USD Composer

| 항목 | Isaac Sim (`isaacsim.exp.full`) | USD Composer (`usd_composer_nvcf.kit`) |
|------|----------------------------------|----------------------------------------|
| 사용자 | root (uid=0) | ubuntu (uid=1000) |
| 캐시 경로 | `/root/.cache/ov` | `/home/ubuntu/.cache/ov` |
| 스트리밍 방식 | 직접 signalPort 열기 | NVCF 세션 API 필요 (`/v1/streaming/creds`) |
| REST API | 없음 | port 8011 uvicorn (FastAPI) |
| 환경변수 | `ISAACSIM_SIGNAL_PORT` 등 | `NVDA_KIT_ARGS` |

---

## 배포 절차

### 1. NGC 이미지 Pull → Harbor Push

> NGC에서 직접 pull이 가능한 노드(마스터 node75)에서 진행한다. 워커 노드(ws-node074)는 TLS 타임아웃이 발생할 수 있다.

```bash
# 1) 마스터 노드에서 NGC 이미지 pull
ssh root@10.61.3.75

k0s ctr images pull \
  --user '$oauthtoken:<NGC_API_KEY>' \
  nvcr.io/nvidia/omniverse/usd-composer-sample:109.0.4

# 2) Harbor 태그 부여
k0s ctr images tag \
  nvcr.io/nvidia/omniverse/usd-composer-sample:109.0.4 \
  10.61.3.124:30002/library/usd-composer-sample:109.0.4

# 3) Harbor push (plain-http, = 없이)
k0s ctr images push --plain-http \
  10.61.3.124:30002/library/usd-composer-sample:109.0.4
# 약 113초 소요 (6GB 이상)
```

### 2. K8s 매니페스트 적용

```bash
# NFS PVC 생성
k0s kubectl apply -f k8s/base/pvcs.yaml

# USD Composer Deployment 배포
k0s kubectl apply -f k8s/usd-composer/deployment.yaml
k0s kubectl apply -f k8s/usd-composer/ingress.yaml
```

> k8s/usd-composer/deployment.yaml 참조:
> - initContainer: NFS 디렉토리를 ubuntu:ubuntu(1000:1000)로 권한 변경
> - NVDA_KIT_ARGS: streamPort, publicIp 오버라이드 + Content Browser `my-computer` 활성화
> - lifecycle.postStart: NVCF 세션 API 자동 초기화
> - minio-omniverse PVC: CSI-S3 드라이버로 MinIO 버킷을 `/mnt/minio`에 마운트

---

## MinIO 스토리지 연동 (CSI-S3)

### 개요

MinIO(10.61.3.117:9000)의 `omniverse` 버킷을 CSI-S3 드라이버(yandex-cloud/k8s-csi-s3)를 통해 Pod 내부 `/mnt/minio`에 마운트한다. USD Composer에서 로컬 파일처럼 MinIO의 USD 파일을 열고 저장할 수 있다.

상세는 [MinIO 설치 가이드](minio-install.md) 참조.

### Content Browser에서 로컬 파일 접근

**문제:** NVCF 스트리밍 이미지의 기본 `.kit` 설정이 Content Browser에서 **My Computer** 항목을 숨긴다.

**해결:** `NVDA_KIT_ARGS`에 다음 3개 설정을 추가하여 `my-computer` 컬렉션을 복원한다:

```
--/exts/omni.kit.window.content_browser/show_only_collections=[bookmarks,omniverse,my-computer]
--/exts/omni.kit.window.filepicker/show_only_collections=[bookmarks,omniverse,my-computer]
--/exts/omni.kit.window.filepicker/show_add_new_connection=true
```

### 사용 방법

1. USD Composer 접속 (`http://10.61.3.126/viewer/`)
2. 하단 **Content** 패널 → 좌측 트리에서 **My Computer** 클릭
3. `/mnt/minio` 경로로 이동
4. USD 파일 더블클릭으로 열기 / **File → Save As** 로 저장

---

## NVCF 세션 관리 원리 (핵심)

`usd_composer_nvcf.kit`는 **NVIDIA Cloud Function(NVCF)** 환경에서 동작하도록 설계되어, 외부 오케스트레이터가 `POST /v1/streaming/creds`를 호출해야 WebRTC offer 생성이 시작된다.

```
# 일반 Kit 스트리밍 흐름
Kit 시작 → 포트 열기 → 브라우저 접속 → offer 전송 → 연결

# NVCF Kit 흐름
Kit 시작 → 포트 열기 → [POST /v1/streaming/creds] → 브라우저 접속 → offer 전송 → 연결
                                 ↑ 이 단계 없으면 "StreamerNoOffer"
```

### REST API 엔드포인트 (port 8011)

```bash
# 스트리밍 준비 상태 확인
curl http://localhost:8011/v1/streaming/ready
# {"statusMessage":"Status: Ready for connection"}

# 세션 초기화 (STUN 없이)
curl -X POST http://localhost:8011/v1/streaming/creds \
  -H 'Content-Type: application/json' \
  -d '{"stunIp":null,"stunPort":null,"username":null,"password":null}'
# {"success":true,"errorMessage":null}
```

---

## 검증

### Pod 상태

```bash
k0s kubectl get pod -l instance=kit-streaming -n isaac-launchable
# NAME            READY   STATUS    RESTARTS   AGE
# kit-streaming   3/3     Running   0          2m
```

### NVCF 세션 자동 초기화 확인

```bash
k0s kubectl exec -n isaac-launchable <kit-pod> -c kit-streaming -- \
  bash -c 'grep "stun credentials" /home/ubuntu/.nvidia-omniverse/logs/Kit/"USD Composer NVCF Streaming"/109.0/*.log'
# [Info] Processed stun credentials: uri :0
```

### HTTP 응답 확인

```bash
curl -o /dev/null -w '%{http_code}' http://10.61.3.126/
# 301
curl -o /dev/null -w '%{http_code}' http://10.61.3.126/viewer/
# 200
```

### NFS 셰이더 캐시 효과

| 항목 | 초기 배포 | NFS 캐시 후 |
|------|----------|------------|
| RTX ready 시간 | ~60초 | ~15초 |

---

## 트러블슈팅

### StreamerNoOffer 에러 (브라우저에서)

**원인:** `/v1/streaming/creds` 미호출. postStart 훅이 실패했거나 오래된 Pod.

**수동 수정:**
```bash
k0s kubectl exec -n isaac-launchable <kit-pod> -c kit-streaming -- \
  curl -X POST http://localhost:8011/v1/streaming/creds \
  -H 'Content-Type: application/json' \
  -d '{"stunIp":null,"stunPort":null,"username":null,"password":null}'
# 이후 브라우저 새로고침
```

### NFS 권한 오류 (PermissionError)

**원인:** 이전 Isaac Sim 배포가 root로 생성한 디렉토리가 남아있음. USD Composer(uid=1000)가 쓰기 불가.

**수정:** initContainer가 자동으로 `chown -R 1000:1000` 수행. Pod 재배포로 해결.

### Harbor Push 오류 (HTTPS 관련)

```bash
# 잘못된 방법
k0s ctr images push --plain-http=false

# 올바른 방법
k0s ctr images push --plain-http  # = 없이 flag만 사용
```

### NGC TLS 타임아웃 (워커 노드)

```bash
# 워커 노드(ws-node074)에서 pull하면 TLS 타임아웃 발생
# 마스터 노드(node75)에서 k0s ctr images pull 사용
ssh root@10.61.3.75 "k0s ctr images pull ..."
```

---

## 동작 흐름

```
Kit 시작
  │
  ├── initContainer: NFS chown 1000:1000
  │
  └── kit-streaming 컨테이너 시작
        ├── USD Composer 앱 로딩 (~9초)
        ├── port 49100 (signal) + port 8011 (REST) LISTEN
        ├── RTX ready (~15초, NFS 캐시 시)
        └── postStart 훅 실행
              ├── polling: curl /v1/streaming/ready (3초 간격)
              └── POST /v1/streaming/creds → {"success":true}
                    │
                    ▼
              브라우저 ws://10.61.3.126/sign_in → nginx → localhost:49100
                    │
                    ▼
              WebRTC offer/answer 교환 → UDP 47999 미디어 스트림
                    │
                    ▼
              http://10.61.3.126/viewer/ → USD Composer 풀 에디터 UI
```

---

## 사고 기록: omni-streaming 네임스페이스 무단 수정

> 작업 중 omni-streaming 네임스페이스의 리소스를 실수로 삭제하는 사고 발생. 즉시 원상복구 완료.

**삭제된 리소스:**
- `ovas-web-viewer-ingress` (isaac-launchable namespace로 이전하려다 실수)

**복구 방법:**
```bash
k0s kubectl apply -f /root/manifests/omni-streaming/ovas-web-viewer-ingress.yaml
```

**재발 방지:** omni-streaming 네임스페이스는 절대 수정 금지. 모든 작업은 isaac-launchable 네임스페이스에서만 수행.

---

## 관련 문서

- [Isaac Sim 배포 가이드](isaac-sim-deploy.md)
- [도입 스펙 문서](spec.md)
- [MinIO 설치 가이드](minio-install.md)
