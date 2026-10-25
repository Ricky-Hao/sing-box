# sing-box 开发环境

本目录用于本地 Dev Containers 和 Coder 的核心 Go 开发。维护分支为 `rickyhao/stable`，工作目录为 `/workspaces/sing-box`。

## 工具与归属

- 基础镜像固定为 Go `1.26.7` 的 Bookworm 镜像及其 digest，与当前 fork CI 工具链一致。升级时核对 `go.mod` 和相关 workflow。
- 容器使用 `vscode` 用户，UID/GID 为 `1000`。`sudo` 用于安装必要依赖和项目已有的特权测试，不代表可以操作外部设备。
- 镜像包含 C/C++ 编译工具、Git、SSH 客户端、Python 和常用查询工具。
- Coder 模板负责 OpenCode、acpx、opencode-task 及其认证环境。本目录不重复安装这些工具。
- Git 使用 Coder 已有 SSH 身份和主机信任。不要把私钥、Token 或签名材料写入镜像、仓库或日志。
- 本目录不新增缓存卷或清理旧数据。Coder 的 Workspace 和工具持久化由模板管理。

## 核心检查

在仓库根目录运行以下检查。具体改动仍须遵守根目录 `AGENTS.md`。

```sh
go version
go test -tags with_iwan,with_gvisor ./transport/iwan ./protocol/iwan ./option
go test -tags with_iwan,with_gvisor ./include
go test -run '^$' ./cmd/internal/build_libbox
go build -tags with_iwan,with_gvisor -o /tmp/sing-box-dev ./cmd/sing-box
git diff --check
```

首次测试会下载 Go 依赖。该镜像没有预装 Android SDK/NDK、JDK 或 Apple 工具链，不代表已经验证 APK、Apple 客户端或完整发布矩阵。需要对应构建时，使用项目已有 CI，或为当前任务单独准备所需工具。

## 运行边界

容器不会自动启动 sing-box，也不请求 TUN、主机网络或额外容器权限。默认只做源码开发和离线测试。真实网络测试、发布和设备部署需要各自的授权；不要把本地构建成功当作设备业务健康。
