# Server-sided Item Decisions

Design for [#21](https://github.com/mkw-server/mkw-server/issues/21). The server side is implemented in this repo (`items/`, `core/server_items.go`); the client side still needs a patch in [mkw-server-client](https://github.com/mkw-server/mkw-server-client).

## Problem

In vanilla Mario Kart Wii, each console rolls its own items when it touches an item box and tells everyone else what it got through ITEM and EVENT records. Nobody can check that the roll was legitimate, so item hacks (always getting Blue Shells, using items you never rolled) can't be detected.

## Approach

Every Race packet already goes through mkw-server, so the server can make the roll itself and refuse packets that use items it didn't hand out.

1. **The client asks.** When a player touches an item box, the client sends `IR` to mkw-server instead of rolling locally, then plays the roulette animation while it waits.
2. **The server rolls.** mkw-server picks the item from the game's own probability tables (ItemSlot.bin), the player's position, and server-side randomness the client can't predict.
3. **The server answers.** mkw-server sends `IT` with the item. The roulette lands on it.
4. **The server enforces.** With `--enforce-items`, packets whose ITEM or EVENT records show items the server never granted are dropped, and the sender gets a strike towards a kick.

## Protocol

Both messages travel over the same UDP socket as Race packets.

| Message | Direction | Layout |
|---|---|---|
| `IR` item request | client → server | `'I' 'R' playerIdx seq` (4 bytes) |
| `IT` item response | server → client | `'I' 'T' playerIdx seq item` (5 bytes) |

- `playerIdx` is 0 for the main player and 1 for a guest.
- `seq` is a per-request counter chosen by the client. If an `IT` is lost, the client resends the same `IR` with the same `seq`, and the server repeats its earlier answer instead of rolling again.
- `item` is an ITEM record item id (`common.ItemType`). `0x14` (no item) means the request was refused, for example because the player still holds an item or the race hasn't started.

## Decision

- **Table:** online VS (table 4), online Balloon Battle (10) or online Coin Runners (11), picked from the game mode in the player's latest RaceInfo.
- **Column, VS:** the player's rank from their latest RaceData. Rooms with fewer than 12 players are spread across all 12 columns, so 1st always gets 1st-place odds and last always gets 12th-place odds. The game also resamples its tables for small rooms; this formula approximates that until the exact one is confirmed.
- **Column, battle:** how far the player is behind the leader, using battle scores from RaceMode (Balloon Battle: <3, 3–5, >5 points; Coin Runners: <4, 4–7, >7 coins). Coin Runners' RaceMode layout isn't decoded yet, so coin counts are always 0 for now.
- **Limits:** after a strong item is handed out, it goes on a room-wide cooldown (defaults in `items.DefaultCooldowns`: Blue Shell, Lightning, POW and Thunder Cloud 30s, Blooper 15s). A roll that lands on an item on cooldown gives a Mushroom, the same thing the game does when its own limits block an item.
- **Randomness:** each room's engine is seeded from `crypto/rand`.

## Enforcement

Per player, the server tracks the current grant and the last item granted.

- **ITEM record, held item:** must be empty, the granted item (or its single version for triple items), or a Thunder Cloud, which players can pass to each other by bumping.
- **ITEM record, trailing item:** must be empty or the same kind of object as the last granted item.
- **EVENT record:** a newly filled *used* or *shot* slot must name the object of a local player's last granted item. Hit, drop and lose events involve other players' items and aren't checked.
- A grant ends once the player's ITEM record shows the item and then shows the slot empty again. A grant that never shows up expires after 15s, so a lost packet can't lock a player out of items.
- A new race (RaceInfo's countdown starting) resets all item state.

## Running it

```
mkw-server --room-addr ... --wfc-addr ... --item-slot /path/to/ItemSlot.bin [--enforce-items]
```

ItemSlot.bin comes from `/Race/Common.szs` on the game disc and isn't shipped with mkw-server. Without `--item-slot`, items behave like vanilla. With `--item-slot` but without `--enforce-items`, violations are only logged, which is the way to try the client patch before turning enforcement on.

## Client work (mkw-server-client)

- Hook the item box collision (the roll in `ItemHolder`/`ItemDirector`) so it sends `IR` instead of rolling.
- Keep the roulette spinning until `IT` arrives, resend `IR` with the same `seq` every ~250ms until it does, and give up (no item) after ~2s.
- Use the `IT` item as the roll result. Don't apply the game's own item limits on top of it.

## Known limitations

- **Rank is reported by the client.** RaceData's rank is trusted, so a client could claim last place to get better items. Fixing this means computing positions on the server from RaceData positions and each course's checkpoints (KMP), which is a separate project.
- **Item box contact isn't checked.** The server doesn't know where item boxes are, so it can't confirm the player actually touched one. The 1s minimum gap between grants and the one-item-at-a-time rule limit how much this can be abused.
- **Item object ids** in EVENT records follow the community-documented `ItemObjId` order. If any turn out wrong, enforcement will flag legitimate items, so check logs with enforcement off before turning it on.
