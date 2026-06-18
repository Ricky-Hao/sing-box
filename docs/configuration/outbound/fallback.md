### Structure

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

### Fields

#### outbounds

==Required==

List of outbound tags to test.

The order is used as priority from high to low. The first available outbound will be selected.

#### url

The URL to test. `https://www.gstatic.com/generate_204` will be used if empty.

#### interval

The test interval. `3m` will be used if empty.

`interval` also limits the maximum duration of each URL test. When a test exceeds `interval`, the outbound is considered unavailable for this check.

#### idle_timeout

The idle timeout. `30m` will be used if empty.

Health checks stop after this outbound is idle for the configured timeout, and restart when the outbound is used again.

#### interrupt_exist_connections

Interrupt existing connections when the selected outbound has changed.

Only inbound connections are affected by this setting, internal connections will always be interrupted.
