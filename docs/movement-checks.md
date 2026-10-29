# Movement checks

`--check-movement` makes mkw-server watch each player's reported positions for movement the game can't produce. It's passive: the server never corrects, delays or rubber-bands anyone, so legit driving feels exactly the same, and the cost is a few comparisons per packet. Failures are logged; `--enforce-movement` turns them into strikes (and kicks, at the usual strike threshold).

## Data

From the community's protocol docs (https://mkwiiki.org/wiki/Network_Protocol):

- **Position** (RACEDATA 0x00-0x0a): three 28 bit values. Add `0x3f800000`, read as a float32, subtract 1e6. Precision is 1/16 unit.
- **Clock** (RACEHEADER_1 0x00): frames since the race started, counted by the game. Checks use it instead of arrival time, so lag spikes and jitter don't look like speed.
- **Exceptions** (RACEDATA): Lakitu (0x27 bits 0-1) and falldown (0x27 bit 7) mean a respawn is coming; the cannon bits (0x2f-0x30) and the Bullet Bill bit (0x29) flip when one starts.

## Checks

- **Average speed** over 30 frames must stay under 140 u/f (175 u/f for 10s after a Bullet Bill). The game caps normal driving at 120 u/f; Mushrooms and boost panels run at about 115, Bullet Bill at about 145. Averaging over half a second smooths out bumps, knockbacks and moving platforms.
- **Teleports:** a jump of more than 3000 units between two packets.
- Skipped while respawning (Lakitu or falling) and for 8s after a cannon.

Each race logs every player's top average speed, so the limits can be tightened from real races.

## Course-based checks

With `--course-dir` (the game's `/Race/Course` files), the movement check also uses each course's layout. Formats from the community's docs (https://mkwiiki.org/wiki/KMP_(File_Format), https://mkwiiki.org/wiki/Checkpoint, https://mkwiiki.org/wiki/KCL_(File_Format)).

- **Laps and rank (KMP CKPT/CKPH).** Each player is followed through the checkpoint zones (each checkpoint and the next form a convex quadrilateral on the ground) with the game's rules: crossing the lap counter forward adds a lap if every key checkpoint was reached this lap (the first crossing from the grid always counts), crossing it backward takes the lap away, and key checkpoints count when reached in order or ahead, so ultra shortcuts the game allows are allowed. Lap completion follows the game's layout: each checkpoint group depth is an equal share of the lap. Racers (bots included) are ranked by lap + completion. Item odds use the client's own rank (it sees the race as the player does, lag included), capped at the worst rank that's plausible: 1 + every racer ahead or within 0.05 laps (about 2s) behind. Claiming a better rank is always accepted; claiming last place from a clear lead isn't. Claims capped for over 3s are logged.
- **Respawns (KMP JGPT).** After Lakitu, the player must reappear within 1500 units (horizontally) of the respawn point for the checkpoint they were in, or one next to it. Battle arenas accept any respawn point.
- **Walls (KCL).** Every movement step between two packets is tested against the course's player walls (KCL base types 0x0C wall, 0x0D invisible wall, 0x0F wall 2, 0x14 player-only wall, 0x1F invisible wall 2), using a 512-unit grid so a step only tests nearby triangles. Only crossings from a wall's front side count (moving against its normal): from behind, players pass through, like one-way walls and down-facing ceilings (Bowser's Castle). Skipped while respawning, during cannons and Bullet Bill.

Tests drive every race course along its checkpoint centerline for 3 laps (`MKW_COURSE_DIR=... go test ./core`): every course counts exactly 3 laps and accepts every respawn point. All 707 item boxes and 302 respawn points on Nintendo's courses fall inside checkpoint zones.

- **Finishing (RACEHEADER_1 + MATCHDATA).** When a client first reports a finish time, the server must have them on lap 4 (the line crossed 3 times after the start), or within a packet of it. The claimed time must not be more than 0.5s faster than the race clock at the line allows: the clock runs 3.9s ahead of the on-screen timer (it includes the countdown; measured 3.79-3.93s on real finishes).

## Limits

- The wall check is only as good as the reported positions: a straight line between two packets can cut a sharp corner. A crude centerline run flagged 0.6% of steps, mostly from that test path cutting corners at a guessed height; real races decide whether it's quiet enough to enforce.
- Objects with their own collision (moving platforms, crushers) aren't in course.kcl and aren't checked.

## Mega Mushroom and lag gaps

- Mega multiplies the 120 u/f cap by the vehicle's Mega scale stat (1.6 to 2.6, MKWii TAS wiki "Velocity"), so up to 312 u/f. For 12 s after a server-granted Mega is used the limit is 120 u/f times that vehicle's Mega scale (kartParam.bin 0x184, 1.55-2.70) x 1.15 + 10; 340 u/f without stats. Conveyors, currents and moving roads count toward the same cap, so Mega plus a conveyor or a Mushroom stays under it.
- A teleport needs a jump over 3000 units and over twice what the speed limit allows for the frames between the two packets, so a lag gap isn't flagged.
- Known legit spikes above 140 u/f (wall-clip glitches ~170, the Koopa Cape reject-road pipe >200) are short; the 30-frame average mostly absorbs them, and movement is log-only unless -enforce-movement is set.
- A Lightning shrinks Mega players back to normal, so when anyone uses one, every other player's Mega limit ends right away.
- A Bullet Bill's length varies (longer the further back it's used), and race packets only show when one starts. It counts as over once the average speed has been Bullet Bill speed (130+ u/f) and drops back under 122 u/f, or after 10 s at most. That end is provisional: a bill that gets stuck on a wall slows down too, so if the speed climbs back to Bullet Bill speed within the 10 s it counts as active again.

## Enforcement

With `-enforce-movement`, failures within 2 s of each other count as one violation, and the first violation kicks the player with the cheating message. The finish check (claiming to finish before the server's lap count, or a time faster than the race clock allows) runs once per race and kicks on its first failure.

## Offroad

The floor under each position comes from the course KCL (types 0x02 weak offroad, 0x03 offroad, 0x04 heavy offroad, looked for from 150 above to 200 below the position). Offroad caps speed at the player's combo's multiplier for that floor type times 120 u/f, with 15% + 5 u/f slack. The multipliers are the vehicle's plus the character's, from kartParam.bin and driverParam.bin in Common.szs (slot i = KCL type i): about 0.55-0.91 on weak offroad, 0.33-0.71 on offroad, 0.19-0.42 on heavy offroad. The combo comes from each player's RaceInfo; Miis and unknown values get the most lenient value. The cap only applies after 60 frames on offroad with no boost, to speed averaged after that point (vehicles slow down gradually). Boosts reset that timer: the Mushroom boost flag, Star/Mega flips (10 s), mini-turbo and trick flips and boost panels/ramps (90 frames), Bullet Bill, Mega, a leaf pile in the last 3 s, and cannons. Racing-line check: on most courses no CPU path point is on offroad; a few corner cuts are, which the settle time covers.

Shrunk and squished (70% each, stacking) aren't enforced yet: their durations aren't known.

Top speed outside boosts isn't per combo: every vehicle is held under the same 120 u/f cap, boosts bring any combo near it, and the boost multipliers aren't documented. Shocked scale (kartParam.bin 0x188, 0.40-0.70 by vehicle) is loaded for a future shrunk limit.

## No-hit checks (log only)

A player's own game decides whether an item hits them. The server expects a hit when it's certain: a Star, Mega or Bullet Bill user within 350 units (1.5 s to show it), a Lightning on everyone not invincible (3 s), a Blue Shell on someone in the room (25 s, leader named). A hit shows as a RACEDATA 0x2C hit flag flipping (Star/Mega, Fake Item Box, Blue Shell/Bob-omb, Goomba) or the speed falling to 60% or to 30 u/f. Becoming invincible in time counts as a dodge. Misses are logged as "possible no-hit code".

## Squish (log only)

A Thwomp or Toad's Factory press multiplies speed by 0.7 for at least 240 frames (Kinoko). From 30 to 240 frames after the RACEDATA Thwomp flag flips, a 30-frame average over 102 u/f (0.7 x 120 x 1.15 + 5) is logged. Mega squishes have no flag.
