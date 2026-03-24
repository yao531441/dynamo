#!/bin/bash
# SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0
set -e
trap 'echo Cleaning up...; kill 0' EXIT

# Set deterministic hash for KV event IDs
export PYTHONHASHSEED=0

# Common configuration
MODEL="Qwen/Qwen3-0.6B"

# Device selection: supports CUDA (CUDA_VISIBLE_DEVICES) and XPU (ZE_AFFINITY_MASK)
# Set DYN_DEVICE=xpu to use Intel XPU device selection
if [[ "${DYN_DEVICE:-cuda}" == "xpu" ]]; then
    DYN_VISIBLE_DEVICES_D0="ZE_AFFINITY_MASK=0"
    DYN_VISIBLE_DEVICES_D1="ZE_AFFINITY_MASK=1"
    DYN_VISIBLE_DEVICES_P0="ZE_AFFINITY_MASK=2"
    DYN_VISIBLE_DEVICES_P1="ZE_AFFINITY_MASK=3"
    export VLLM_TARGET_DEVICE=xpu
    export NIXL_BUFFER_DEVICE=xpu
else
    DYN_VISIBLE_DEVICES_D0="CUDA_VISIBLE_DEVICES=0"
    DYN_VISIBLE_DEVICES_D1="CUDA_VISIBLE_DEVICES=1"
    DYN_VISIBLE_DEVICES_P0="CUDA_VISIBLE_DEVICES=2"
    DYN_VISIBLE_DEVICES_P1="CUDA_VISIBLE_DEVICES=3"
    export NIXL_BUFFER_DEVICE=cuda
fi

HTTP_PORT="${DYN_HTTP_PORT:-8000}"
SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
source "$SCRIPT_DIR/../../../common/launch_utils.sh"

print_launch_banner "Launching Disaggregated + KVBM + KV Routing (4 GPUs)" "$MODEL" "$HTTP_PORT"


# dynamo.frontend accepts either --http-port flag or DYN_HTTP_PORT env var (defaults to 8000)
python -m dynamo.frontend \
    --router-mode kv \
    --router-reset-states &

# two decode workers (without KVBM)
# --enforce-eager is added for quick deployment. for production use, need to remove this flag
env "$DYN_VISIBLE_DEVICES_D0" python3 -m dynamo.vllm \
    --model $MODEL \
    --enforce-eager \
    --disaggregation-mode decode \
    --kv-transfer-config "{\"kv_connector\":\"NixlConnector\",\"kv_role\":\"kv_both\",\"kv_buffer_device\":\"${NIXL_BUFFER_DEVICE}\"}" &

VLLM_NIXL_SIDE_CHANNEL_PORT=20096 \
env "$DYN_VISIBLE_DEVICES_D1" python3 -m dynamo.vllm \
    --model $MODEL \
    --enforce-eager \
    --disaggregation-mode decode \
    --kv-transfer-config "{\"kv_connector\":\"NixlConnector\",\"kv_role\":\"kv_both\",\"kv_buffer_device\":\"${NIXL_BUFFER_DEVICE}\"}" &

# two prefill workers with KVBM enabled
# Each worker needs unique ZMQ ports to avoid KVBM coordination conflicts
VLLM_NIXL_SIDE_CHANNEL_PORT=20097 \
DYN_KVBM_LEADER_ZMQ_PUB_PORT=56001 \
DYN_KVBM_LEADER_ZMQ_ACK_PORT=56002 \
DYN_KVBM_CPU_CACHE_GB=20 \
env "$DYN_VISIBLE_DEVICES_P0" \
    python3 -m dynamo.vllm \
    --model $MODEL \
    --enforce-eager \
    --disaggregation-mode prefill \
    --kv-transfer-config "{\"kv_connector\":\"PdConnector\",\"kv_role\":\"kv_both\",\"kv_buffer_device\":\"${NIXL_BUFFER_DEVICE}\",\"kv_connector_extra_config\":{\"connectors\":[{\"kv_connector\":\"DynamoConnector\",\"kv_connector_module_path\":\"kvbm.vllm_integration.connector\",\"kv_role\":\"kv_both\"},{\"kv_connector\":\"NixlConnector\",\"kv_role\":\"kv_both\"}]},\"kv_connector_module_path\":\"kvbm.vllm_integration.connector\"}" \
    --kv-events-config '{"publisher":"zmq","topic":"kv-events","endpoint":"tcp://*:20081"}' &

VLLM_NIXL_SIDE_CHANNEL_PORT=20098 \
DYN_KVBM_LEADER_ZMQ_PUB_PORT=56003 \
DYN_KVBM_LEADER_ZMQ_ACK_PORT=56004 \
DYN_KVBM_CPU_CACHE_GB=20 \
env "$DYN_VISIBLE_DEVICES_P1" \
    python3 -m dynamo.vllm \
    --model $MODEL \
    --enforce-eager \
    --disaggregation-mode prefill \
    --kv-transfer-config "{\"kv_connector\":\"PdConnector\",\"kv_role\":\"kv_both\",\"kv_buffer_device\":\"${NIXL_BUFFER_DEVICE}\",\"kv_connector_extra_config\":{\"connectors\":[{\"kv_connector\":\"DynamoConnector\",\"kv_connector_module_path\":\"kvbm.vllm_integration.connector\",\"kv_role\":\"kv_both\"},{\"kv_connector\":\"NixlConnector\",\"kv_role\":\"kv_both\"}]},\"kv_connector_module_path\":\"kvbm.vllm_integration.connector\"}" \
    --kv-events-config '{"publisher":"zmq","topic":"kv-events","endpoint":"tcp://*:20082"}' &

# Exit on first worker failure; kill 0 in the EXIT trap tears down the rest
wait_any_exit
