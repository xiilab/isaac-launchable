# DEPRECATED — Pion SFU signaling proxy

이 디렉토리의 Go (Pion) gateway 는 더 이상 사용되지 않습니다.

## 폐기 사유

`gateway/DEPRECATED.md` 와 동일 — streamPort 가 advertise+bind 둘 다 결정하므로 별도 signaling/SFU proxy 가 불필요.

초기 설계의 모든 분기 (NVST peer_msg envelope intercept, codec/ackid/transceiver 처리, audio element 요구사항 우회 등) 는 잘못된 가정 ("Kit 의 광고 포트 ≠ 실제 bind 포트") 에서 출발했고, 그 가정은 거짓.

## 대체 경로

nginx `/sign_in` 이 직접 `localhost:49100` (Kit signaling) 으로 proxy. 별도 Go process 없음.

## 관련 메모리
- `~/.claude/projects/-Users-xiilab-git-HAMi/memory/project_isaac_sim_streamport_truth.md`
