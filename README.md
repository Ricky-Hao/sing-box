# sing-box feature patchset

本仓库维护 Ricky-Hao/sing-box 的完整功能补丁。`main` 只保存补丁、生成工具、CI 和维护资料；完整 sing-box 源码由固定的官方版本生成，Go module 保持 `github.com/sagernet/sing-box`。

当前上游是 [SagerNet/sing-box v1.14.4](https://github.com/SagerNet/sing-box/tree/c005f35300ce76427dc285481ec5695f5b179cd2)，准确 ref 为 `refs/tags/v1.14.4`，base 为 `c005f35300ce76427dc285481ec5695f5b179cd2`。唯一应用顺序见 [manifest.json](manifest.json)。旧 `rickyhao/stable` 和已有 worktree 保留，后续维护入口迁到补丁层。

| 顺序 | 功能组 | 依赖 |
| --- | --- | --- |
| 1 | [iwan](patches/iwan/README.md)：完整客户端、服务端、协议与构建接线 | 无 |
| 2 | [tcp-urltest](patches/tcp-urltest/README.md)：公共 `tcp://host:port` 探测 | 无 |
| 3 | [urltest-concurrency-fix](patches/urltest-concurrency-fix/README.md)：TCP/UDP 选择并发修复 | 无 |
| 4 | [fallback](patches/fallback/README.md)：完整优先级故障切换出站 | tcp-urltest、urltest-concurrency-fix（现有测试夹具） |
| 5 | [dns-response-compression-fix](patches/dns-response-compression-fix/README.md)：DNS 响应压缩与长度修复 | 无 |

一组交付完整能力或可独立淘汰的修复。必要调用方、构建标签、原有测试、schema 和配置/使用文档与功能同组；历史修正合入所属组。文件数、跨层和历史提交数不是拆组理由。共享文件按 hunk 分责，依赖须经代码和独立重放证明。应用源码修改必须回写 patch，CI 与开发环境直接维护在 `main`。

## 生成与验证

需要 Git、Python 3.11+、Go 1.26.7+ 和 C 编译器（race 测试）。容器入口见 [.devcontainer/README.md](.devcontainer/README.md)。以下命令都从维护仓根目录执行：

```sh
python3 -m unittest discover -s tools -p 'test_*.py' -v
python3 tools/patchset.py list
python3 tools/patchset.py branch --target .work/application
python3 tools/patchset.py verify --target .work/application
python3 tools/patchset.py test --target .work/application --tier all
```

`branch` 完整预检后生成真实 `v1.14.4` linked worktree，保留上游历史，每组一个实现提交，最后一个维护提交归档上游 workflow 并安装应用工程指引。已有分支仅在 commit 精确相同时复用；同版本更新用尚未占用的 `v1.14.4-patchset-N`（N 从 2 开始），不覆盖快照或官方 tag。已有本地上游仓可加 `--source /path/to/sing-box`。

```sh
cd .work/application
go build -tags with_iwan,with_gvisor ./cmd/sing-box
```

单组研究用 `generate --only ID`，会自动包含真实依赖，完整预检后生成独立 clone；它不创建正式版本分支。详细的新增、应用、修改、上游替代和同步上游步骤见 [MAINTENANCE.md](MAINTENANCE.md)，主动移除能力按[主动放弃功能](MAINTENANCE.md#主动放弃功能)处理。迁移逐文件归属与验证记录入口见 [MIGRATION.md](MIGRATION.md)，来源与许可见 [SOURCES.md](SOURCES.md)。

## 构建与发布

PR 只验证。合入 `main` 后重新验证精确维护 SHA，全部检查成功且应用快照有更新时，发布流程才从该 SHA 重建并核对应用 commit/tree。发布标签使用 `patchset-v1.14.4` 等独立名字，指向维护版本，官方 `v1.14.4` 不移动；应用分支不含活跃 workflow。

输出保持为 Linux（pure Go / musl）、Windows、macOS、FreeBSD 的 amd64/arm64 CLI 归档，以及 arm64-v8a SFA APK 与原有版本元数据。不生成 glibc、SFM、deb、OpenWrt 或其他产物。SFA 必须使用稳定的四项签名 secret，不生成临时密钥。
