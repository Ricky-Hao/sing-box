# 补丁仓工作规则

先读 `README.md`、`MAINTENANCE.md`、`manifest.json` 和涉及组的 README/tests.json。`main` 是维护层，不保存完整上游源码或 go.mod。完整应用在 `.work/application` 等生成目录；旧 `rickyhao/stable`、其他分支和 worktree 必须保留。不要把应用提交或上游 merge 直接写进维护层。

## 功能分组与实现

- 一组交付完整能力或可独立应用、验证、淘汰的修复。必要调用方、状态和副作用、构建接线、原有测试、配置/使用文档、schema 与功能同组。
- 将历史修正合入当前功能组。不能按文件数、跨层、历史提交数或单纯降低 patch 大小拆组。
- 共享文件按 hunk 分配单一责任，不能整文件覆盖混入其他组。按准确前序 tree 导出；依赖只写真实调用、数据或测试要求，并通过依赖闭包重放核实。
- 应用开发使用编辑分支；所有源码变更必须回写 patch。保留上游默认行为，不顺带重构或重写原有业务测试。
- 主动放弃能力按 [MAINTENANCE.md 的流程](MAINTENANCE.md#主动放弃功能)处理：先核消费者、配置/数据与真实依赖，在当前授权内明确行为变化，再移出活动 manifest；无替代时不得伪造上游替代状态。
- CI、发布矩阵、SFA 签名/依赖、Devcontainer、工具链和工程指引属于维护层，不另做业务 patch。应用指引模板是 `tools/application-AGENTS.md`。

## 生成与验证

使用 `python3 tools/patchset.py`；工具只依赖 Python 标准库和 Git。正式快照用 `branch` 完整预检和生成；研究用 `generate --only ID`；只读预检用 `check`。现有脏 worktree、不同 commit 的同名分支、tag 冲突或并发锁冲突必须停止并保留原状态，不能 reset、clean、stash 或 force。

上游 URL、准确 ref、完整 base 以 manifest 为准，不以最近 tag 替代。已有 `vX.Y.Z` 快照不可修改；同版本新补丁用未占用的 `vX.Y.Z-patchset-N`。官方 release tag 不得被 fork 发布重写。

维护工具变更运行 `python3 -m unittest discover -s tools -p 'test_*.py' -v`。补丁变更运行完整预检、相关依赖闭包独立重放和对应 tests.json；不能用 plan、树相同或仅转译成功替代行为测试。Go 检查在生成目录运行，变更 Go 文件须 gofmt。交付前检查原始 patch 字节、应用 diff 和维护 diff 的 `git diff --check`；不要格式化 patch。

iWAN 必须同时带 `with_iwan,with_gvisor`，核 CLI release 文件、mobile/libbox、include/stub 和 lint 标签。必跑定向入口在 iwan/tests.json；手机就绪与 MTU 等限制见应用指引。Fallback 的 daemon、真实失败通知与路由接线，schema 和共享注册项必须随所属组验证。

## CI 与授权

复用原有有效构建步骤，只保留既定 CLI 平台/架构、Linux pure Go/musl 和 arm64-v8a SFA。四项稳定 SFA 签名 secret：`SFA_RELEASE_KEYSTORE`、`SFA_KEYSTORE_PASS`、`SFA_KEY_ALIAS`、`SFA_KEY_PASS`。不得恢复临时密钥、不输出秘密、不提交 keystore 或真实网络凭据。

PR 仅验证。发布只在合入 main 并通过检查后重建精确维护 SHA，核对应用 commit/tree 和最终资产集合。应用分支原文归档上游 workflow，不能成为第二发布入口。workflow 变更做 actionlint 等静态检查；真实 Actions、签名 APK 与发布资产只能在获准执行后声称验证。

提交维护变更、push、创建或合并 PR、改默认分支、创建远端 tag/Release、dispatch CI、发布、部署、操作真实网络设备或重写历史，均须在当前用户明确授权的范围内执行。对本地生成分支与实现提交的授权不扩展到这些动作。
