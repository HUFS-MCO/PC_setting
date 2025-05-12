#!/bin/bash
set -e

kubectl create namespace prometheus
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

helm upgrade -i prometheus prometheus-community/prometheus \
    --namespace prometheus \
    --set server.extraArgs.storage.tsdb.retention.time="90d"