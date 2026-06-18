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

测试间隔。默认使用 `3m`。

#### idle_timeout

空闲超时。默认使用 `30m`。

当该出站空闲超过配置的超时时间后，健康检查会停止；再次使用该出站时重新启动健康检查。

#### interrupt_exist_connections

当选定的出站发生更改时，中断现有连接。

仅入站连接受此设置影响，内部连接将始终被中断。
