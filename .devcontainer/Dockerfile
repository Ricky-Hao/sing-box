# syntax=docker/dockerfile:1
# 与 fork 的 CI 工具链一致；go.mod 仍决定项目的最低 Go 版本。
FROM golang:1.26.7-bookworm@sha256:e8c859f5632dcfde7b32d2012b4351728f6437930887c2f6a91ea242459e5514

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       bash ca-certificates curl git openssh-client \
       python3 python3-venv jq ripgrep file unzip xz-utils \
       build-essential pkg-config sudo \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 1000 vscode \
    && useradd --uid 1000 --gid 1000 --create-home --shell /bin/bash vscode \
    && printf 'vscode ALL=(root) NOPASSWD:ALL\n' > /etc/sudoers.d/vscode \
    && chmod 0440 /etc/sudoers.d/vscode \
    && install -d -o vscode -g vscode \
       /home/vscode/go /home/vscode/.cache/go-build /workspaces/sing-box

ENV HOME=/home/vscode
ENV GOPATH=/home/vscode/go
ENV GOCACHE=/home/vscode/.cache/go-build
ENV PATH=/usr/local/go/bin:/home/vscode/go/bin:${PATH}
USER vscode
WORKDIR /workspaces/sing-box
