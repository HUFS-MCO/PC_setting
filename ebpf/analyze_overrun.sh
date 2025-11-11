#!/bin/bash

# Usage: ./analyze_overrun.sh overrun_stats_20251111_120458.log

if [ -z "$1" ]; then
    echo "Usage: $0 <overrun_stats_log_file>"
    exit 1
fi

LOG_FILE="$1"
OUTPUT_FILE="${LOG_FILE%.log}_analyzed.log"

echo "=== HCBS Overrun Analysis with QoS Classes ===" > "$OUTPUT_FILE"
echo "Timestamp: $(date '+%Y-%m-%d %H:%M:%S')" >> "$OUTPUT_FILE"
echo "" >> "$OUTPUT_FILE"

# 통계 초기화
low_count=0
medium_count=0
high_count=0
unknown_count=0

low_overruns=0
medium_overruns=0
high_overruns=0
unknown_overruns=0

echo "Processing containers..." >&2

# 로그 파일에서 컨테이너 ID와 overrun 횟수 추출
while IFS=$'\t' read -r container_id overrun_count rest; do
    # 헤더나 빈 줄 건너뛰기
    if [[ "$container_id" =~ ^(Container|===|Timestamp|Total|Unique|=) ]] || [ -z "$container_id" ]; then
        continue
    fi
    
    # 컨테이너 ID가 64자 hex인지 확인
    if [[ ! "$container_id" =~ ^[a-f0-9]{64}$ ]]; then
        continue
    fi
    
    echo "Checking $container_id..." >&2
    
    # kubectl로 해당 컨테이너를 가진 Pod 찾기
    pod_info=$(kubectl get pods -A -o json | jq -r --arg cid "$container_id" '
        .items[] | 
        select(.status.containerStatuses[]?.containerID? | contains($cid)) |
        "\(.metadata.namespace)|\(.metadata.name)|\(.metadata.labels.app // "none")"
    ' 2>/dev/null | head -1)
    
    if [ -z "$pod_info" ]; then
        qos_class="UNKNOWN"
        pod_name="Not Found"
        namespace="N/A"
        ((unknown_count++))
        ((unknown_overruns+=overrun_count))
    else
        IFS='|' read -r namespace pod_name app_label <<< "$pod_info"
        
        # app label로 QoS 판단
        case "$app_label" in
            *high*)
                qos_class="HIGH"
                ((high_count++))
                ((high_overruns+=overrun_count))
                ;;
            *middle*)
                qos_class="MEDIUM"
                ((medium_count++))
                ((medium_overruns+=overrun_count))
                ;;
            *low*)
                qos_class="LOW"
                ((low_count++))
                ((low_overruns+=overrun_count))
                ;;
            *)
                qos_class="UNKNOWN"
                ((unknown_count++))
                ((unknown_overruns+=overrun_count))
                ;;
        esac
    fi
    
    # 결과 저장
    printf "%-10s %-64s %5d   %s/%s\n" \
        "$qos_class" "$container_id" "$overrun_count" "$namespace" "$pod_name" >> "$OUTPUT_FILE"
    
done < <(tail -n +7 "$LOG_FILE")

# 요약 통계 추가
echo "" >> "$OUTPUT_FILE"
echo "=== Summary by QoS Class ===" >> "$OUTPUT_FILE"
echo "QoS Class     Containers    Total Overruns    Avg Overruns" >> "$OUTPUT_FILE"
echo "============================================================" >> "$OUTPUT_FILE"

if [ $low_count -gt 0 ]; then
    avg_low=$((low_overruns / low_count))
    printf "LOW           %-13d %-17d %d\n" $low_count $low_overruns $avg_low >> "$OUTPUT_FILE"
fi

if [ $medium_count -gt 0 ]; then
    avg_medium=$((medium_overruns / medium_count))
    printf "MEDIUM        %-13d %-17d %d\n" $medium_count $medium_overruns $avg_medium >> "$OUTPUT_FILE"
fi

if [ $high_count -gt 0 ]; then
    avg_high=$((high_overruns / high_count))
    printf "HIGH          %-13d %-17d %d\n" $high_count $high_overruns $avg_high >> "$OUTPUT_FILE"
fi

if [ $unknown_count -gt 0 ]; then
    avg_unknown=$((unknown_overruns / unknown_count))
    printf "UNKNOWN       %-13d %-17d %d\n" $unknown_count $unknown_overruns $avg_unknown >> "$OUTPUT_FILE"
fi

total_containers=$((low_count + medium_count + high_count + unknown_count))
total_overruns=$((low_overruns + medium_overruns + high_overruns + unknown_overruns))
echo "------------------------------------------------------------" >> "$OUTPUT_FILE"
printf "TOTAL         %-13d %-17d\n" $total_containers $total_overruns >> "$OUTPUT_FILE"

echo "" >&2
echo "Analysis complete! Results saved to: $OUTPUT_FILE" >&2
cat "$OUTPUT_FILE"
