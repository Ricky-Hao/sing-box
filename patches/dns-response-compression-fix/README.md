# DNS 响应压缩修复

修复 DNS UDP/TCP 响应压缩、打包长度与截断；保留 EDNS 语义并避免修改共享 OPT/EDNS 记录。UDP 按请求预算截断，TCP 使用完整压缩响应。保留所有原有长度、可解码、截断和共享记录回归。

来源：旧 fork 最终 `678d3c3bdf163ad33209f990d79348a334c586b0` 相对上游 `c005f35300ce76427dc285481ec5695f5b179cd2` 的净差异；原实现 `eefd3af1900d4b6f833dcb7576ea8ec23f1a79b5` 和后续夹具 lint 修正 `e1d9234536dd150fa3df71d5ecc5c642d5182d97` 同组。

保留原因：准确上游缺少这些修复。依赖：无；不涉及 DNS 服务器选择或系统 resolver。范围为 `dns/{client_truncate.go,client_truncate_test.go}` 和 `protocol/dns/{handle.go,handle_test.go}`，精确清单见 manifest。

```sh
python3 tools/patchset.py generate --target .work/dns-compression --source .work/application --only dns-response-compression-fix
python3 tools/patchset.py test --target .work/dns-compression --only dns-response-compression-fix --tier all
```

`tests.json` 运行两个包的真实回归及 race。替代需固定新 base 已有等价 UDP/TCP 压缩、尺寸、截断和共享 EDNS 保护，附完整 SHA、祖先关系及原回归结果。部分替代按 hunk 删除，测试未上游时继续保留有效回归。

本组交付完整独立修复，必要调用方和原有测试同组；历史修正并回本组。只有独立应用/验证/淘汰的能力才拆组，不按文件、跨层和提交数拆分。共享文件按 hunk 单一责任、真实依赖；源码修改必须按准确前序 tree 回写 patch。
