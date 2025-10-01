#!/bin/bash

set -e

echo "🧹 [0/4] 기존 resource-daemon 정리 중..."
sudo systemctl stop resource 2>/dev/null || true
sudo systemctl disable resource 2>/dev/null || true
sudo rm -f /usr/local/bin/resource-daemon || true
sudo rm -f /etc/systemd/system/resource.service || true

echo "📦 [1/4] resource-daemon 빌드 및 설치 중..."
go build -o resource-daemon main.go
sudo cp resource-daemon /usr/local/bin/
sudo chmod +x /usr/local/bin/resource-daemon

echo "🛠 [2/4] systemd 서비스 파일 구성..."
cat <<EOF | sudo tee /etc/systemd/system/resource.service > /dev/null
[Unit]
Description=Renicer Daemon for Container Nice Adjustment
After=network.target

[Service]
ExecStart=/usr/local/bin/resource-daemon
Restart=always
RestartSec=2
User=root
StandardOutput=journal
StandardError=journal

# 포트 접근용 보호 해제 (선택)
AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
EOF

echo "🔧 [3/4] 필수 도구 확인 중..."

# jq 설치
if ! command -v jq &> /dev/null; then
  echo "📥 jq 설치 중..."
  sudo apt-get install -y jq
else
  echo "✅ jq 이미 설치됨"
fi

echo "🚀 [4/4] systemd 서비스 시작..."
sudo systemctl daemon-reexec
sudo systemctl daemon-reload
sudo systemctl enable resource
sudo systemctl restart resource
sudo systemctl status resource --no-pager

echo "✅ resource-daemon 설치 및 실행 완료!"
