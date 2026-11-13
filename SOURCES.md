# 来源与许可

- 应用上游：[SagerNet/sing-box](https://github.com/SagerNet/sing-box)，固定 `refs/tags/v1.14.4`，commit `c005f35300ce76427dc285481ec5695f5b179cd2`。根 [LICENSE](LICENSE) 原文保留（GNU GPL v3）；生成应用继续保留上游许可、完整历史和 Go module。
- fork 净差异来源：[Ricky-Hao/sing-box](https://github.com/Ricky-Hao/sing-box/tree/678d3c3bdf163ad33209f990d79348a334c586b0)，commit `678d3c3bdf163ad33209f990d79348a334c586b0`。各组 README 记录原实现与后续修正完整 SHA。补丁按该最终 tree 提取，没有机械重放历史提交。
- 维护工具直接参考：[Ricky-Hao/new-api](https://github.com/Ricky-Hao/new-api/tree/7ca7f4112b06b26222946033d2fb038fe6223c41)，commit `7ca7f4112b06b26222946033d2fb038fe6223c41` 的 `tools/patchset.py`、`tools/test_patchset.py`、manifest、维护文档、CI 与开发环境。借用的是其独立标注 MIT 的维护工具，未复制 new-api 应用代码。
- 工具原始来源：[Ricky-Hao/LibreChat](https://github.com/Ricky-Hao/LibreChat/tree/f02d9b1d81a5e61661d69bea05e9278c162a3fd7)，commit `f02d9b1d81a5e61661d69bea05e9278c162a3fd7`。工具版权与 MIT 许可保留于 [tools/LICENSE.LibreChat](tools/LICENSE.LibreChat)。组文档和维护说明参考其 patches 与 docs/maintenance.md 的组织方式。

维护工具在上述实现上适配 sing-box 的真实功能组、可复现版本分支、并发保护、应用指引与上游 workflow 原文归档。CI 构建命令来自旧 fork 及其上游 workflow；维护层调整生成目录、精确来源、触发入口与既定产物范围。生成分支 `.github/upstream-workflows/` 保存该上游版本所有 workflow 的原始字节，作为无触发作用的参考。
