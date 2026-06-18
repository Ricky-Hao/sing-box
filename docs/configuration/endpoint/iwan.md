# iWAN

!!! quote ""

    iWAN is an endpoint for the Panabit iWAN SD-WAN UDP tunnel protocol.

!!! warning ""

    iWAN support is userspace stack only in this version. `system` TUN mode and segment routing are not supported yet.

### Version scope

This endpoint implements the single-hop iWAN tunnel data path used by iwand:

- OPEN, OPENACK, OPENREJ, DATA, DATA_ENC, ECHO, and CLOSE packets
- server-assigned IPv4 tunnel address from OPENACK
- optional XOR data encryption
- pipe ID and pipe index
- IPFRAG receive reassembly

This is not a 1:1 replacement for the iwand daemon. The following iwand features are intentionally not included in this version:

- system TUN interface creation and OS route management
- segment routing (`SEGRT`) and segment-routing IP fragments (`IPFRAG_SR`)
- `up_script` and `down_script` hooks
- applying DNS servers from OPENACK to the system resolver

To use this endpoint, build sing-box with both `with_iwan` and `with_gvisor` tags.

### Structure

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

### Fields

#### server

==Required==

The iWAN server address.

#### server_port

The iWAN server port.

`4567` is used by default.

#### username

==Required==

The iWAN username.

#### password

==Required==

The iWAN password.

Only the first 16 bytes are used by the protocol password block.

#### mtu

The client MTU.

`1400` is used by default. The valid range is `46` to `1600`.

After OPENACK, sing-box uses the smaller valid value between the configured MTU and the server MTU.

#### encrypt

Enable iWAN XOR encryption for data packets.

#### address

Optional expected tunnel address prefix.

The actual tunnel IPv4 address is assigned by the iWAN server in OPENACK. If `address` is set and the server assigns a different address, sing-box uses the server address and logs a warning.

#### allowed_ips

Optional route preference prefixes for `preferred_by` rules. These prefixes do not install system routes; they only help sing-box prefer this endpoint for matching destinations.

#### pipe_id

Optional iWAN pipe ID. Valid values are `0` to `32767`.

#### pipe_index

Optional iWAN pipe index. Valid values are `0` and `1`.

#### udp_timeout

UDP timeout for the userspace stack.

#### system

Not supported yet. If set to `true`, sing-box returns an error. Leave it unset or set it to `false`.

### Dial Fields

See [Dial Fields](/configuration/shared/dial/) for details.
