# MC-Kube CPU Agent DaemonSet

이 디렉토리는 MC-Kube 시스템의 CPU 모니터링 에이전트를 Kubernetes DaemonSet으로 배포하기 위한 파일들을 포함합니다.

## 파일 구조

```
cpu_util_sender/
├── main.go           # CPU 모니터링 에이전트 Go 소스코드
├── go.mod            # Go 모듈 의존성
├── go.sum            # Go 모듈 체크섬
├── Dockerfile        # Docker 이미지 빌드를 위한 파일
├── deploy.sh         # 자동 배포 스크립트
├── README.md         # 이 문서
└── setup/            # Kubernetes 배포 설정 파일들
    ├── rbac.yaml     # Kubernetes RBAC 설정
    └── daemonset.yaml # DaemonSet 배포 설정
```

## 기능 설명

CPU 에이전트는 다음과 같은 작업을 수행합니다:

1. **CPU 사용률 모니터링**: `/proc/stat`에서 CPU 사용률을 1초마다 수집
2. **노드 어노테이션 업데이트**: Kubernetes API를 통해 노드의 `node.mckube.io/cpu-usage` 어노테이션 업데이트
3. **실시간 데이터 제공**: MC-Kube 컨트롤러가 CPU 압박 상황을 감지할 수 있도록 데이터 제공

## 전제 조건

- Kubernetes 클러스터가 실행 중이어야 함
- Docker가 설치되어 있어야 함
- kubectl이 설정되어 있어야 함
- 클러스터에 대한 admin 권한 필요

## 빠른 시작

### 1. 자동 배포 (권장)

```bash
cd /home/ice-sub-04/MC-Kube-proto/cpu_util_sender
./deploy.sh
```

### 2. 수동 배포

```bash
# 1. Docker 이미지 빌드
docker build -t mckube-cpu-agent:latest .

# 2. RBAC 설정 적용
kubectl apply -f setup/rbac.yaml

# 3. DaemonSet 배포
kubectl apply -f setup/daemonset.yaml
```

## 배포 확인

```bash
# Pod 상태 확인
kubectl get pods -l app=mckube-cpu-agent -o wide

# DaemonSet 상태 확인
kubectl get daemonset mckube-cpu-agent

# 로그 확인
kubectl logs -l app=mckube-cpu-agent

# 노드 어노테이션 확인
kubectl get nodes -o custom-columns="NAME:.metadata.name,CPU-USAGE:.metadata.annotations.node\.mckube\.io/cpu-usage"
```

## 설정 사항

### DaemonSet 설정

- **ServiceAccount**: `mckube-cpu-agent`
- **Tolerations**: 마스터 노드에도 배포되도록 설정
- **Host 접근**: `/proc`과 `/sys` 마운트
- **리소스 제한**: CPU 200m, Memory 128Mi

### RBAC 권한

- **nodes**: get, list, patch, update
- 노드 어노테이션 수정을 위한 최소 권한

## 트러블슈팅

### 1. Pod가 시작되지 않는 경우

```bash
# Pod 상태 상세 확인
kubectl describe pods -l app=mckube-cpu-agent

# 이벤트 확인
kubectl get events --sort-by=.metadata.creationTimestamp
```

### 2. 권한 오류

```bash
# ServiceAccount 확인
kubectl get serviceaccount mckube-cpu-agent

# ClusterRoleBinding 확인
kubectl get clusterrolebinding mckube-cpu-agent
```

### 3. CPU 데이터가 업데이트되지 않는 경우

```bash
# 로그 확인
kubectl logs -l app=mckube-cpu-agent --tail=50

# 노드 어노테이션 확인
kubectl describe node <node-name>
```

## 삭제

```bash
# DaemonSet 삭제
kubectl delete -f setup/daemonset.yaml

# RBAC 설정 삭제
kubectl delete -f setup/rbac.yaml

# Docker 이미지 삭제 (선택사항)
docker rmi noru0817/cpu_util_sender:0.0.1
```

## 개발자 노트

### 코드 수정 후 재배포

```bash
# 코드 수정 후
docker build -t mckube-cpu-agent:latest .
kubectl rollout restart daemonset mckube-cpu-agent
```

### 디버깅

```bash
# 특정 노드의 Pod에 접속
kubectl exec -it <pod-name> -- /bin/sh

# /proc/stat 직접 확인
kubectl exec -it <pod-name> -- cat /proc/stat
```