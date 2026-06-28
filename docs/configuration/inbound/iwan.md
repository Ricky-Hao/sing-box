# iWAN

### Structure

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

The iWAN inbound accepts iWAN endpoint clients and routes packets through sing-box routing rules like a TUN inbound.

The client side should use the iWAN endpoint in `endpoints`.

On Linux, `system: true` enables the iWAN server system TUN path. DATA packets are decapsulated into a kernel TUN interface, and return packets read from the TUN interface are encapsulated back to the owning iWAN session. This bypasses sing-box routing and requires the host system to provide forwarding, firewall, and NAT policy when clients need access beyond local services.

### Fields

#### listen

Listen address.

#### listen_port

Listen port.

Default is `4567`.

#### system

Enable Linux system TUN server mode.

When enabled, packets bypass sing-box routing rules. The process needs permission to create a TUN interface, and the host must be configured for the intended L3 forwarding/NAT behavior.

#### interface_name

Linux system TUN interface name.

By default, an unused name with the `iwan` prefix is selected.

#### address_pool

Required IPv4 `/24` address pool used to assign tunnel addresses to clients.

The first usable address is reserved by the server; dynamic clients are assigned from the remaining pool.

#### username / password

Single-user authentication.

Conflicts with `users`.

#### users

Multi-user authentication.

Each user has:

```json
{
  "username": "user",
  "password": "password",
  "address": "10.66.0.2"
}
```

`address` is optional and must be inside `address_pool`.

#### mtu

iWAN tunnel MTU.

Default is `1400`.

#### encrypt

Enable iWAN `DATA_ENC` XOR obfuscation.

This is protocol compatibility, not modern transport security.

#### dns

Optional IPv4 DNS servers sent in OPENACK.

At most two addresses are supported.

#### session_timeout

Idle session timeout.

By default, the iWAN data timeout is used.

### Route example

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

### Limitations

- Requires `with_iwan` and `with_gvisor` build tags.
- `system` mode is only supported for iWAN inbound servers on Linux.
- IPv4 tunnel address assignment only.
- Segment routing and transmit fragmentation are not supported.
- Third-party iWAN client compatibility is not guaranteed.
