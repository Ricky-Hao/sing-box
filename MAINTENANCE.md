# 维护方法

## 事实来源与边界

`manifest.json` 是上游 URL、完整 base、准确 release ref、应用分支名、功能顺序、状态、依赖和文件清单的唯一事实来源。`patches/<id>/changes.patch` 是应用改动的事实来源；README 解释行为与取舍，非空 tests.json 给出实际执行的检查命令。工具不自动切 base、不猜 tag、不 cherry-pick 旧历史。

维护层只放 manifest、patches、tools、根 `.github/workflows`、`.devcontainer`、维护文档、许可和来源。生成应用保留上游完整历史与 `module github.com/sagernet/sing-box`。旧 `rickyhao/stable` 作为旧 fork 快照保留；它不再是新补丁的维护入口。

一组交付完整能力或独立修复，必要调用方、构建接线、原有测试、schema、中英文配置和使用说明同组。历史修正归并所属组。只有能独立应用、验证和淘汰的能力才拆组；跨层、文件数、提交数不构成拆组依据。共享文件按 hunk 分责，不得整体覆盖前后组。依赖反映真实代码、数据或测试需要，不为排序制造依赖。

## 完整生成与失败保护

```sh
python3 tools/patchset.py list
python3 tools/patchset.py branch --target .work/application
python3 tools/patchset.py verify --target .work/application
python3 tools/patchset.py test --target .work/application --tier all
```

有本地准确源码时可加 `--source /path/to/upstream-repository`，离线来源仍须包含固定 base 和准确 ref，二者解析出的 commit 必须相等。不要将 fork 发布 tag 当作上游 tag。

工具先在独立临时 clone 与私有 index 中预检整个有序序列，然后才导入对象、创建正式分支和 linked worktree。每个活动组对应一个实现提交；末尾维护提交原字节归档 `.github/workflows/*` 到 `.github/upstream-workflows/*`，并由 `tools/application-AGENTS.md` 生成应用 AGENTS。它不是第六业务组。上游未来已有同名归档或 AGENTS 时，要求显式适配，不覆盖。

生成身份、时间、父提交、tree 和消息固定，同一维护输入在独立仓库得到同一应用 commit/tree。已有分支只允许 commit 精确相同；已有目标只允许验证精确快照。`verify` 检查 commit、官方 ref、工作树、index 和未跟踪文件，修改不能伪装成已生成快照。

工具按 Git common dir 加排他锁，并为新目标加目标锁，linked worktree 共用锁。中断后先检查记录的 PID 和对应进程是否仍运行，确认没有生成/应用/测试操作后才人工删除该具体锁文件；不自动抢锁。工具不会 reset、clean、stash 或 force。失败通常只留下可回收 Git 对象；worktree 创建若失败，已完成且精确的分支可能保留，先核实再重试，不删除用户分支。

正式快照名首次为 `vX.Y.Z`。同版本任何改变生成结果的维护输入都用尚未占用的 `vX.Y.Z-patchset-N`（N 从 2 开始）；例如先在 manifest 改为 `v1.14.4-patchset-2`，再选新目录生成。旧 `.work/application` 可用 `git worktree move .work/application .work/application-v1.14.4` 保留，随后在原路径生成新版。不要移动脏 worktree 或改写旧版本分支。工具不自动寻找空闲序号，先用 `git show-ref --verify refs/heads/<name>` 和获准时的远端只读查询核实。

## 预检、应用与独立重放

`check` 和 `apply` 要求目标 HEAD 恰好等于 base，index、工作树和未跟踪文件干净。完整预检在私有 index 中完成；`apply` 最后以一次 `git apply --index` 原子落地整个合并 diff，结果已暂存，HEAD 不移动。

```sh
git clone https://github.com/SagerNet/sing-box.git .work/manual
git -C .work/manual checkout --detach c005f35300ce76427dc285481ec5695f5b179cd2
python3 tools/patchset.py check --target .work/manual
python3 tools/patchset.py apply --target .work/manual
python3 tools/patchset.py verify --target .work/manual
```

单组能力及依赖闭包必须能在没有其他组的干净源码上重放、实际测试：

```sh
python3 tools/patchset.py generate --only fallback --target .work/replay-fallback
python3 tools/patchset.py test --only fallback --target .work/replay-fallback --tier all
```

`--only` 自动包含传递依赖、仍按 manifest 排序，每组一次；`branch` 不接受部分选择。`generate` 生成独立 clone、在临时目录完成后才公布目标，不能把它误称为正式应用版本分支。已有生成目录只验证，不重复应用。更新补丁后保留旧研究目录，使用新路径。完整分支可配 `test --only ID` 跑选中合同，但完整分支验证仍覆盖所有组。

## 新增或修改一个组

先找清真正的功能边界和已有调用方。能合并到现有能力的修正直接回写该组。新组须有 changes.patch、README.md、非空 tests.json，以及 manifest 的 paths、真实 depends_on 和状态。README 记录行为、限制、保留理由、来源完整 SHA 与后续修正、依赖、文件范围、真实检查入口、部分/完全上游替代条件。

导出第 k 组时，基准必须是**准确上游 base 加 manifest 中所有活动前序组后的 tree**。只有依赖闭包适合独立测试，它不一定等于导出用的完整前序 tree。最直接的方法是从生成分支对应的实现提交开始编辑：

```sh
# 先从日志找出目标组的完整实现 SHA；不使用最后的维护归档提交。
git -C .work/application log --reverse --format='%H %s' c005f35300ce76427dc285481ec5695f5b179cd2..HEAD
implementation=<目标组完整实现SHA>
preceding=$(git rev-parse "$implementation^1")
git worktree add -b edit/fallback .work/edit-fallback "$implementation"
# 在 .work/edit-fallback 修改该组文件及必要调用方/测试/文档，并 gofmt。
git -C .work/edit-fallback add -- <明确的本组文件>
git -C .work/edit-fallback diff --cached --check "$preceding"
git -C .work/edit-fallback diff --cached --binary --full-index --no-ext-diff --no-renames "$preceding" > patches/fallback/changes.patch.next
# 检查新增文件、共享 hunks、--numstat 与 manifest paths 后才替换。
git -C .work/edit-fallback apply --numstat "$PWD/patches/fallback/changes.patch.next"
mv patches/fallback/changes.patch.next patches/fallback/changes.patch
```

示例中的尖括号占位须替换为真实值；每条导出命令成功后才执行下一步，不将直接重定向写到原 patch。新组从准确前序实现提交建编辑 worktree；第一个组从 base 开始。编辑后必须回写，不能只在生成源码修好。不要从最终应用 tree 导出较早组，否则后续组、工程指引和 workflow 归档会混入。

更新 manifest 的路径清单、真实依赖、组说明与测试合同，同版本改用新快照名。完整预检若后续 patch 因上下文失败，按每个后续组准确前序 tree 分别更新其 hunk，不添加虚假依赖、不整文件复制。然后运行独立依赖闭包和完整源码的相应真实测试。已有行为回归继续执行，不将它们换成只检查源码字符串的测试。

## 部分上游替代与完全淘汰

部分吸收：记录官方完整 commit SHA、该 commit 是新 base 祖先的证据，以及它覆盖哪些行为/测试。只删除已被覆盖的 hunk，保留尚未实现的行为、构建接线、测试和文档；仍是同一组。基于新准确前序 tree 重新导出，并在无该残余 patch 时证明缺口、带 patch 时验证通过。标题相似、单个 symbol 相同或 patch 能应用都不能证明等价。

完全替代：核实该组 README 的全部替代条件和现有回归，包括边界、错误传播、标签、配置/schema 和 daemon/路由集成。在新 base 上不应用该组也应通过等价行为检查。随后保留组 README/tests.json 和历史说明，删除 changes.patch，manifest 设置 `status: upstream`（或 `replaced`）、`patch: null`、`paths: []`，添加：

```json
"replacement": {
  "commit": "<上游完整40位commit SHA>",
  "evidence": "组README中的具体行为、回归入口与验证记录"
}
```

工具会验证 replacement.commit 是所选固定 base 的祖先才跳过。若原测试还未被上游提供，不能将它一起丢弃并宣称完全替代；保留必要测试 hunk，或先将合同指向已证实等价且实际存在的上游入口。调整真实依赖并重放受影响闭包。保留 README 和非空合同，使未来仍可检查“为什么不再打补丁”。

## 主动放弃功能

主动放弃表示后续快照不再提供该能力，按以下步骤处理；本流程不增加自动删除或发布授权。

1. 先查消费者、现有配置、已有数据和真实依赖，说明放弃原因、行为变化及配置/数据的处置方法。仅在当前任务授权覆盖这些变化时执行。
2. 仍保留的组若有实际调用或测试依赖，先适配调用方、夹具及相关回归/文档，将源码改动回写所属组 patch，再更新真实依赖；不能只删除 `depends_on`。受影响组按准确前序 tree 重新导出。
3. 从活动 manifest 清单移除该组条目。没有替代实现时，不得标为 `upstream` / `replaced` 或伪造 `replacement`。按实际需要删除该组文件，或仅保留不参与执行的退役 README，说明原因和行为变化；来源由 Git 历史保留。
4. 同步调整受影响的测试合同、配置/schema 和使用说明，保留剩余功能仍需的回归。检查文档和命令入口，避免继续宣称提供已放弃的能力。
5. 按[完整生成与失败保护](#完整生成与失败保护)选择未占用的新快照名，在新目录从固定 base 干净生成并 `verify`；按实际影响验证剩余功能、受影响依赖闭包、相关回归及必要项目检查。保留历史已发布分支、tag 和资产，不覆盖旧快照。

## 同步准确上游版本

1. 在官方仓核实新 `refs/tags/vX.Y.Z` 指向的完整 commit；若是 annotated tag，核实 peeled commit。按该准确 ref 获取并校验，不用最近 tag 或 fork 同名 tag 顶替。
2. 在维护编辑分支更新 manifest 的 ref、base、application.branch；旧版本分支保留。用新目录从官方完整历史生成。遇到上游已有逻辑，按上述部分/完全替代规则逐组处理。
3. 每组按其新准确前序 tree 导出，真实依赖和顺序保持单一事实来源。分别重放闭包，跑全部 tests.json 与适用项目检查，核 schema、标签、daemon、路由和预期上游差异。
4. 运行工具自测、维护 diff/原始 patch 字节/应用 diff 检查、actionlint。核新生成 commit/tree，再提交维护变更供 PR 审阅。提交、推送与发布须符合当前授权。

## CI、SFA 与发布

根 CI 对 PR 合并预览及 main push 的精确 SHA 生成源码，运行维护工具自测、五组独立依赖闭包合同、完整分支合同、原有多平台 Go 测试及适用 lint。所有后续 job 从同一维护 SHA 重建，核对期望的应用 commit/tree。main 的精确提交须能通过 GitHub 只读 API 关联到已合并到 main 的 PR，且该次全部检查通过，才调用构建；直接初始化 main 仅验证。PR 不进入签名/发布 job，来自外部 fork 的 PR 也不需要写 token 或签名 secret。

构建复用旧 fork 有效 CLI/cronet、gomobile、NDK r28、JDK 17 与 Gradle 步骤。Android 子模块使用上游 gitlink 固定版本，不 checkout 浮动 main/dev。仅编译 arm64 libbox，两个原有 Android API 变体仍保留；必须从日志核实 transport/iwan 和 protocol/iwan 都被编译。稳定签名四 secret 缺一即失败，禁止临时密钥。签名文件仅存在于运行器并清理，不上传源码工作树。

发布标签为 `patchset-<application.branch>`，例如 `patchset-v1.14.4-patchset-2`，指向精确维护 SHA；Release 正文记录上游 base、应用 branch/commit/tree 和重建入口。它与官方 `v1.14.4`、应用版本分支是三种不同对象。资产来自该次核对后的构建，发布前核完整白名单；同名 Release/tag 不覆盖，不使用 --clobber、--replace 或 force。应用版本分支按当前授权用精确 refspec 推送，保留上游 workflow 的原文归档，不启用上游 workflow。

源码/指引/生成器输入改变须增加快照名；纯维护文档、测试合同或 workflow 调整可验证同一快照而不再发布。发布失败时先核 GitHub 上该次 Release/tag 状态，不盲目重试副作用。若资产构建需要修正而已存在旧发布，创建新快照版本，而非覆盖旧发布。

提交、推送、平台合并、默认分支调整和发布等动作须符合当前用户明确授权的范围；本地生成授权不自动扩展为远端操作授权。只记录实际完成的检查，不将未运行的 Actions、Android 签名构建或真实设备检查写成通过。
