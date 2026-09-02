# iWAN

### Structure

```json
{
  "type": "iwan",
  "tag": "iwan-in",
  "listen": "::",
  "listen_port": 4567,
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

!!! warning "Security"

    Compatibility with legacy iwand uses MD5-signed control packets, a reversible AES password block derived from the username, and XOR data obfuscation. It does not provide modern confidentiality, peer authentication, or replay resistance against an on-path or passive observer. Deploy it only inside a trusted network or authenticated encrypted tunnel.

### Fields

#### listen

Listen address.

#### listen_port

Listen port.

Default is `4567`.

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
- IPv4 tunnel address assignment only.
- Segment routing and transmit fragmentation are not supported.
- Third-party iWAN client compatibility is not guaranteed.
