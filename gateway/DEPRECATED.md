# DEPRECATED — Node.js NVST signaling proxy

이 디렉토리의 Node.js gateway 는 더 이상 사용되지 않습니다.

## 폐기 사유

**streamPort 는 advertise + 실제 UDP bind 둘 다 결정하므로 별도 signaling proxy 가 불필요합니다.** 초기 설계 가정 ("Kit 의 publicIp 와 실제 UDP bind 포트가 분리됨") 이 거짓이었음을 코드 분석 + 실측 검증으로 확인.

해결 방식:
- `runheadless.sh` / IsaacLab `--kit_args` 에 `publicIp / streamPort / signalPort` 직접 주입
- deployment 의 hostPort 30998 (UDP) + 49100 (TCP) 매핑
- nginx `/sign_in → localhost:49100` 직결 (gateway 우회)

## 대체 경로

`k8s/base/configmaps.yaml` 의 `runheadless-script-0` + `nginx-config-0` 의 `/sign_in` 라인 참고.

## 관련 메모리
- `~/.claude/projects/-Users-xiilab-git-HAMi/memory/project_isaac_sim_streamport_truth.md`
