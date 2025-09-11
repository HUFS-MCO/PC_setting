#!/bin/bash

set -e

echo "🧪 [1/5] 테스트 실행 중..."
go test ./...

echo "📦 [2/5] renicer-daemon 빌드 및 설치 중..."
go build -o renicer-daemon main.go
sudo cp renicer-daemon /usr/local/bin/
sudo chmod +x /usr/local/bin/renicer-daemon

echo "🛠 [3/5] systemd 서비스 파일 구성..."
cat <<EOF | sudo tee /etc/systemd/system/renicer.service > /dev/null
[Unit]
Description=Renicer Daemon for Container Nice Adjustment
After=network.target

[Service]
ExecStart=/usr/local/bin/renicer-daemon
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

echo "🔧 [4/5] 필수 도구 확인 중..."

# jq 설치
if ! command -v jq &> /dev/null; then
  echo "📥 jq 설치 중..."
  sudo apt-get install -y jq
else
  echo "✅ jq 이미 설치됨"
fi

echo "🚀 [5/5] systemd 서비스 시작..."
sudo systemctl daemon-reexec
sudo systemctl daemon-reload
sudo systemctl enable renicer
sudo systemctl restart renicer
sudo systemctl status renicer --no-pager

echo "✅ renicer 설치 및 실행 완료!"
