# [스펙 문서] Isaac Launchable 도입

> 이 문서는 **코딩 시작 전** 프로젝트 담당자 모두가 작성한다.
> 크로스 리뷰 or 리드의 승인을 받아야 개발을 시작할 수 있다.
> 해당 없는 섹션도 삭제하지 말고, "해당 없음"으로 명시한다.

---

## 1. 프로젝트 개요

### 목적

> 기존 OVAS로는 Isaac Sim 구동 시 절차가 복잡하고 Isaac Lab 연동이 제한된다. https://github.com/isaac-sim/isaac-launchable 가 나오면서 사용자에게 Isaac Sim + Isaac Lab 제공이 가능할 것 같다.

- OVAS는 다중 사용자 Omniverse 앱 스트리밍 플랫폼으로, 인프라 컴포넌트 6종 이상(Session Manager, RMCP, Flux, Memcached, Ingress x2, Coturn)이 필요하고 Helm Chart + CRD 3종 배포가 요구되어 설치/운영 복잡도가 높다.
- OVAS는 WebRTC 뷰어 전용이라 터미널 접근이 불가능하여 Isaac Lab RL 학습, ROS 2 연동, 디버깅이 원천 차단된다.
- Isaac Launchable은 3-컨테이너(VSCode + Nginx + Web Viewer) 단일 Pod 구조로, `kubectl apply` 한 번으로 배포 가능하며 VSCode 터미널에서 Isaac Sim 실행 + Isaac Lab 학습 + ROS 2 설치까지 가능하다.
- Isaac Sim 6.0부터 동적 포트 할당(ISAACSIM_SIGNAL_PORT, ISAACSIM_STREAM_PORT, WEB_VIEWER_PORT)을 지원하여 **노드당 다중 Pod 배포**가 가능해졌다(기존 5.x는 hostPort 충돌로 노드당 1 Pod 제한).
- Astrago에 Isaac Launchable을 통합하면 웹 UI에서 원클릭 Isaac Lab 개발 환경 생성, GPU 스케줄링, 멀티테넌시를 제공할 수 있다.

### 주요 요구사항

- Astrago 웹 UI/CLI에서 Isaac Lab 개발 환경을 생성/삭제할 수 있어야 한다.
- GPU 노드의 다중 GPU를 효율적으로 활용해야 한다 (GPU당 1 Pod, 동적 포트 오프셋).
- 사용자별 PVC 격리 및 셰이더 캐시 영구 보존이 필요하다.
- 브라우저에서 VSCode(코드 편집) + Viewer(Isaac Sim 렌더링 스트리밍)에 접속 가능해야 한다.
- Isaac Lab RL 학습 → USD 파일 생성 → OVAS 시각화 파이프라인 연동 (장기 목표).

---

## 2. 기본 설계 / 작업 상세

### 기본 설계

**전체 아키텍처 (Isaac Launchable Pod 구조)**

```
사용자 브라우저
    │
    ├── HTTP → Ingress → Pod nginx:80
    │                     ├── /          → localhost:8080 (VSCode)
    │                     ├── /viewer/   → localhost:5173 (Web Viewer)
    │                     └── /sign_in   → localhost:49100+N (WebRTC 시그널링)
    │
    └── UDP → 노드IP:47998+N (WebRTC 미디어, hostPort)

K8s 클러스터:
  ┌─ isaac-launchable 네임스페이스 ───────────────────┐
  │  [Pod N — 3개 컨테이너]                            │
  │  ┌────────────┐ ┌──────────┐ ┌────────────────┐  │
  │  │ nginx :80  │ │ vscode   │ │ web-viewer     │  │
  │  │ (프록시)    │ │ :8080    │ │ :5173          │  │
  │  └────────────┘ └──────────┘ └────────────────┘  │
  │                                                    │
  │  hostPort: 47998+N/UDP, 49100+N/TCP               │
  │  GPU: nvidia.com/gpu: 1 (Pod당 1 GPU)             │
  │  PVC: isaac-workspace-pvc-N (50Gi)                │
  └────────────────────────────────────────────────────┘
```

**Astrago 연동 구조 (방안 2: ISAAC_LAB 워크로드 타입 — 권장)**

```
사용자 → Astrago UI → "Isaac Lab 환경 생성" 클릭
  → Astrago Backend
    → Pod 생성 (3-컨테이너: vscode + nginx + web-viewer)
    → Service 생성 (ClusterIP :80)
    → Ingress 자동 생성 ({workload-name}.isaac.{cluster-domain})
    → 동적 hostPort 오프셋 계산 (노드별 사용 중 포트 추적)
  → 사용자에게 접속 URL 반환
```

**Isaac Sim 6.0 동적 포트 할당**

| 환경변수 | 기본값 | 용도 |
|---|---|---|
| ISAACSIM_SIGNAL_PORT | 49100 (TCP) | WebRTC 시그널링 |
| ISAACSIM_STREAM_PORT | 47998 (UDP) | WebRTC 미디어 |
| WEB_VIEWER_PORT | 8210 (TCP) | 웹 뷰어 |

Pod별 포트 오프셋:
- Pod 0: hostPort 47998/49100
- Pod 1: hostPort 47999/49101
- Pod N: hostPort 47998+N/49100+N

**Ingress 설계 — 단일 IP + Host 기반 라우팅 (권장)**

| 방식 | 설명 | 장단점 |
|------|------|--------|
| IP 분리 (PoC 현행) | Pod당 별도 IP | 단순하지만 IP 수 = Pod 수로 확장 불가 |
| **Host 기반 (권장)** | 단일 IP + host header로 분기 | Pod 수 확장에 IP 추가 불필요 |
| Path 기반 | 단일 IP + path prefix로 분기 | WebSocket 경로 충돌 가능성 |

**Host 기반 라우팅 구조 (권장안):**

```
단일 Ingress Controller (예: nginx-community, 10.61.3.126)
  │
  ├── isaac-0.astrago.local → Service Pod 0 → Pod 0 (VSCode + Viewer)
  ├── isaac-1.astrago.local → Service Pod 1 → Pod 1 (VSCode + Viewer)
  └── ...
```

- 와일드카드 DNS 설정: `*.isaac.astrago.local → 10.61.3.126`
- Astrago Backend가 Pod 생성 시 `{workload-name}.isaac.{cluster-domain}` host로 Ingress 자동 생성

**외부 연동**

| 시스템 | 연동 방식 | 용도 |
|---|---|---|
| Harbor (10.61.3.124:30002) | 이미지 레지스트리 | isaac-lab, nginx, viewer 이미지 저장 |
| NGC (nvcr.io) | 베이스 이미지 소스 | Isaac Sim 6, Isaac Lab 3 공식 이미지 |
| OVAS (향후) | 학습 결과 → USD Viewer | USD 파일 시각화 파이프라인 |
| Keycloak | SSO | Astrago 사용자 인증 |

### 작업 상세

| # | 작업 | 설명 | 예상 공수 | 담당 |
|---|---|---|---|---|
| 1 | Backend: ISAAC_LAB 워크로드 타입 추가 | WorkloadJobType.kt에 ISAAC_LAB enum 추가 | 3일 | BE |
| 2 | Backend: 3-컨테이너 Pod 템플릿 생성 | PodTemplateBuilder에 vscode + nginx + web-viewer sidecar 주입 | 3일 | BE |
| 3 | Backend: 동적 hostPort 오프셋 계산 | 노드별 사용 중 포트 추적, Pod 생성 시 오프셋 계산 | 2일 | BE |
| 4 | Backend: Ingress 자동 생성 | WebSocket annotation 추가, 사용자별 고유 host 생성 | 1일 | BE |
| 5 | Backend: ConfigMap 동적 생성 | Pod별 nginx 설정(sign_in 포트 분기), web-viewer 설정 | 1일 | BE |
| 6 | Frontend: Isaac Lab 워크로드 생성 UI | 워크로드 타입 선택에 "Isaac Lab" 추가, GPU/스토리지 입력 폼 | 2일 | FE |
| 7 | Frontend: 워크로드 상세 — VSCode/Viewer 접속 버튼 | 워크로드 상세페이지에서 VSCode, Viewer 외부 링크 버튼 | 1일 | FE |
| 8 | Infra: 이미지 빌드 및 Harbor 등록 | isaac-lab:3.0.0-beta1 + nginx + viewer 이미지 빌드/푸시 | 1일 | Infra |
| 9 | Infra: k0s 클러스터 검증 환경 구축 | GPU Operator, StorageClass, Ingress Controller 확인 | 1일 | Infra |
| 10 | Infra: Memcached 셰이더 캐시 서버 배포 | Memcached Pod + Service 배포, AUTO_ENABLE_DRIVER_SHADER_CACHE_WRAPPER 환경변수 주입 | 1일 | Infra |
| 11 | 통합 테스트 | Astrago UI → Pod 생성 → 접속 → 삭제 E2E 검증 | 2일 | All |
| **합계** | | | **~18일 (3.5주)** | |

---

## 3. 영향도 분석

### 영향도 등급

| 등급 | 판단 근거 | 주의사항 |
|---|---|---|
| `높음` | 새 워크로드 타입(ISAAC_LAB) 추가로 Backend Core의 WorkloadJobType, WorkloadService, PodTemplateBuilder, IngressBuilder 수정 필요 | 기존 BATCH, INTERACTIVE, SERVING 워크로드에 영향 없는지 회귀 테스트 필수 |

### 영향 범위

| 모듈/파일 | 변경 내용 | 영향 범위 |
|---|---|---|
| WorkloadJobType.kt | ISAAC_LAB enum 추가 | 워크로드 타입 분기 전체 |
| WorkloadService.kt | ISAAC_LAB 타입 처리 로직 | 워크로드 CRUD |
| PodTemplateBuilder.kt | 3-컨테이너 Pod 템플릿, hostPort 동적 할당 | Pod 생성 로직 |
| IngressBuilder.kt | WebSocket annotation 자동 추가, 사용자별 host 생성 | Ingress 생성 |
| FE: 워크로드 생성 폼 | Isaac Lab 타입 선택 + GPU/스토리지 입력 | 워크로드 생성 UI |
| FE: 워크로드 상세 | VSCode/Viewer 접속 버튼 | 워크로드 상세 페이지 |

---

## 4. 리스크 / 제약사항

### 기술적 리스크

- **셰이더 캐시 관리 (프로덕션 크리티컬)**: Isaac Sim은 최초 실행 시 RTX 셰이더 컴파일에 ~430초 소요.

  **캐시 방식별 비교:**

  | 방식 | 캐시 손상 시 | 사용자 개입 | 사용자 간 공유 | 구현 난이도 |
  |------|------------|-----------|--------------|-----------|
  | emptyDir (현재) | Pod 재시작으로 자동 해소 | 없음 | X | 없음 (매번 430초) |
  | PVC | 사용자가 PVC 초기화 불가 → 관리자 호출 | 필요 | X | 낮음 |
  | **Memcached (확정)** | **캐시 미스 → 자동 재컴파일** | **없음** | **O** | 중간 |

  **확정안: Memcached 셰이더 캐시 서버 도입**

  Kit SDK 연동 메커니즘:
  - Extension: `omni.hsscclient` (Kit 106+에서 자동 동작)
  - 환경변수: `AUTO_ENABLE_DRIVER_SHADER_CACHE_WRAPPER=hsscdns://<memcached-service-dns>`

  인프라 구성:
  ```
  [Memcached Pod — 상시 실행, 1개]
    ├── image: memcached:1.6
    ├── 메모리: 8~16Gi
    └── Service: memcached.isaac-launchable.svc.cluster.local:11211

  [Isaac Lab Pod — 사용자당 1개]
    └── env: AUTO_ENABLE_DRIVER_SHADER_CACHE_WRAPPER=hsscdns://memcached.isaac-launchable.svc.cluster.local
  ```

- **Isaac Sim 콜드 스타트 (~430초)**: 최초 실행 시 extensions 다운로드 + shader 컴파일로 약 7분 소요.
- **WebRTC 단일 활성 스트림 제한**: Isaac Sim은 단일 활성 제어 스트림만 지원.
- **isaac-lab:3.0.0-beta1 이미지에 code-server 미포함**: isaac-launchable-vscode 이미지를 Isaac Sim 6 기반으로 리빌드 필요.

### 고객사 제약사항

- **GPU 제한**: ws-node074에 RTX A6000 2장. 동시 사용자 수 = GPU 수로 제한.
- **네트워크**: 폐쇄망 환경 — NGC에서 이미지 직접 pull 불가, Harbor(10.61.3.124:30002) 경유 필수.
- **Ingress IP 제한**: 현재 nginx-ovas(10.61.3.125), nginx-community(10.61.3.126) 2개.

---

## 5. 완료 조건

해당 프로젝트:
- [ ] Astrago UI에서 Isaac Lab 워크로드 생성 시 3-컨테이너 Pod가 정상 배포되고 3/3 Running 상태
- [ ] 브라우저에서 VSCode(/) 접속 가능, 터미널에서 Isaac Sim 실행 가능
- [ ] 브라우저에서 Viewer(/viewer/) 접속 시 WebRTC 스트리밍 정상 동작
- [ ] 같은 노드에 2개 이상 Pod 동시 배포 시 포트 충돌 없이 독립 동작
- [ ] 워크로드 삭제 시 Pod, Service, Ingress, ConfigMap 전체 정리
- [ ] 셰이더 캐시 PVC가 워크로드 재생성 시에도 보존되어 2차 실행 시 콜드 스타트 시간 단축

---

## 참고 문서

| 문서 | 위치 |
|---|---|
| Isaac Launchable K8s 배포 가이드 | [docs/isaac-sim-deploy.md](isaac-sim-deploy.md) |
| USD Composer 배포 가이드 | [docs/usd-composer-deploy.md](usd-composer-deploy.md) |
| MinIO 설치 가이드 | [docs/minio-install.md](minio-install.md) |
| Isaac Launchable GitHub | https://github.com/isaac-sim/isaac-launchable |
