# SPDX-FileCopyrightText: 2020-2026 Tigerblue77 and the Dell iDRAC fan controller Docker image contributors
# SPDX-License-Identifier: AGPL-3.0-only

FROM golang:1.23-bookworm AS builder
WORKDIR /src

COPY go.mod ./
COPY cmd ./cmd

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/dell_idrac_fan_controller ./cmd/controller

FROM ubuntu:latest

LABEL org.opencontainers.image.authors="tigerblue77"
LABEL org.opencontainers.image.title="Dell iDRAC fan controller"
LABEL org.opencontainers.image.description="Control the fan speed of a Dell PowerEdge server from its temperatures, through IPMI"
LABEL org.opencontainers.image.url="https://github.com/tigerblue77/Dell_iDRAC_fan_controller_Docker"
LABEL org.opencontainers.image.source="https://github.com/tigerblue77/Dell_iDRAC_fan_controller_Docker"
LABEL org.opencontainers.image.documentation="https://github.com/tigerblue77/Dell_iDRAC_fan_controller_Docker#readme"
LABEL org.opencontainers.image.licenses="AGPL-3.0-only"

RUN apt-get update \
 && apt-get install ipmitool lm-sensors -y \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /out/dell_idrac_fan_controller /app/dell_idrac_fan_controller
COPY LICENSE /app/LICENSE
COPY LICENSE-COMMERCIAL.md /app/LICENSE-COMMERCIAL.md
COPY NOTICE /app/NOTICE

# you should override these default values when running. See README.md
ENV IDRAC_HOST=local
ENV FAN_SPEED=5
ENV CPU_TEMPERATURE_THRESHOLD=auto
ENV MAXIMUM_IPMI_UNREACHABLE_DURATION=60s
ENV DISABLE_THIRD_PARTY_PCIE_CARD_DELL_DEFAULT_COOLING_RESPONSE=false
ENV KEEP_THIRD_PARTY_PCIE_CARD_COOLING_RESPONSE_STATE_ON_EXIT=false
ENV MONITORING_ONLY_MODE=false
ENV AUTO_MODE=true
ENV PID_GAIN_PROPORTIONAL=1.5
ENV PID_GAIN_INTEGRAL=0.1
ENV PID_GAIN_DERIVATIVE=0.5
ENV AUTO_MODE_TEMPERATURE_MARGIN=3
ENV RATE_OF_CHANGE_BOOST=1.0
ENV GPU_TEMPERATURE_SOURCE=disabled
ENV GPU_TEMPERATURE_THRESHOLD=80

ENTRYPOINT ["/app/dell_idrac_fan_controller"]
