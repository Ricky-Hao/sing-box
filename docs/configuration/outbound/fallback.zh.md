### 结构

```json
{
  "type": "fallback",
  "tag": "fallback",

  "outbounds": [
    "proxy-a",
    "proxy-b",
    "proxy-c"
  ],
  "url": "",
  "interval": "",
  "idle_timeout": "",
  "interrupt_exist_connections": false
}
```

### 字段

#### outbounds

==必填==

用于测试的出站标签列表。

列表顺序即优先级，从高到低选择第一个可用的出站。

#### url

用于测试的链接。默认使用 `https://www.gstatic.com/generate_204`。

#### interval

最大健康检查预算。默认使用 `3m`。

Fallback 会在内部把该值拆分为探测调度时间和探测超时：

* 探测超时：`min(15s, interval / 2)`
* 探测调度时间：`interval - 探测超时`

这样可以避免单次较慢的 URL 测试占满整个 interval，阻塞下一轮定时检查。

#### idle_timeout

空闲超时。默认使用 `30m`。

当该出站空闲超过配置的超时时间后，健康检查会停止；再次使用该出站时重新启动健康检查。

#### interrupt_exist_connections

当选定的出站发生更改时，中断现有连接。

仅入站连接受此设置影响，内部连接将始终被中断。
