# mkw-server

mkw-server is the WIP client server component of MKW-Server. Clients send [Race Packets](https://wiki.tockdom.com/wiki/Network_Protocol#Race_Packets) to mkw-server, which broadcasts them to other clients. mkw-server instances are managed by [wfc-server](https://github.com/mkw-server/wfc-server).

## Current Support

- Transmitting Race Packets between clients
- Server authority over Race packets: every record is parsed and validated against what the server knows about the sender (its aid, guest, room membership and friend code). Invalid packets are dropped instead of relayed.
- Kicking aids that keep sending invalid packets, and notifying wfc-server
- Server-sided item decisions (needs client support, see [docs/server-item-decisions.md](docs/server-item-decisions.md))

## Options

| Flag | Default | Description |
|---|---|---|
| `--room-addr` | | UDP address (ip:port) the room listens on. Required. |
| `--wfc-addr` | | TCP address (ip:port) of wfc-server. Required. |
| `--kick-threshold` | `30` | Kick an aid after this many invalid packets within `--kick-window`. `0` disables kicks. |
| `--kick-window` | `10s` | Window for counting invalid packets towards a kick. |
| `--item-slot` | | Path to ItemSlot.bin. Enables server-side item decisions. |
| `--enforce-items` | `false` | Drop packets with items the server didn't grant. Requires `--item-slot`. |

## wfc-server Messages

Besides AddAid (`0x01`) and RemoveAid (`0x02`), mkw-server accepts KickAid (`0x03`, followed by ip u32 and port u16) to stop relaying an aid. When mkw-server kicks an aid on its own, it sends wfc-server AidKicked (`0x02`, followed by ip u32, port u16, aid u8 and reason u8, where reason `1` means invalid packets).

## Setup

1. Set up [wfc-server](https://github.com/mkw-server/wfc-server). Refer to its README for setup instructions.
2. Run `go build` to build the `mkw-server` executable.
3. Add the path to the `mkw-server` executable in [wfc-server's config.xml](https://github.com/mkw-server/wfc-server). This allows wfc-server to locate and launch mkw-server instances.
