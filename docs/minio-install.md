# MinIO Helm 설치 가이드

> 날짜: 2026-04-10
> 클러스터: root@10.61.3.75 (k0s, astrago)
> 네임스페이스: minio

---

## 목적

Astrago 인프라 클러스터에 S3 호환 오브젝트 스토리지(MinIO)를 Helm으로 설치하고,
MetalLB LoadBalancer를 통해 `10.61.3.117`로 외부 접근 가능하게 구성한다.

## 아키텍처

```
사용자
  │
  ├── http://10.61.3.117:9000  →  MinIO API (S3 호환)
  └── http://10.61.3.117:9001  →  MinIO Console (웹 UI)
        │
        └── LoadBalancer Service (MetalLB minio-pool)
              │
              └── minio Pod (standalone, ws-node074)
                    └── PVC: 100Gi (astrago-nfs-csi)
```

## 구성 요소

| 항목 | 값 |
|------|-----|
| Helm repo | `https://charts.min.io/` |
| 모드 | standalone |
| 네임스페이스 | `minio` |
| StorageClass | `astrago-nfs-csi` |
| PVC 크기 | 100Gi |
| 서비스 타입 | LoadBalancer (MetalLB) |
| 외부 IP | 10.61.3.117 |
| API 포트 | 9000 |
| Console 포트 | 9001 |
| 초기 계정 | minioadmin / minioadmin |

## MetalLB IP Pool

`minio-pool` IPAddressPool을 생성하여 10.61.3.117/32를 할당한다.
`autoAssign: false`로 다른 서비스의 자동 점유를 방지한다.

```yaml
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: minio-pool
  namespace: metallb-system
spec:
  addresses:
  - 10.61.3.117/32
  autoAssign: false
```

## Helm 설치

```bash
# kubeconfig 설정
export KUBECONFIG=/tmp/k0s-admin.conf

# Helm repo 추가
helm repo add minio https://charts.min.io/
helm repo update

# values.yaml 작성 후 설치
helm install minio minio/minio \
  -n minio --create-namespace \
  -f values.yaml
```

## values.yaml

```yaml
mode: standalone
rootUser: minioadmin
rootPassword: minioadmin

persistence:
  enabled: true
  storageClass: astrago-nfs-csi
  size: 100Gi

service:
  type: LoadBalancer
  port: 9000
  annotations:
    metallb.io/allow-shared-ip: "minio-shared"
    metallb.io/loadBalancerIPs: "10.61.3.117"

consoleService:
  type: LoadBalancer
  port: 9001
  annotations:
    metallb.io/allow-shared-ip: "minio-shared"
    metallb.io/loadBalancerIPs: "10.61.3.117"

resources:
  requests:
    memory: 512Mi
    cpu: 250m
```

---

## CSI-S3 연동 (yandex-cloud/k8s-csi-s3)

MinIO S3 버킷을 K8s PersistentVolume으로 마운트하는 방법.

### 사전 조건

1. csi-s3 드라이버 설치 (kube-system 네임스페이스)
2. csi-s3-secret 생성

```bash
kubectl create secret generic csi-s3-secret \
  -n kube-system \
  --from-literal=accessKeyID=minioadmin \
  --from-literal=secretAccessKey=minioadmin \
  --from-literal=endpoint=http://10.61.3.117:9000 \
  --from-literal=region=us-east-1
```

### StorageClass

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: csi-s3-minio
provisioner: ru.yandex.s3.csi
reclaimPolicy: Retain
volumeBindingMode: Immediate
allowVolumeExpansion: true
parameters:
  mounter: geesefs
  options: "--memory-limit 1000 --dir-mode 0777 --file-mode 0666"
  csi.storage.k8s.io/provisioner-secret-name: csi-s3-secret
  csi.storage.k8s.io/provisioner-secret-namespace: kube-system
  csi.storage.k8s.io/node-stage-secret-name: csi-s3-secret
  csi.storage.k8s.io/node-stage-secret-namespace: kube-system
  csi.storage.k8s.io/node-publish-secret-name: csi-s3-secret
  csi.storage.k8s.io/node-publish-secret-namespace: kube-system
```

### PersistentVolume (정적 바인딩)

```yaml
apiVersion: v1
kind: PersistentVolume
metadata:
  name: minio-omniverse-pv
spec:
  storageClassName: csi-s3-minio
  capacity:
    storage: 100Gi
  accessModes:
  - ReadWriteMany
  persistentVolumeReclaimPolicy: Retain
  csi:
    driver: ru.yandex.s3.csi
    volumeHandle: omniverse   # CRITICAL: 실제 버킷 이름과 일치해야 함
    volumeAttributes:
      bucket: omniverse
      mounter: geesefs
      options: "--memory-limit 1000 --dir-mode 0777 --file-mode 0666"
    nodePublishSecretRef:
      name: csi-s3-secret
      namespace: kube-system
    nodeStageSecretRef:
      name: csi-s3-secret
      namespace: kube-system
    controllerPublishSecretRef:
      name: csi-s3-secret
      namespace: kube-system
```

### PersistentVolumeClaim

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: minio-omniverse-pvc
  namespace: isaac-launchable
spec:
  storageClassName: csi-s3-minio
  volumeName: minio-omniverse-pv
  accessModes:
  - ReadWriteMany
  resources:
    requests:
      storage: 100Gi
```

### Pod volumeMount

```yaml
volumeMounts:
- name: minio-omniverse
  mountPath: /mnt/minio
volumes:
- name: minio-omniverse
  persistentVolumeClaim:
    claimName: minio-omniverse-pvc
```

---

## 트러블슈팅

### `ls /mnt/minio` hang (geesefs)

**원인:** `volumeHandle`이 실제 MinIO 버킷 이름과 다름. geesefs는 volumeHandle을 버킷 이름으로 사용.

**해결:** PV의 `volumeHandle`을 실제 버킷 이름(`omniverse`)으로 변경 후 PV/PVC 재생성.

```bash
# PVC 강제 삭제 (Terminating stuck 시)
kubectl patch pvc minio-omniverse-pvc -n isaac-launchable \
  -p '{"metadata":{"finalizers":[]}}' --type=merge
```

### CSI endpoint empty 오류

**원인:** `nodeStageSecretRef`, `controllerPublishSecretRef` 누락.

**해결:** PV spec에 3개 secretRef 모두 추가:
- `nodePublishSecretRef`
- `nodeStageSecretRef`
- `controllerPublishSecretRef`

### StorageClass 변경 불가

**원인:** `helm upgrade`로 StorageClass 파라미터 변경 불가.

**해결:**
```bash
kubectl delete storageclass csi-s3-minio
kubectl apply -f k8s/storage/storageclass.yaml
```

---

## 관련 문서

- [USD Composer 배포 가이드](usd-composer-deploy.md)
- [Isaac Sim 배포 가이드](isaac-sim-deploy.md)
- CSI-S3 GitHub: https://github.com/yandex-cloud/k8s-csi-s3
