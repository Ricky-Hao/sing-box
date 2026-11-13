# TCP URLTest

公共 `common/urltest` 支持 `tcp://host:port`，只测 TCP connect，不产生 HTTP 或 TLS 探测；校验 host/port 并传播解析、拨号错误，连接在测量后关闭。保留现有 HTTP(S) 默认行为。回归和 URLTest 中英文配置说明同组交付。

来源：旧 fork `678d3c3bdf163ad33209f990d79348a334c586b0` 相对上游 `c005f35300ce76427dc285481ec5695f5b179cd2` 的最终净差异；能力原随 `194182bfe64ca14b29cc8b3a047ef60ac7f0c0c5` 进入 fork，上游合并修正已折叠到最终内容。当前没有额外独立修正补丁。

保留原因：准确上游没有纯 TCP URL 探测；多个组可直接使用公共能力。依赖：无。Fallback 的现有测试使用它，方向为 Fallback 依赖本组。

范围：`common/urltest/{urltest.go,urltest_test.go}`、`docs/configuration/outbound/urltest{,.zh}.md`，精确清单见 manifest。限制：连接成功只证明 TCP connect，不证明应用协议或设备业务健康；必须显式指定端口。

```sh
python3 tools/patchset.py generate --target .work/tcp-urltest --source .work/application --only tcp-urltest
python3 tools/patchset.py test --target .work/tcp-urltest --only tcp-urltest --tier all
```

`tests.json` 真实执行公共探测回归及 race。替代需新固定 base 包含同等 TCP 行为、错误传播和回归，给出完整替代 SHA 与祖先/语义证据；部分替代只移除对应 hunk。

本组是一项完整公共能力，必要调用路径、原有测试和使用文档同组；历史修正并回本组。只有可独立应用/验证/淘汰才拆组，不按文件、层或提交数量拆分。共享文件按 hunk，登记真实依赖；开发修改必须按准确前序 tree 回写 patch。
