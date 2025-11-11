#!/bin/bash

# MC-Kube CPU Agent - 통합 HTTP API 모드 빌드 및 배포 스크립트

set -e

echo "=== MC-Kube CPU Agent - 통합 HTTP API 모드 배포 시작 ==="

# 1. Go 의존성 업데이트
echo "1. Go 의존성 업데이트..."
go mod tidy

# 2. 통합 이미지 빌드 (API 서버 + CPU 모니터링)
echo "2. 통합 이미지 빌드..."
docker build -t noru0817/cpu_util_sender:0.0.8 .

# 3. 이미지 푸시 (선택사항)
read -p "Docker Hub에 이미지를 푸시하시겠습니까? (y/n): " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo "3. 이미지 푸시..."
    docker push noru0817/cpu_util_sender:0.0.8
else
    echo "3. 이미지 푸시 건너뜀"
fi

# 4. 기존 DaemonSet 정리
echo "4. 기존 DaemonSet 정리..."
kubectl delete daemonset mckube-cpu-agent -n mc-kube-system --ignore-not-found=true
kubectl delete daemonset mckube-api-server -n mc-kube-system --ignore-not-found=true

# 잠시 대기
echo "잠시 대기 중..."
sleep 5

# 5. RBAC 설정 적용
echo "5. RBAC 설정 적용..."
kubectl apply -f setup/rbac.yaml

# 6. 통합 DaemonSet 배포
echo "6. 통합 DaemonSet 배포..."
kubectl apply -f setup/daemonset.yaml

# 7. 배포 상태 확인
echo "7. 배포 상태 확인..."
echo
echo "CPU Agent 상태 (API 서버 포함):"
kubectl get daemonset mckube-cpu-agent -n mc-kube-system
echo
echo "Pod 상태:"
kubectl get pods -l app=mckube-cpu-agent -n mc-kube-system

# 8. 포트 확인
echo
echo "8. 포트 8888 사용 확인..."
sleep 10  # Pod가 시작될 때까지 대기
ss -tuln | grep :8888 || echo "포트 8888이 아직 사용 중이 아닙니다. 잠시 후 다시 확인해주세요."

echo
echo "=== 배포 완료 ==="
echo "통합 CPU Agent가 각 노드의 포트 8888에서 실행됩니다."
echo "- CPU 모니터링: 백그라운드에서 실행"
echo "- API 서버: 8888 포트에서 HTTP API 제공"
echo
echo "테스트 방법:"
echo "  # API 서버 헬스체크"
echo "  curl http://localhost:8888/health"
echo
echo "  # CPU 사용률 확인 (노드 annotation)"
echo "  kubectl get node \$(hostname) -o jsonpath='{.metadata.annotations}' | jq ."
echo
echo "  # 로그 확인"
echo "  kubectl logs -l app=mckube-cpu-agent -n mc-kube-system"