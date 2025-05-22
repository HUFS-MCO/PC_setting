#!/bin/bash

set -e

echo "📦 [1/4] renicer-daemon 설치 중..."
sudo cp renicer-daemon /usr/local/bin/
sudo chmod +x /usr/local/bin/renicer-daemon

echo "🛠 [2/4] systemd 서비스 파일 구성..."
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
sudo systemctl enable renicer
sudo systemctl restart renicer
sudo systemctl status renicer --no-pager

echo "✅ renicer 설치 및 실행 완료!"
