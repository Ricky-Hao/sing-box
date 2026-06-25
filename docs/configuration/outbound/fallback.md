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

Use `tcp://host:port` to perform a pure TCP connect probe without HTTP or TLS overhead.

#### interval

The maximum health check budget. `3m` will be used if empty.

Fallback internally splits this value into probe scheduling time and probe timeout:

* probe timeout: `min(15s, interval / 2)`
* probe scheduling time: `interval - probe timeout`

This prevents one slow URL test from occupying the whole interval and blocking the next scheduled check.

To reduce flapping on weak or switching networks without increasing failover time, scheduled background checks mark a failed outbound unavailable immediately, but require two consecutive successful background probes before failing back to a higher-priority recovered outbound. Forced checks and real connection failures still update availability immediately.

#### idle_timeout

The idle timeout. `30m` will be used if empty.

Health checks stop after this outbound is idle for the configured timeout, and restart when the outbound is used again.

#### interrupt_exist_connections

Interrupt existing connections when the selected outbound has changed.

Only inbound connections are affected by this setting, internal connections will always be interrupted.
