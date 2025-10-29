#!/bin/bash

# MC-Kube CPU Agent DaemonSet 배포 해제 스크립트

set -e

echo "=== MC-Kube CPU Agent DaemonSet 배포 해제 시작 ==="

# 현재 디렉토리 확인
if [ ! -f "main.go" ]; then
    echo "Error: main.go 파일이 현재 디렉토리에 없습니다."
    echo "cpu_util_sender 디렉토리에서 실행해주세요."
    exit 1
fi

# 배포 해제 전 현재 상태 확인
echo "1. 현재 배포 상태 확인 중..."
echo ""
echo "현재 Pod 상태:"
kubectl get pods -l app=mckube-cpu-agent -o wide 2>/dev/null || echo "  - Pod가 없습니다."

echo ""
echo "현재 DaemonSet 상태:"
kubectl get daemonset mckube-cpu-agent 2>/dev/null || echo "  - DaemonSet이 없습니다."

echo ""

# DaemonSet 삭제
echo "2. DaemonSet 삭제 중..."
if kubectl get daemonset mckube-cpu-agent >/dev/null 2>&1; then
    kubectl delete -f setup/daemonset.yaml
    if [ $? -eq 0 ]; then
        echo "✓ DaemonSet 삭제 완료"
    else
        echo "✗ DaemonSet 삭제 실패"
        exit 1
    fi
else
    echo "- DaemonSet이 이미 존재하지 않습니다."
fi

# RBAC 삭제
echo "3. RBAC 설정 삭제 중..."
if kubectl get serviceaccount mckube-cpu-agent >/dev/null 2>&1; then
    kubectl delete -f setup/rbac.yaml
    if [ $? -eq 0 ]; then
        echo "✓ RBAC 설정 삭제 완료"
    else
        echo "✗ RBAC 설정 삭제 실패"
        exit 1
    fi
else
    echo "- RBAC 설정이 이미 존재하지 않습니다."
fi

# Pod 완전 삭제 대기
echo "4. Pod 완전 삭제 대기 중..."
timeout=60
elapsed=0
while kubectl get pods -l app=mckube-cpu-agent 2>/dev/null | grep -q mckube-cpu-agent; do
    if [ $elapsed -ge $timeout ]; then
        echo "⚠ 경고: Pod 삭제가 $timeout초를 초과했습니다."
        echo "남은 Pod 목록:"
        kubectl get pods -l app=mckube-cpu-agent
        break
    fi
    echo "  - Pod 삭제 대기 중... ($elapsed/$timeout초)"
    sleep 5
    elapsed=$((elapsed + 5))
done

# Docker 이미지 삭제 옵션
echo ""
read -p "5. Docker 이미지도 삭제하시겠습니까? (y/N): " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo "Docker 이미지 삭제 중..."
    docker rmi noru0817/cpu_util_sender:0.0.1 2>/dev/null || echo "  - 이미지가 이미 존재하지 않습니다."
    echo "✓ Docker 이미지 삭제 완료"
else
    echo "- Docker 이미지는 유지됩니다."
fi

# 최종 상태 확인
echo ""
echo "6. 배포 해제 완료 확인..."
echo ""
echo "남은 Pod 상태:"
kubectl get pods -l app=mckube-cpu-agent 2>/dev/null || echo "  - Pod가 없습니다. ✓"

echo ""
echo "남은 DaemonSet 상태:"
kubectl get daemonset mckube-cpu-agent 2>/dev/null || echo "  - DaemonSet이 없습니다. ✓"

echo ""
echo "남은 RBAC 리소스:"
kubectl get serviceaccount mckube-cpu-agent 2>/dev/null || echo "  - ServiceAccount가 없습니다. ✓"
kubectl get clusterrole mckube-cpu-agent 2>/dev/null || echo "  - ClusterRole이 없습니다. ✓"
kubectl get clusterrolebinding mckube-cpu-agent 2>/dev/null || echo "  - ClusterRoleBinding이 없습니다. ✓"

echo ""
echo "=== 배포 해제 완료 ==="
echo ""
echo "유용한 명령어들:"
echo "  - 다시 배포: ./deploy.sh"
echo "  - 전체 리소스 확인: kubectl get all -A | grep mckube"
echo "  - Docker 이미지 확인: docker images | grep cpu_util_sender"