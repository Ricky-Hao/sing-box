# Fallback 出站

按配置优先级选择首个健康出站，探测受 interval 预算约束；故障后切换，较高优先级连续成功后回切。包含空闲处理、已有连接中断、嵌套组、真实连接失败通知、daemon 探测入口、中英文配置文档和 schema。保留原有单元、并发及路由回归。

保留原因：准确上游没有该完整出站。限制和默认值以生成应用 `docs/configuration/outbound/fallback{,.zh}.md` 和原代码为准；成功阈值为现有内部常量，不新增配置。TCP 探测成功不代表应用层健康。

来源：最终源 fork `678d3c3bdf163ad33209f990d79348a334c586b0` 相对 `c005f35300ce76427dc285481ec5695f5b179cd2` 提取。历史 `fa4a1d960c698ec9e90c56fe78d8aa8c651560b3`、修正 `efd5345282e7d719d0837d91435f9dc5a9ada36a`、`b40ca6fcc8ea70428ee1f92aaaeb699578514928`、`194182bfe64ca14b29cc8b3a047ef60ac7f0c0c5` 已并为本组；`2d18736b29c9b82aa029d878e57f4c6f09dc51e8` 的 Fallback schema hunk 同组。

真实依赖：`tcp-urltest`，因为原 `TestFallbackCheckOutboundsSupportsTCPURL` 和预算回归使用 `tcp://`；`urltest-concurrency-fix`，因为原嵌套组测试调用 `newURLTestRaceGroup`，并复用先交付的 manager/race 夹具和文件上下文。后者是当前测试/应用依赖，Fallback 的生产策略没有调用 URLTest 锁方法。不依赖 iWAN 或 DNS 修复。

文件范围：`protocol/group/fallback*.go` 的功能/业务回归 hunk；`urltest.go` 的 `adapter.URLTestGroup` 嵌套探测 hunk；`adapter/outbound.go`、`option/group.go`、`daemon/started_service.go`、`route/{conn.go,fallback_test.go}`；Fallback 中英文配置/索引和客户端说明。共享常量、registry、schema、mkdocs 仅含 Fallback hunk。完整 18 文件清单见 manifest。

```sh
python3 tools/patchset.py generate --target .work/fallback --source .work/application --only fallback
python3 tools/patchset.py test --target .work/fallback --only fallback --tier all
```

选择会自动包含两个真实依赖，按 manifest 顺序一次应用。契约运行完整 group/route 回归和 race，并编译 daemon 接入；独立闭包不需要前面的 iWAN。

替代条件：新固定 base 覆盖完整策略、预算、连续成功回切、失败通知/嵌套/daemon/路由链路和对应回归；记录完整替代 SHA 与祖先/语义证据。部分上游替代仅删等价 hunk，不用补丁能应用作为等价证据。

本组是一项完整功能，调用方、接线、原有测试、配置/schema 和说明同组；历史修正并回本组。仅可独立应用/验证/淘汰的能力才拆组，文件数、跨层、提交数不作依据。共享文件按 hunk 单一归属；真实依赖；源码修改按准确前序 tree 回写 patch。
