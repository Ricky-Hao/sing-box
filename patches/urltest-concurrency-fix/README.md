# URLTest 选择并发修复

为既有 URLTest 的 TCP/UDP 选择读写加锁，Selected、References、DialContext、ListenPacket 和选择更新使用一致快照；锁不跨网络 I/O。保留三项原有回归：所有请求入口使用当前选择、并发更新和请求、阻塞拨号不阻塞选择更新。

保留原因：准确上游仍直接并发读写 selected outbound。依赖：无，独立于 Fallback 和 TCP URL 探测。这里不带 Fallback 类型、策略或嵌套组探测。

来源：旧 fork `678d3c3bdf163ad33209f990d79348a334c586b0` 相对 `c005f35300ce76427dc285481ec5695f5b179cd2` 的净差异；历史 `194182bfe64ca14b29cc8b3a047ef60ac7f0c0c5` 中的 URLTest 锁和回归部分。所有上游合并修正包含在最终源 SHA，不另保留历史修正层。

范围：`protocol/group/urltest.go` 的锁/快照 hunk、`urltest_race_test.go`；以及 `fallback_test.go` 中的通用 outbound manager 夹具、`fallback_race_test.go` 中的通用 race outbound 夹具。两个文件名来自原 fork，此阶段只有夹具，没有 Fallback 实现或业务测试。后续 Fallback 用自己的 hunk 补齐文件，最终文件字节与旧 fork 一致。精确范围见 manifest。

```sh
python3 tools/patchset.py generate --target .work/urltest-lock --source .work/application --only urltest-concurrency-fix
python3 tools/patchset.py test --target .work/urltest-lock --only urltest-concurrency-fix --tier all
```

契约用 `go test -race -run ^TestURLTestSelection ./protocol/group` 实际运行原回归；不把编译当作并发证据。限制：仅修复选择状态，不改变探测策略。

替代条件：新固定 base 的选择状态同步和不跨网络 I/O 的语义等价，原回归通过；记录完整替代 SHA、祖先关系和检查结果。Fallback 仍需的夹具要保留其明确归属，不因锁上游化而丢失有效测试。

本组交付可独立应用/验证/淘汰的修复，夹具是完成原测试所需部分；不按层、文件或历史提交拆组。历史修正并回本组，共享文件按 hunk 单一责任，真实依赖，源码修改按准确前序 tree 回写 patch。
