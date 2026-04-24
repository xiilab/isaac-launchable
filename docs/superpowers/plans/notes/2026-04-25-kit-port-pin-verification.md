# Kit port pin verification (2026-04-25)

## Pre-flight (Task 13)

- pod-0 status: `3/3 Running` (gateway 컨테이너 제거 후 vscode+nginx+web-viewer)
- pod-1 status: 변경 없음 (AGE 21h+)
- **포트 핀 적용 방식**: pod-level `securityContext.sysctls.net.ipv4.ip_local_port_range`
  - 초기에는 runheadless.sh 에서 `echo > /proc/sys/...` 로 시도했으나 container runtime 이 `/proc/sys` 를 ro mount → "Read-only file system" 에러
  - NET_ADMIN capability 도 이 mount 제약에는 무효
  - k8s sysctl inject 은 kubelet 이 pod 기동 시점에 host user-ns 권한으로 sysctl 값을 설정 (k0s 에서 safe 분류로 허용)
- `/proc/sys/net/ipv4/ip_local_port_range`:
  ```
  30998	30998
  ```
- Kit signaling TCP :49100 listen 확인:
  ```
  LISTEN 0  64  0.0.0.0:49100  0.0.0.0:*  users:(("kit",pid=80,fd=275))
  ```
- runheadless.sh 로그 첫 줄:
  ```
  [runheadless] ip_local_port_range = 30998	30998
  ```

## Kit bind snapshot (세션 전)

UDP 30998 이 세션 시작 전엔 바인드 안 될 수 있음 (Kit 의 WebRTC 소켓은 세션 시작 시 생성). Task 14 에서 실제 브라우저 세션 진행 중 재확인.

## 관찰된 ICE candidate

(Task 14 에서 기록)
