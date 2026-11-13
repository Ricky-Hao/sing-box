# v1.14.4 补丁仓迁移候选

以下生成与验证记录保留本地迁移候选阶段的事实，当时维护候选尚未提交；后续采用进展见[提交与推送阶段](#提交与推送阶段)。原 fork 是 `678d3c3bdf163ad33209f990d79348a334c586b0`；准确上游是 `https://github.com/SagerNet/sing-box.git` 的 `refs/tags/v1.14.4`，base 为 `c005f35300ce76427dc285481ec5695f5b179cd2`。官方 tag 已按准确 ref 获取并核对，没有采用最近 tag。

## 生成结果

完整源码位于 `.work/application`，本地版本分支为 `v1.14.4`。生成工具保留完整上游历史，依次创建以下五个实现提交，再创建工程指引和 workflow 归档提交：

| 功能组 | 文件数 | 增删 | 真实依赖 | 实现 commit |
| --- | ---: | --- | --- | --- |
| iwan | 49 | +6796/-5 | 无 | `a2d8a8eb0ced80c562d46c3eedf96c0da8582db9` |
| tcp-urltest | 4 | +100/-0 | 无 | `57d96f5ff24d8bd93a6977d60095f768f48bda2e` |
| urltest-concurrency-fix | 4 | +350/-28 | 无 | `21c71899130bdb507453154704d33eb3af27ef25` |
| fallback | 18 | +1811/-7 | tcp-urltest、urltest-concurrency-fix | `4d3cb70a36ced9b7c5257ed79beb56492a5927d7` |
| dns-response-compression-fix | 4 | +370/-12 | 无 | `525ca7b252450bb2907424ac4886477a1895edf6` |

五组共覆盖 72 个不同业务路径，合计 +9427/-52；表内文件数按各组计算，共享文件会重复计数。全部业务补丁应用后的 tree 为 `76b979add3bf6e9dcb1bfc88fac4fefbbebb8798`。

最终应用 commit 为 `ca3b2070fde38fcc4cc3d862d8d567cff6ec0124`，tree 为 `3d2273ce97e857b6ff0cdd1700665728be63258c`。最后一个提交只安装维护层的应用指引模板，并将官方上游的六个 workflow 原文移入 `.github/upstream-workflows/`；没有第六业务组。另一份隔离维护仓库 `.work/independent-maintenance` 已从准确 base 独立重建，得到相同 commit/tree。

Fallback 的 TCP 探测测试使用公共 `tcp://` 能力；嵌套组回归复用 URLTest 并发测试的原有夹具，因此有两个真实测试依赖。URLTest 并发组本身不依赖 Fallback 实现：它仅带入原有 `fallback_test.go`、`fallback_race_test.go` 中被 URLTest 测试直接使用的通用夹具声明。最终测试文件与旧 fork 字节一致，未改写原有回归。

## 独立核对与预期差异

核对直接读取 base、旧 fork、各实现提交和最终应用的 Git tree/blob/mode，没有复用补丁提取脚本的归属结果。结论如下：

- 原 fork 相对上游的净差异为 83 文件、+9694/-389。72 个业务路径全部进入五组，其余 11 个工程路径归入维护层，逐文件归属见下表。
- 生成应用的全部业务文件及其他未变上游文件，与旧 fork 的 blob 和文件模式一致。`go.mod`、`go.sum`、`LICENSE` 与三个客户端 submodule gitlink 均保持一致，module 仍为 `github.com/sagernet/sing-box`。
- 最终应用相对旧 fork 仅有 17 个预期路径差异：下表 11 个维护路径，以及六个新增的 `.github/upstream-workflows/*.yml` 归档路径。归档内容和模式与官方 base 完全一致，原 `.github/workflows/` 不保留活跃入口。
- 生成应用采用上游 `.gitignore`，不携带 fork 的 Devcontainer；开发环境由维护仓根目录提供。应用 `AGENTS.md` 来自 `tools/application-AGENTS.md`，已纠正旧分支与命令入口，并保留 iWAN 手机就绪、MTU 和真实设备验收限制。
- 原始五份 patch 与其准确父提交到实现提交的 `git diff --binary --full-index --no-renames` 导出逐字节一致。没有格式化 patch，也没有通过整文件覆盖分配共享责任。

`constant/proxy.go`、`include/registry.go`、`docs/schema.json`、`mkdocs.yml` 的 iWAN 与 Fallback hunk 分别随所属组交付。`protocol/group/urltest.go` 的选择锁属于并发修复，嵌套 `URLTestGroup` 调用属于 Fallback。schema 本地引用全部可解析；iWAN endpoint/inbound、Fallback outbound 注册均随对应组重放。CLI 三份 release 标签、mobile/libbox 和 lint 均同时包含 `with_iwan,with_gvisor`。Fallback 的公共失败通知、路由叶子失败分发和 daemon 通用探测接线均保留并纳入验证。

维护层的 CI、发布矩阵、SFA 签名与依赖、开发环境、工具和工程指引直接留在候选 `main`。维护仓不保留完整应用或 `go.mod`；原有源码和历史仍在旧分支、原 worktree 及生成应用中。

## 原 83 个差异文件的归属

增删统计均为官方 base 到旧 fork 的原始净差异。`maintenance` 表示工程路径移交维护层，不进入业务 patch；共享文件中的各 hunk 只有一个所属组。

| 原路径 | 原净差异 | 归属 |
| --- | --- | --- |
| `.devcontainer/Dockerfile` | +23/-0 | maintenance |
| `.devcontainer/README.md` | +31/-0 | maintenance |
| `.devcontainer/devcontainer.json` | +20/-0 | maintenance |
| `.github/workflows/build.yml` | +121/-304 | maintenance |
| `.github/workflows/docker.yml` | +0/-3 | maintenance |
| `.github/workflows/lint.yml` | +4/-10 | maintenance |
| `.github/workflows/linux.yml` | +0/-7 | maintenance |
| `.github/workflows/stale.yml` | +1/-2 | maintenance |
| `.github/workflows/test.yml` | +3/-12 | maintenance |
| `.gitignore` | +0/-1 | maintenance |
| `.golangci.yml` | +1/-1 | iwan |
| `AGENTS.md` | +66/-0 | maintenance |
| `adapter/outbound.go` | +5/-0 | fallback |
| `cmd/internal/build_libbox/main.go` | +1/-1 | iwan |
| `cmd/internal/build_libbox/main_test.go` | +38/-0 | iwan |
| `common/urltest/urltest.go` | +21/-0 | tcp-urltest |
| `common/urltest/urltest_test.go` | +75/-0 | tcp-urltest |
| `constant/proxy.go` | +6/-0 | iwan, fallback |
| `daemon/started_service.go` | +5/-3 | fallback |
| `dns/client_truncate.go` | +57/-8 | dns-response-compression-fix |
| `dns/client_truncate_test.go` | +245/-0 | dns-response-compression-fix |
| `docs/clients/general.md` | +1/-1 | fallback |
| `docs/configuration/endpoint/index.md` | +1/-0 | iwan |
| `docs/configuration/endpoint/index.zh.md` | +1/-0 | iwan |
| `docs/configuration/endpoint/iwan.md` | +115/-0 | iwan |
| `docs/configuration/endpoint/iwan.zh.md` | +115/-0 | iwan |
| `docs/configuration/inbound/index.md` | +1/-0 | iwan |
| `docs/configuration/inbound/index.zh.md` | +1/-0 | iwan |
| `docs/configuration/inbound/iwan.md` | +127/-0 | iwan |
| `docs/configuration/inbound/iwan.zh.md` | +127/-0 | iwan |
| `docs/configuration/outbound/fallback.md` | +61/-0 | fallback |
| `docs/configuration/outbound/fallback.zh.md` | +61/-0 | fallback |
| `docs/configuration/outbound/index.md` | +1/-0 | fallback |
| `docs/configuration/outbound/index.zh.md` | +1/-0 | fallback |
| `docs/configuration/outbound/urltest.md` | +2/-0 | tcp-urltest |
| `docs/configuration/outbound/urltest.zh.md` | +2/-0 | tcp-urltest |
| `docs/installation/build-from-source.md` | +8/-0 | iwan |
| `docs/installation/build-from-source.zh.md` | +8/-0 | iwan |
| `docs/schema.json` | +333/-0 | iwan, fallback |
| `include/iwan.go` | +17/-0 | iwan |
| `include/iwan_stub.go` | +27/-0 | iwan |
| `include/registry.go` | +3/-0 | iwan, fallback |
| `mkdocs.yml` | +3/-0 | iwan, fallback |
| `option/group.go` | +8/-0 | fallback |
| `option/iwan.go` | +39/-0 | iwan |
| `option/iwan_test.go` | +39/-0 | iwan |
| `protocol/dns/handle.go` | +2/-4 | dns-response-compression-fix |
| `protocol/dns/handle_test.go` | +66/-0 | dns-response-compression-fix |
| `protocol/group/fallback.go` | +524/-0 | fallback |
| `protocol/group/fallback_race_test.go` | +221/-0 | urltest-concurrency-fix, fallback |
| `protocol/group/fallback_test.go` | +739/-0 | urltest-concurrency-fix, fallback |
| `protocol/group/urltest.go` | +59/-28 | urltest-concurrency-fix, fallback |
| `protocol/group/urltest_race_test.go` | +206/-0 | urltest-concurrency-fix |
| `protocol/iwan/endpoint.go` | +349/-0 | iwan |
| `protocol/iwan/endpoint_test.go` | +191/-0 | iwan |
| `protocol/iwan/inbound.go` | +243/-0 | iwan |
| `protocol/iwan/inbound_test.go` | +54/-0 | iwan |
| `protocol/iwan/stub.go` | +3/-0 | iwan |
| `release/DEFAULT_BUILD_TAGS` | +1/-1 | iwan |
| `release/DEFAULT_BUILD_TAGS_OTHERS` | +1/-1 | iwan |
| `release/DEFAULT_BUILD_TAGS_WINDOWS` | +1/-1 | iwan |
| `route/conn.go` | +7/-1 | fallback |
| `route/fallback_test.go` | +221/-0 | fallback |
| `transport/iwan/batch_linux_test.go` | +104/-0 | iwan |
| `transport/iwan/device_stack.go` | +509/-0 | iwan |
| `transport/iwan/device_stack_gonet.go` | +66/-0 | iwan |
| `transport/iwan/device_stack_stub.go` | +11/-0 | iwan |
| `transport/iwan/device_stack_test.go` | +69/-0 | iwan |
| `transport/iwan/endpoint.go` | +661/-0 | iwan |
| `transport/iwan/endpoint_options.go` | +30/-0 | iwan |
| `transport/iwan/endpoint_test.go` | +542/-0 | iwan |
| `transport/iwan/fragment.go` | +126/-0 | iwan |
| `transport/iwan/fragment_test.go` | +114/-0 | iwan |
| `transport/iwan/protocol.go` | +386/-0 | iwan |
| `transport/iwan/protocol_test.go` | +159/-0 | iwan |
| `transport/iwan/protocol_username_test.go` | +72/-0 | iwan |
| `transport/iwan/server.go` | +852/-0 | iwan |
| `transport/iwan/server_test.go` | +598/-0 | iwan |
| `transport/iwan/socket_buffer.go` | +23/-0 | iwan |
| `transport/iwan/tcp_forwarder.go` | +243/-0 | iwan |
| `transport/iwan/username_options_test.go` | +75/-0 | iwan |
| `transport/iwan/write_vector_linux.go` | +321/-0 | iwan |
| `transport/iwan/write_vector_stub.go` | +20/-0 | iwan |

维护路径的具体处理：三个 Devcontainer 文件适配补丁仓生成入口；原 build workflow 的有效 CLI、libbox、SFA 构建步骤移入维护层可复用 workflow，平台/签名政策保留；原 test/lint 的检查并入维护 CI；docker、linux、stale 不保留额外镜像、扩展产物或维护自动动作入口。六个官方 workflow 在应用中原文归档。根 `.gitignore` 仅负责维护仓生成目录，根 `AGENTS.md` 负责补丁维护规则，应用工程规则由模板生成。这 11 个路径没有遗漏业务差异。

## 本地验证证据

以下证据保存在原工作区的忽略目录 `.work/`，不包含签名密钥或真实网络凭据。

| 检查 | 已执行结果 | 证据 |
| --- | --- | --- |
| `python3 -m unittest discover -s tools -p 'test_*.py' -v` | 34 项通过，含脏树、冲突、原子应用、不可变快照与并发锁保护 | `.work/tool-tests.log` |
| `python3 tools/patchset.py branch --target .work/application --source /workspaces/sing-box` 与 `verify` | 完整预检、五组重放与真实版本分支生成通过 | `.work/ci-helper.log` |
| 独立维护仓执行 `branch` 与 `verify` | 相同应用 commit/tree | `.work/independent-rebuild.log`、`.work/independent-rebuild.json` |
| `GOWORK=off GOFLAGS=-timeout=10m python3 tools/patchset.py test --target .work/application --tier all` | 全部契约通过，包含真实行为与相关 race 测试 | `.work/go-contracts.log` |
| 各组 `generate --only ID` 后执行对应依赖闭包的 `test --only ID --tier all` | 五个闭包全部独立重放、全部契约通过 | `.work/closure-replays.log`、`.work/closure-results.json` |
| iWAN transport/protocol/option、include 启用与 stub、mobile/libbox | 定向行为、race、标签与编译检查通过；完整命令见 iwan/tests.json | `.work/go-contracts.log` |
| Fallback group/route、daemon、URLTest/TCP、DNS | 原有定向行为与相关 race 测试通过；daemon 编译通过 | `.work/go-contracts.log` |
| 五组 Go 文件 `gofmt -l` | 无输出 | `.work/gofmt.log` |
| 独立 tree/blob/mode、83 文件归属及原始 patch 字节核对 | 全部通过 | `.work/migration-audit.json`、`.work/migration-audit.log`、`.work/audit_migration.py` |
| actionlint 1.7.12 与 `bash -n tools/prepare.sh` | 通过 | `.work/actionlint.log`、`.work/prepare-syntax.log` |
| `bash tools/prepare.sh --source /workspaces/sing-box` | 原生生成、快照验证与依赖下载通过 | `.work/dev-prepare.log` |
| 完整 release/DEFAULT_BUILD_TAGS_OTHERS 的项目级 `go test -count=1 ... ./...` | 仅上游 `common/netns.TestUnshareNamespace` 失败，其余包通过；容器拒绝创建网络命名空间 | `.work/project-go-test.log`、`.work/project-checks.json` |
| 同标签、release/LDFLAGS 的原生 `go build -trimpath ... ./cmd/sing-box` | Linux amd64 构建通过，`version` 显示 iWAN/gVisor 标签；本地产物不是 release 归档 | `.work/cli-build.log`、`.work/cli-version.log`、`.work/build/sing-box` |
| 维护工作树/index、各组应用 diff、完整应用 diff、原始 patch 的 `git diff --check` | 均无 whitespace 诊断；五份原始 patch 哈希保持不变 | `.work/whitespace-checks.json` |
| JSON、schema 本地引用与 iWAN/Fallback 类型条目 | 均通过 | `.work/schema-feature-check.json` |
| 原分支/worktree、应用干净状态和维护文件范围 | 全部符合预期 | `.work/final-state.json` |

项目级命令在 `.work/application` 执行，环境为 Go 1.26.7、`GOWORK=off GOFLAGS=-timeout=10m`：

```sh
go test -count=1 -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -ldflags "$(cat release/LDFLAGS)" ./...
go build -trimpath -tags "$(cat release/DEFAULT_BUILD_TAGS_OTHERS)" -ldflags "$(cat release/LDFLAGS)" -o ../build/sing-box ./cmd/sing-box
```

唯一失败原文为 `manager_test.go:47: network namespace[test]: start holder process: fork/exec /proc/self/exe: operation not permitted`。该上游测试和实现未改动，没有跳过它、修改断言或提高容器权限。`git diff --no-index --check /dev/null changes.patch` 的退出码 1 表示文件与空文件不同；五份 patch 均无 whitespace 输出。patch 包装文本按 `.gitattributes` 保留原字节，实际新增代码另由各实现提交和完整应用 diff 检查。

当前分支是 `chore/feature-patchset`，HEAD 仍为旧 fork 的 `678d3c3bdf163ad33209f990d79348a334c586b0`，没有维护候选提交。工作树有 38 个维护文件：30 个新增文件以 intent-to-add 便于直接查看 diff，7 个已有文件修改，原完整应用的 1612 个路径删除已暂存；LICENSE 保留原文。旧 `rickyhao/stable`、origin 跟踪引用及另外两个原有测试 worktree 的 HEAD 不变且干净，生成的 `v1.14.4` worktree 也干净。

CI 源码已连接实际生成、完整定向测试、五个独立闭包，以及原有跨平台测试/lint。PR 只验证；main 的合并提交在全部检查成功后才调用构建发布 workflow，并从同一维护 SHA 重建、核对应用 commit/tree。发布前检查精确资产集合，创建独立 `patchset-v*` tag，拒绝覆盖已有 tag/资产。只初始化 main 或直接提交不会自动发布。

SFA 客户端固定到应用现有 gitlink `f87c2ebdb7e34754be751bb5e58926db2fed8f8c`，保留其 Gradle 依赖与原有版本更新方法；已只读核对该版本的 APK 命名、签名属性和 ABI 配置。签名仍要求四项稳定 secret，产物仅有既定十份 CLI 归档、一份 arm64-v8a APK 及版本元数据。资产检查器的本地测试使用合成文件，不代表实际发布产物已经构建。

本阶段未执行真实 Actions、跨平台 release 构建、完整 golangci-lint 矩阵、Android NDK/Gradle 签名 APK、发布或真实设备验收。环境没有 Docker，未运行 Devcontainer 镜像构建；原生生成/依赖入口已通过。维护候选未提交，因此也未声称已验证一个已提交的维护 SHA。旧 `rickyhao/stable`、其他分支和 worktree 保留，没有 push、创建 PR、改默认分支、远端 tag/Release、dispatch CI 或操作真实网络设备。

## 主动放弃功能规则的文档复核

本轮仅在 MAINTENANCE 补齐[主动放弃功能](MAINTENANCE.md#主动放弃功能)流程，并补充 README、AGENTS 的入口引用；本记录仅追加本轮复核事实。五组 patch、manifest、生成器、CI 和应用源码未改动，原有测试及核对证据直接复用，未重跑 Go、完整重放或工具测试。

本轮文档引用与规范检查、维护 worktree/index 的 `git diff --check` 通过。只读核对生成身份仍为 `v1.14.4`、commit `ca3b2070fde38fcc4cc3d862d8d567cff6ec0124`、tree `3d2273ce97e857b6ff0cdd1700665728be63258c`；维护候选未提交。

## 提交与推送阶段

用户已明确授权采用既定迁移。本阶段负责清理主文中的本地候选阶段限制、提交已审维护内容、使用已提交维护 SHA 严格核验生成结果，并普通推送 `chore/feature-patchset` 和精确的 `refs/heads/v1.14.4`。main 引导、PR 平台合并和默认分支设置交负责人后续处理；本记录不声称这些动作、远端 CI 或发布已经完成。

本轮只调整 README、MAINTENANCE、AGENTS 的长期流程表述并补充本记录，未改五组 patch、manifest、生成器、CI 或应用源码。应用身份仍为前述 commit/tree，无需更换快照名；既有重放和业务测试证据继续有效。精确暂存范围、已提交维护 SHA、严格生成核验及两个远端 ref 的执行结果留在原工作区 `.work/adoption-*.json` 与日志中。
