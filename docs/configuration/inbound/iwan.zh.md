# iWAN

### 结构

```json
{
  "type": "iwan",
  "tag": "iwan-in",
  "listen": "::",
  "listen_port": 4567,
  "system": false,
  "interface_name": "iwan0",
  "address_pool": "10.66.0.0/24",
  "users": [
    {
      "username": "user",
      "password": "password"
    }
  ],
  "mtu": 1400,
  "encrypt": false
}
```

iWAN 入站接受 iWAN endpoint 客户端，并像 TUN 入站一样把数据交给 sing-box 路由规则处理。

客户端应继续在 `endpoints` 中使用 iWAN endpoint。

在 Linux 上，`system: true` 会启用 iWAN 服务端 system TUN 路径。DATA 包会解封装后写入内核 TUN 接口，回包从 TUN 接口读出后再封装回对应 iWAN session。该模式会绕过 sing-box 路由规则；如果客户端需要访问本机服务之外的网络，宿主机需要自行配置转发、防火墙和 NAT。

### 字段

#### listen

监听地址。

#### listen_port

监听端口。

默认值为 `4567`。

#### system

启用 Linux system TUN 服务端模式。

启用后数据包会绕过 sing-box 路由规则。进程需要具备创建 TUN 接口的权限，宿主机也需要按预期配置 L3 转发/NAT。

#### interface_name

Linux system TUN 接口名称。

默认会选择未使用的 `iwan` 前缀接口名。

#### address_pool

必填，IPv4 `/24` 地址池，用于给客户端分配隧道地址。

服务端保留第一个可用地址；动态客户端从剩余地址中分配。

#### username / password

单用户认证。

与 `users` 冲突。

#### users

多用户认证。

每个用户格式如下：

```json
{
  "username": "user",
  "password": "password",
  "address": "10.66.0.2"
}
```

`address` 可选，必须位于 `address_pool` 内。

#### mtu

iWAN 隧道 MTU。

默认值为 `1400`。

#### encrypt

启用 iWAN `DATA_ENC` XOR 混淆。

这是协议兼容特性，不是现代传输安全机制。

#### dns

可选，随 OPENACK 下发的 IPv4 DNS 服务器。

最多支持两个地址。

#### session_timeout

空闲会话超时。

默认使用 iWAN 数据超时时间。

### 路由示例

```json
{
  "inbounds": [
    {
      "type": "iwan",
      "tag": "iwan-in",
      "listen": "::",
      "listen_port": 4567,
      "address_pool": "10.66.0.0/24",
      "username": "user",
      "password": "password"
    }
  ],
  "route": {
    "rules": [
      {
        "inbound": "iwan-in",
        "outbound": "proxy"
      }
    ],
    "final": "direct"
  }
}
```

### 限制

- 需要 `with_iwan` 和 `with_gvisor` 构建标签。
- `system` 模式仅支持 Linux iWAN 入站服务端。
- 仅支持 IPv4 隧道地址分配。
- 不支持段路由和发送方向分片。
- 不保证兼容第三方 iWAN 客户端。
