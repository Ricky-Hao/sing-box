# iWAN 完整能力

保留原因：上游 v1.14.4 没有 iWAN；本组交付客户端 endpoint、服务端 inbound 和共同协议，以及 gVisor 数据路径、握手/认证/会话/重连/就绪等待、地址池/固定用户地址、路由和返回分发。现存 socket 缓冲、向量写入、Linux 批量 UDP 收发和其他数据路径优化全部保留，不拆成性能组。

必须同时启用 `with_iwan,with_gvisor`；include/stub、CLI 三份默认标签、mobile/libbox 标签和标签回归、必要 lint 接线都在本组。维护 main 决定构建矩阵、工具链、SFA 签名和发布入口。

限制：仅 userspace stack；不支持 `system: true`。可省略 `address`，有效 IPv4 来自 OPENACK；`allowed_ips` 只影响 `preferred_by`，不安装 OS 路由。保留 iwand 的 MD5、AES-128 密码 TLV、XOR、token/session 和 TLV 长度语义；这些旧协议机制不提供现代传输安全保证。UDP 来源检查不可省略。分段路由、系统路由、up/down scripts、将 OPENACK DNS 写入系统 resolver 均不在能力范围。移动 5G 从 MTU 1280 开始，TUN MTU 保持一致。

来源为旧 fork 最终净差异 `678d3c3bdf163ad33209f990d79348a334c586b0`，相对上游 `c005f35300ce76427dc285481ec5695f5b179cd2` 提取，非历史 cherry-pick。历史来源及并入的修正：

- `e48d647c503384bbaf797d6f1bd01475a28d0002` endpoint；`14cf397e494d3471f354a103ba9a6d05841e7249` mobile 标签。
- `0a671bf6fb36c6c60c9f3648f4f1b739d4fe29e6` 就绪等待；`72964fa9ba5b10f0563e8cbdf47bbcae07645e49` inbound。
- `92b6add3fc56f68611de53a9e869d4e0de68ef74` lint；`612af3db9587573b8d6e1677e51869121461b95a` UDP buffering。
- `b8ed9773a3c725ea549c54c1472ea8b5b5492475` 路由服务端路径；`d1f3cacf4dda8c023d442860c3dbc6d4cb21b2cc` userspace 优化。
- `b201412cb4baaddb9477a153c2b8b09450ce6bb1` Linux batch；`2f89d67287fcbbf7caf4c0afa1f7e5f1f640c034` routed data path。
- `2d18736b29c9b82aa029d878e57f4c6f09dc51e8` schema 的 iWAN 部分；`ba61c0a31ceadc3dda9eb6130cdb299bf10363de` 安全限制。上游合并后的最终内容以源 fork SHA 为准，历史标题不扩大当前支持范围。

依赖：无其他业务组。gVisor 依赖已存在于准确上游 go.mod，不改 module 或依赖版本。

文件范围：`transport/iwan/*`、`protocol/iwan/*`、`option/iwan*.go`、`include/iwan*.go`、`cmd/internal/build_libbox/{main.go,main_test.go}`、`release/DEFAULT_BUILD_TAGS*`、`.golangci.yml`、iWAN 中英文 endpoint/inbound 文档和索引、构建说明；共享 `constant/proxy.go`、`include/registry.go`、`docs/schema.json`、`mkdocs.yml` 只含 iWAN hunk。精确文件清单为 manifest 的本项 `paths`。

从维护根运行真实验证：

```sh
python3 tools/patchset.py test --target .work/application --only iwan --tier all
# 独立闭包：生成 .work/iwan 后执行相同的 test --only iwan
python3 tools/patchset.py generate --target .work/iwan --source .work/application --only iwan
python3 tools/patchset.py test --target .work/iwan --only iwan --tier all
```

契约覆盖 tagged endpoint/server/protocol/option、include 与无标签 stub、mobile 标签和 race；本地回环测试不代表真实设备验收。配置示例和使用限制随应用生成于 `docs/configuration/{endpoint,inbound}/iwan{,.zh}.md`。

替代条件：固定新 base 已完整实现上述协议、客户端/服务端、构建入口和回归，才可整体淘汰。部分上游化时只删已等价的 hunk，保留其余实现和测试；按维护文档记录完整替代 SHA、祖先关系和逐项行为证据。

本组按 feature 维护：完整能力、调用方/接线/原有测试/文档同组，历史修正并回本组；只有可独立应用、验证、淘汰的能力才拆分，文件数、跨层和提交数不作为依据。共享文件按 hunk 单一归属，源码修改必须回写 patch；导出使用准确前序 tree。
