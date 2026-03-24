#!/bin/bash
# SPDX-FileCopyrightText: Copyright (c) 2025-2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
# SPDX-License-Identifier: Apache-2.0
set -e
trap 'echo Cleaning up...; kill 0' EXIT

SCRIPT_DIR="$(dirname "$(readlink -f "$0")")"
source "$SCRIPT_DIR/../../../common/launch_utils.sh"

# Set deterministic hash for KV event IDs
export PYTHONHASHSEED=0

# Common configuration
MODEL="Qwen/Qwen3-0.6B"
BLOCK_SIZE=64

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

print_launch_banner "Launching Disaggregated + KV Routing (4 GPUs)" "$MODEL" "$HTTP_PORT"


# Start frontend with KV routing
# The frontend will automatically detect prefill workers and activate an internal prefill router
# dynamo.frontend accepts either --http-port flag or DYN_HTTP_PORT env var (defaults to 8000)
python -m dynamo.frontend \
    --router-mode kv \
    --router-reset-states &

# two decode workers
# --enforce-eager is added for quick deployment. for production use, need to remove this flag
env "$DYN_VISIBLE_DEVICES_D0" python3 -m dynamo.vllm \
    --model $MODEL \
    --block-size $BLOCK_SIZE \
    --enforce-eager \
    --disaggregation-mode decode \
    --kv-transfer-config "{\"kv_connector\":\"NixlConnector\",\"kv_role\":\"kv_both\",\"kv_buffer_device\":\"${NIXL_BUFFER_DEVICE}\"}" &

VLLM_NIXL_SIDE_CHANNEL_PORT=20097 \
env "$DYN_VISIBLE_DEVICES_D1" python3 -m dynamo.vllm \
    --model $MODEL \
    --block-size $BLOCK_SIZE \
    --enforce-eager \
    --disaggregation-mode decode \
    --kv-transfer-config "{\"kv_connector\":\"NixlConnector\",\"kv_role\":\"kv_both\",\"kv_buffer_device\":\"${NIXL_BUFFER_DEVICE}\"}" &

# two prefill workers
# When registered with --disaggregation-mode prefill, these workers are automatically detected
# by the frontend, which activates an internal prefill router for KV-aware prefill routing
VLLM_NIXL_SIDE_CHANNEL_PORT=20098 \
env "$DYN_VISIBLE_DEVICES_P0" python3 -m dynamo.vllm \
    --model $MODEL \
    --block-size $BLOCK_SIZE \
    --enforce-eager \
    --disaggregation-mode prefill \
    --kv-transfer-config "{\"kv_connector\":\"NixlConnector\",\"kv_role\":\"kv_both\",\"kv_buffer_device\":\"${NIXL_BUFFER_DEVICE}\"}" \
    --kv-events-config '{"publisher":"zmq","topic":"kv-events","endpoint":"tcp://*:20082","enable_kv_cache_events":true}'&

VLLM_NIXL_SIDE_CHANNEL_PORT=20099 \
env "$DYN_VISIBLE_DEVICES_P1" python3 -m dynamo.vllm \
    --model $MODEL \
    --block-size $BLOCK_SIZE \
    --enforce-eager \
    --disaggregation-mode prefill \
    --kv-transfer-config "{\"kv_connector\":\"NixlConnector\",\"kv_role\":\"kv_both\",\"kv_buffer_device\":\"${NIXL_BUFFER_DEVICE}\"}" \
    --kv-events-config '{"publisher":"zmq","topic":"kv-events","endpoint":"tcp://*:20083","enable_kv_cache_events":true}' &

# Exit on first worker failure; kill 0 in the EXIT trap tears down the rest
wait_any_exit
