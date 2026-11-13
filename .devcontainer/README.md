# 补丁仓开发环境

沿用 Go 1.26.7 Bookworm 固定镜像、Python、Git 和 C 编译工具。没有安装 coding agent，也不修改现有 Coder runtime。此配置给新建 Dev Container 使用；已有工作区直接运行下列命令即可。

```sh
bash tools/prepare.sh
# 离线重用已有准确源码：bash tools/prepare.sh --source /path/to/sing-box
python3 tools/patchset.py test --target .work/application --tier all
```

postCreateCommand 在固定官方 ref 上生成 `.work/application` linked worktree，再在生成目录下载 Go 依赖。目标有修改或同名版本内容不同会停止，保留原文件；按 MAINTENANCE.md 移动旧 worktree 并选择新快照名，不重置工作树。

VS Code 可打开根目录的 `sing-box.code-workspace`，同时看到维护文件与应用源码。Go 构建、gopls、gofmt、原有 Makefile 命令和测试在 `.work/application` 执行，维护 Python 检查在根目录执行。维护仓没有 go.mod，不能在根目录执行 `go test ./...`。应用修改使用编辑分支，必须回写所属 patch；不要直接改已生成版本快照。

容器不挂载 TUN、不配置真实网络设备，也不导入 APK 签名或网络凭据。Android 签名构建仍使用维护 CI 的稳定 secret 和固定 SDK/NDK/JDK 步骤。
