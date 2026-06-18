# iWAN

!!! quote ""

    iWAN 是 Panabit iWAN SD-WAN UDP 隧道协议的端点。

!!! warning ""

    当前版本的 iWAN 仅支持用户态 stack 模式。暂不支持 `system` TUN 模式和 segment routing。

### 版本范围

该端点实现 iwand 使用的单跳 iWAN 隧道数据面：

- OPEN、OPENACK、OPENREJ、DATA、DATA_ENC、ECHO 和 CLOSE 包
- 来自 OPENACK 的服务器分配 IPv4 隧道地址
- 可选 XOR 数据加密
- pipe ID 和 pipe index
- IPFRAG 接收端重组

这不是 iwand daemon 的 1:1 替代品。当前版本有意不包含以下 iwand 功能：

- 创建系统 TUN 网卡和管理系统路由
- segment routing（`SEGRT`）和 segment-routing IP fragment（`IPFRAG_SR`）
- `up_script` 和 `down_script` hook
- 将 OPENACK 中的 DNS 服务器应用到系统解析器

使用该端点时，需要同时使用 `with_iwan` 和 `with_gvisor` 标签构建 sing-box。

### 结构

```json
{
  "type": "iwan",
  "tag": "iwan-out",
  "server": "1.2.3.4",
  "server_port": 4567,
  "username": "myuser",
  "password": "mypass",
  "mtu": 1400,
  "encrypt": false,
  "allowed_ips": [
    "0.0.0.0/0"
  ]
}
```

### 字段

#### server

==必填==

iWAN 服务器地址。

#### server_port

iWAN 服务器端口。

默认使用 `4567`。

#### username

==必填==

iWAN 用户名。

#### password

==必填==

iWAN 密码。

协议密码块只使用前 16 字节。

#### mtu

客户端 MTU。

默认使用 `1400`。有效范围是 `46` 到 `1600`。

OPENACK 后，sing-box 会使用配置 MTU 和服务器 MTU 中较小的有效值。

#### encrypt

启用 iWAN 数据包 XOR 加密。

#### address

可选的预期隧道地址前缀。

实际隧道 IPv4 地址由 iWAN 服务器在 OPENACK 中分配。如果设置了 `address` 且服务器分配了不同地址，sing-box 会使用服务器地址并记录警告。

#### allowed_ips

用于 `preferred_by` 规则的可选路由偏好前缀。这些前缀不会安装系统路由，只用于帮助 sing-box 为匹配目标优先选择该端点。

#### pipe_id

可选 iWAN pipe ID。有效值为 `0` 到 `32767`。

#### pipe_index

可选 iWAN pipe index。有效值为 `0` 和 `1`。

#### udp_timeout

用户态 stack 的 UDP 超时时间。

#### system

暂不支持。如果设置为 `true`，sing-box 会返回错误。请保持未设置或设置为 `false`。

### 拨号字段

参阅 [拨号字段](/zh/configuration/shared/dial/)。
