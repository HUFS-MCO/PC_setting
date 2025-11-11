#!/bin/bash

# MC-Kube CPU Agent DaemonSet 배포 스크립트

set -e

echo "=== MC-Kube CPU Agent DaemonSet 배포 시작 ==="

# 현재 디렉토리 확인
if [ ! -f "main.go" ]; then
    echo "Error: main.go 파일이 현재 디렉토리에 없습니다."
    echo "cpu_util_sender 디렉토리에서 실행해주세요."
    exit 1
fi

# Docker 이미지 빌드
echo "1. Docker 이미지 빌드 중..."
docker build -t noru0817/cpu_util_sender:0.0.1 .
docker push noru0817/cpu_util_sender:0.0.1

if [ $? -eq 0 ]; then
    echo "✓ Docker 이미지 빌드 완료"
else
    echo "✗ Docker 이미지 빌드 실패"
    exit 1
fi

# RBAC 적용
echo "2. RBAC 설정 적용 중..."
kubectl apply -f setup/rbac.yaml

if [ $? -eq 0 ]; then
    echo "✓ RBAC 설정 적용 완료"
else
    echo "✗ RBAC 설정 적용 실패"
    exit 1
fi

# DaemonSet 배포
echo "3. DaemonSet 배포 중..."
kubectl apply -f setup/daemonset.yaml

if [ $? -eq 0 ]; then
    echo "✓ DaemonSet 배포 완료"
else
    echo "✗ DaemonSet 배포 실패"
    exit 1
fi

# 배포 상태 확인
echo "4. 배포 상태 확인 중..."
echo ""
echo "Pod 상태:"
kubectl get pods -l app=mckube-cpu-agent -o wide

echo ""
echo "DaemonSet 상태:"
kubectl get daemonset mckube-cpu-agent

echo ""
echo "=== 배포 완료 ==="
echo ""
echo "유용한 명령어들:"
echo "  - Pod 로그 확인: kubectl logs -l app=mckube-cpu-agent"
echo "  - Pod 상태 확인: kubectl get pods -l app=mckube-cpu-agent"
echo "  - DaemonSet 삭제: kubectl delete -f setup/daemonset.yaml"
echo "  - RBAC 삭제: kubectl delete -f setup/rbac.yaml"