# MC-Kube CPU Agent - HTTP API 모드# MC-Kube CPU Agent DaemonSet



이 디렉토리는 MC-Kube 시스템의 CPU 모니터링 에이전트를 Kubernetes DaemonSet으로 배포하기 위한 파일들을 포함합니다.이 디렉토리는 MC-Kube 시스템의 CPU 모니터링 에이전트를 Kubernetes DaemonSet으로 배포하기 위한 파일들을 포함합니다.



## 주요 특징## 파일 구조



- **통합 구조**: API 서버와 CPU 모니터링이 하나의 Pod에서 실행```

- **HTTP API 통신**: 8888 포트를 통한 node annotation 업데이트cpu_util_sender/

- **mc-kube-system 네임스페이스**: 시스템 레벨 배포├── main.go           # CPU 모니터링 에이전트 Go 소스코드

- **실시간 모니터링**: 1초마다 CPU 사용률 측정├── go.mod            # Go 모듈 의존성

├── go.sum            # Go 모듈 체크섬

## 디렉토리 구조├── Dockerfile        # Docker 이미지 빌드를 위한 파일

├── deploy.sh         # 자동 배포 스크립트

```├── README.md         # 이 문서

cpu_util_sender/└── setup/            # Kubernetes 배포 설정 파일들

├── main.go                # 통합 CPU 에이전트 + API 서버    ├── rbac.yaml     # Kubernetes RBAC 설정

├── deploy-http.sh         # HTTP API 모드 배포 스크립트    └── daemonset.yaml # DaemonSet 배포 설정

├── Dockerfile            # Docker 이미지 빌드```

├── go.mod, go.sum        # Go 모듈 의존성

├── setup/## 기능 설명

│   ├── daemonset.yaml    # DaemonSet 배포 설정 (mc-kube-system)

│   └── rbac.yaml         # ServiceAccount 및 권한 설정CPU 에이전트는 다음과 같은 작업을 수행합니다:

└── legacy/               # 구버전 파일들 (사용 안함)

```1. **CPU 사용률 모니터링**: `/proc/stat`에서 CPU 사용률을 1초마다 수집

2. **노드 어노테이션 업데이트**: Kubernetes API를 통해 노드의 `node.mckube.io/cpu-usage` 어노테이션 업데이트

## 사용법3. **실시간 데이터 제공**: MC-Kube 컨트롤러가 CPU 압박 상황을 감지할 수 있도록 데이터 제공



### 1. 의존성 설치## 전제 조건



```bash- Kubernetes 클러스터가 실행 중이어야 함

go mod tidy- Docker가 설치되어 있어야 함

```- kubectl이 설정되어 있어야 함

- 클러스터에 대한 admin 권한 필요

### 2. 배포

## 빠른 시작

```bash

./deploy-http.sh### 1. 자동 배포 (권장)

```

```bash

### 3. 상태 확인cd /home/ice-sub-04/MC-Kube-proto/cpu_util_sender

./deploy.sh

```bash```

# Pod 상태 확인

kubectl get pods -l app=mckube-cpu-agent -n mc-kube-system### 2. 수동 배포



# API 서버 헬스체크```bash

curl http://localhost:8888/health# 1. Docker 이미지 빌드

docker build -t mckube-cpu-agent:latest .

# 로그 확인

kubectl logs -l app=mckube-cpu-agent -n mc-kube-system# 2. RBAC 설정 적용

kubectl apply -f setup/rbac.yaml

# 노드 annotation 확인

kubectl get node $(hostname) -o jsonpath='{.metadata.annotations}' | jq .# 3. DaemonSet 배포

```kubectl apply -f setup/daemonset.yaml

```

## API 엔드포인트

## 배포 확인

- `GET /health` - 헬스체크

- `POST/PATCH /api/v1/nodes/{nodeName}/annotations` - Node annotation 업데이트```bash

# Pod 상태 확인

## 모니터링 데이터kubectl get pods -l app=mckube-cpu-agent -o wide



CPU 에이전트는 다음 annotation을 실시간으로 업데이트합니다:# DaemonSet 상태 확인

kubectl get daemonset mckube-cpu-agent

- `mckube.sdv.com/cpu-usage`: CPU 사용률 (%)

- `mckube.sdv.com/cpu-over90-duration-s`: 90% 이상 지속 시간 (초)# 로그 확인

- `mckube.sdv.com/isCpuBusy`: CPU 압박 상태 (true/false)kubectl logs -l app=mckube-cpu-agent



## 기능 설명# 노드 어노테이션 확인

kubectl get nodes -o custom-columns="NAME:.metadata.name,CPU-USAGE:.metadata.annotations.node\.mckube\.io/cpu-usage"

### CPU 모니터링```

- 1초마다 `/proc/stat`에서 CPU 사용률 측정

- 90% 이상 사용 시간 추적## 설정 사항

- HTTP API를 통해 로컬 서버로 데이터 전송

### DaemonSet 설정

### HTTP API 서버

- 포트 8888에서 HTTP 서버 실행- **ServiceAccount**: `mckube-cpu-agent`

- Node annotation 업데이트 API 제공- **Tolerations**: 마스터 노드에도 배포되도록 설정

- kubectl 명령어를 통한 실제 Kubernetes API 호출- **Host 접근**: `/proc`과 `/sys` 마운트

- **리소스 제한**: CPU 200m, Memory 128Mi

## 전제 조건

### RBAC 권한

- Kubernetes 클러스터가 실행 중이어야 함

- Docker가 설치되어 있어야 함- **nodes**: get, list, patch, update

- kubectl이 설정되어 있어야 함- 노드 어노테이션 수정을 위한 최소 권한

- mc-kube-system 네임스페이스에 대한 권한 필요

## 트러블슈팅

## 트러블슈팅

### 1. Pod가 시작되지 않는 경우

### 1. Pod가 시작되지 않는 경우

```bash

```bash# Pod 상태 상세 확인

# Pod 상태 상세 확인kubectl describe pods -l app=mckube-cpu-agent

kubectl describe pods -l app=mckube-cpu-agent -n mc-kube-system

# 이벤트 확인

# 이벤트 확인kubectl get events --sort-by=.metadata.creationTimestamp

kubectl get events -n mc-kube-system --sort-by=.metadata.creationTimestamp```

```

### 2. 권한 오류

### 2. API 서버 접근 불가

```bash

```bash# ServiceAccount 확인

# API 서버 헬스체크kubectl get serviceaccount mckube-cpu-agent

curl http://localhost:8888/health

# ClusterRoleBinding 확인

# 포트 사용 확인kubectl get clusterrolebinding mckube-cpu-agent

netstat -tulpn | grep 8888```

```

### 3. CPU 데이터가 업데이트되지 않는 경우

### 3. Annotation 업데이트 안됨

```bash

```bash# 로그 확인

# 로그 확인kubectl logs -l app=mckube-cpu-agent --tail=50

kubectl logs -l app=mckube-cpu-agent -n mc-kube-system --tail=50

# 노드 어노테이션 확인

# RBAC 권한 확인kubectl describe node <node-name>

kubectl get clusterrolebinding mckube-cpu-agent```

```

## 삭제

## 삭제

```bash

```bash# DaemonSet 삭제

# DaemonSet 삭제kubectl delete -f setup/daemonset.yaml

kubectl delete -f setup/daemonset.yaml

# RBAC 설정 삭제

# RBAC 설정 삭제kubectl delete -f setup/rbac.yaml

kubectl delete -f setup/rbac.yaml

# Docker 이미지 삭제 (선택사항)

# 네임스페이스 삭제 (주의: 다른 리소스도 함께 삭제됨)docker rmi noru0817/cpu_util_sender:0.0.1

kubectl delete namespace mc-kube-system```

```

## 개발자 노트

## 개발자 노트

### 코드 수정 후 재배포

### 코드 수정 후 재배포

```bash

```bash# 코드 수정 후

# 이미지 빌드 및 배포docker build -t mckube-cpu-agent:latest .

./deploy-http.shkubectl rollout restart daemonset mckube-cpu-agent

``````



### 디버깅### 디버깅



```bash```bash

# Pod 내부 접속# 특정 노드의 Pod에 접속

kubectl exec -it <pod-name> -n mc-kube-system -- /bin/shkubectl exec -it <pod-name> -- /bin/sh



# API 서버 직접 테스트# /proc/stat 직접 확인

kubectl exec -it <pod-name> -n mc-kube-system -- curl http://localhost:8888/healthkubectl exec -it <pod-name> -- cat /proc/stat

``````