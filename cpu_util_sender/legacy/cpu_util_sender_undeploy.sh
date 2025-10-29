#!/usr/bin/env bash
set -euo pipefail

# ===============================
# cpu-util-sender systemd uninstaller
# ===============================

SERVICE_NAME="cpu-util-sender"
BIN_PATH="/usr/local/bin/${SERVICE_NAME}"
ENV_DIR="/etc/${SERVICE_NAME}"
ENV_FILE="${ENV_DIR}/env"
UNIT_FILE="/etc/systemd/system/${SERVICE_NAME}.service"

log() { echo -e "[${SERVICE_NAME}] $*"; }
warn() { echo -e "[${SERVICE_NAME}] WARNING: $*" >&2; }

# ---- prerequisites
command -v systemctl >/dev/null 2>&1 || { warn "systemd가 없습니다. 수동 정리가 필요할 수 있습니다."; }
command -v sudo >/dev/null 2>&1 || { warn "sudo가 없습니다. root 권한으로 실행하세요."; }

log "cpu-util-sender 서비스 제거 시작..."

# ---- stop and disable service
if systemctl is-active --quiet "${SERVICE_NAME}" 2>/dev/null; then
    log "서비스 중지: ${SERVICE_NAME}"
    sudo systemctl stop "${SERVICE_NAME}" || warn "서비스 중지 실패"
else
    log "서비스가 이미 중지된 상태입니다."
fi

if systemctl is-enabled --quiet "${SERVICE_NAME}" 2>/dev/null; then
    log "서비스 비활성화: ${SERVICE_NAME}"
    sudo systemctl disable "${SERVICE_NAME}" || warn "서비스 비활성화 실패"
else
    log "서비스가 이미 비활성화된 상태입니다."
fi

# ---- remove systemd unit file
if [[ -f "${UNIT_FILE}" ]]; then
    log "systemd 유닛 파일 제거: ${UNIT_FILE}"
    sudo rm -f "${UNIT_FILE}" || warn "유닛 파일 제거 실패"
else
    log "systemd 유닛 파일이 이미 없습니다."
fi

# ---- remove binary
if [[ -f "${BIN_PATH}" ]]; then
    log "바이너리 제거: ${BIN_PATH}"
    sudo rm -f "${BIN_PATH}" || warn "바이너리 제거 실패"
else
    log "바이너리가 이미 없습니다."
fi

# ---- remove environment files
if [[ -d "${ENV_DIR}" ]]; then
    log "환경 디렉토리 제거: ${ENV_DIR}"
    sudo rm -rf "${ENV_DIR}" || warn "환경 디렉토리 제거 실패"
else
    log "환경 디렉토리가 이미 없습니다."
fi

# ---- reload systemd
log "systemd 데몬 리로드"
sudo systemctl daemon-reload || warn "systemd 리로드 실패"

# ---- final check
log "제거 완료 확인..."
if systemctl list-unit-files | grep -q "${SERVICE_NAME}"; then
    warn "서비스가 여전히 systemd에 등록되어 있습니다."
else
    log "서비스가 성공적으로 제거되었습니다."
fi

if [[ -f "${BIN_PATH}" ]] || [[ -f "${UNIT_FILE}" ]] || [[ -d "${ENV_DIR}" ]]; then
    warn "일부 파일이 여전히 남아있습니다. 수동 확인이 필요합니다."
    echo "  Binary: ${BIN_PATH}"
    echo "  Unit:   ${UNIT_FILE}"
    echo "  Config: ${ENV_DIR}"
else
    log "모든 파일이 성공적으로 제거되었습니다."
fi

log "제거 완료 ✅"
echo ""
echo "제거된 구성요소:"
echo "  • systemd 서비스: ${SERVICE_NAME}"
echo "  • 바이너리: ${BIN_PATH}"
echo "  • 환경 설정: ${ENV_DIR}/"
echo "  • 유닛 파일: ${UNIT_FILE}"
echo ""
echo "확인 명령어:"
echo "  sudo systemctl status ${SERVICE_NAME}  # 서비스 상태 (실패해야 정상)"
echo "  sudo systemctl list-unit-files | grep ${SERVICE_NAME}  # 등록 확인 (없어야 정상)"