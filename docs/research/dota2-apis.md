# Dota 2 APIs & Integration Points for Live Game Data

> **Research Date:** 2026-07-21  
> **Purpose:** Survey all official Dota 2 / Steam APIs that can provide live or near-real-time game data for third-party tooling.

---

## 1. Valve Game State Integration (GSI)

GSI is a local HTTP-based integration built into the Source 2 engine (used by Dota 2). It pushes JSON payloads from a running game client to a user‑configured local web server.

### How it Works

1. Place a `.cfg` file in `<Dota2Install>/game/dota/cfg/gamestate_integration/`.
2. The Dota 2 client sends periodic POST requests (JSON) to `http://localhost:<port>/` (or any URI you configure).
3. Your local server parses the payload and reacts in real time.

### What Data GSI Exposes in Real Time

| Section | Fields |
|---------|--------|
| `provider` | Game name, Steam ID, app ID, version, timestamp |
| `map` | Map name, game time, clock time, match ID, game mode, game phase (e.g. `DOTA_GAMERULES_STATE_INIT` → `GAME_IN_PROGRESS`), team scores, win team |
| `player` (for the local player only) | Steam ID, hero name, level, kills, deaths, assists, last hits, denies, gold, GPM/XPM, net worth, position (x,y), abilities (level, cooldown, passive), items (slot, name, purchaser), stats (strength, agility, intelligence), respawn timer, buyback cooldown, courier, backpack, teleport cooldown |
| `hero` | Hero ID, name, level, alive/dead, buyback info |
| `abilities` | List of abilities with level, cooldown, passive flag |
| `items` | Item slots 0–5, backpack slots, teleport item, neutral item |
| `wearables` | Cosmetic items equipped on the hero |
| `inventory` | Full inventory state |
| `buildings` | (Sometimes available) Building health by lane |
| `draft` | (Pre-game) Radiant/Dire picks and bans |
| `events` | In-game events like rune pickups, kills, building destroys |

### Limitations

- **Local player only** – GSI only sends data for the player running the Dota 2 client, not all 10 players.
- **Private match required for offline/spectator** – For full match data in a coach/spectator scenario, you must be in a lobby.
- **No minimap/camera data.**
- **Payload frequency** is throttled by the engine (roughly every 1–2 seconds, configurable).

### Source / Config Example

The canonical GSI config file for Dota 2 is maintained at:  
[https://github.com/SteamTracking/GameTracking-Dota2](https://github.com/SteamTracking/GameTracking-Dota2)  
(A sample `gamestate_integration.cfg` is included in the repo under `game/dota/bin/…`.)

**Official Valve docs (currently behind captcha):**  
[https://developer.valvesoftware.com/wiki/Counter-Strike:_Global_Offensive_Game_State_Integration](https://developer.valvesoftware.com/wiki/Counter-Strike:_Global_Offensive_Game_State_Integration)  
(CS:GO GSI docs – GSI works identically for Dota 2.)

---

## 2. Dota 2 WebAPI (Steam Web API)

### Prerequisites

- Register for a free API key at: [https://steamcommunity.com/dev/apikey](https://steamcommunity.com/dev/apikey)
- Base URL: `https://api.steampowered.com/`
- Supported output formats: `json` (default), `xml`, `vdf`

### Dota 2–Specific Interfaces

#### IDOTA2Match\_<ID> (Match Data)

| Method | Description | Live/Historical |
|--------|-------------|----------------|
| `GetLeagueListing` | Info about DotaTV‑supported leagues | Historical |
| `GetLiveLeagueGames` | **In‑progress league/custom matches** with real‑time details | **Live** |
| `GetMatchDetails` | Full match info (players, items, kills, etc.) | Historical |
| `GetMatchHistory` | List of matches, filterable by player/hero/league | Historical |
| `GetMatchHistoryBySequenceNum` | Matches ordered by sequence number (for incremental sync) | Historical |
| `GetScheduledLeagueGames` | Upcoming scheduled league games | Historical |
| `GetTeamInfoByTeamID` | Team roster / info | Historical |
| `GetTournamentPlayerStats` | Player stats within a tournament | Historical |
| `GetTopLiveGame` | Currently featured live game | **Live** |

#### IDOTA2MatchStats\_<ID> (Real‑time Stats)

| Method | Description |
|--------|-------------|
| `GetRealtimeStats` | **Live in‑match stats** for a specific match (server ID + match ID required) – includes team scores, player stats, towers, barracks, Roshan, runes, etc. |

This is the closest official analog to GSI for remote spectator applications. However, documentation is sparse and the endpoint may only work for Valve‑sanctioned tournaments (DotaTV leagues).

#### IEconDOTA2\_<ID> (Economy / Static Data)

| Method | Description |
|--------|-------------|
| `GetGameItems` | List of in‑game items |
| `GetHeroes` | List of all heroes |
| `GetItemIconPath` | URL template for item icons |
| `GetRarities` | Item rarity categories |
| `GetTournamentPrizePool` | Current prize pool |
| `GetEventStatsForAccount` | Event progress for an account |

#### IDOTA2Fantasy\_<ID> (Fantasy League)

| Method | Description |
|--------|-------------|
| `GetFantasyPlayerStats` | Fantasy‑relevant stats |
| `GetPlayerOfficialInfo` | Official player info |

#### IDOTA2StreamSystem\_<ID> (Streaming)

| Method | Description |
|--------|-------------|
| `GetBroadcasterInfo` | Broadcaster info for streaming |

#### IDOTA2Teams\_<ID> (Teams)

| Method | Description |
|--------|-------------|
| `GetTeamInfo` | Team information |

#### IDOTA2Ticket\_<ID> (Tickets)

| Method | Description |
|--------|-------------|
| `SetSteamAccountPurchased` | Mark ticket as purchased |
| `SteamAccountValidForEvent` | Check event eligibility |

### General Steam WebAPI Methods

| Interface | Methods |
|-----------|---------|
| `ISteamUser` | `GetPlayerSummaries`, `GetFriendList`, `GetPlayerBans`, `ResolveVanityURL` |
| `ISteamUserStats` | `GetNumberOfCurrentPlayers`, `GetGlobalAchievementPercentagesForApp`, `GetSchemaForGame`, `GetUserStatsForGame`, `GetPlayerAchievements` |
| `IPlayerService` | `GetRecentlyPlayedGames`, `GetOwnedGames`, `GetSteamLevel` |
| `ISteamApps` | `GetAppList`, `GetServersAtAddress` |
| `ISteamNews` | `GetNewsForApp` |

### Source Documentation

- TF2 Wiki WebAPI page (comprehensive, community‑maintained):  
  [https://wiki.teamfortress.com/wiki/WebAPI](https://wiki.teamfortress.com/wiki/WebAPI)
- Official Steam WebAPI docs (Valve, behind captcha):  
  [https://developer.valvesoftware.com/wiki/Steam_Web_API](https://developer.valvesoftware.com/wiki/Steam_Web_API)
- Dota 2 Community site:  
  [https://www.dota2.com/community/](https://www.dota2.com/community/)

---

## 3. Real‑Time Spectator & Coach APIs

### 3.1 DotaTV (In‑Client Spectator)

- Built into the Dota 2 client. Supports live spectating with full player vision or free camera.
- Exposed programmatically via **`IDOTA2Match.GetLiveLeagueGames`** and **`IDOTA2MatchStats.GetRealtimeStats`**.
- DotaTV uses Valve's custom tick‑based replay protocol (not HTTP). To capture live data, you must either:
  - Use **GSI** from within the client (local), or
  - Poll the **WebAPI** endpoints for league/lobby matches.

### 3.2 Coach API / Console Commands

- Dota 2 supports coaching via the in‑client system (no separate HTTP API).
- Coach sees **fog‑of‑war obscured** data (your own team's vision), not the full map.
- Coach can **speak** to the team via voice/chat.
- Coach mode is controlled through game state — there is no web‑based coach API.

### 3.3 `GetRealtimeStats` (WebAPI)

- The `IDOTA2MatchStats_570.GetRealtimeStats` endpoint returns live match JSON.
- Parameters: `server_steam_id`, `match_id`.
- Returns: live scores, player KDA, items, tower/barrack/Roshan state, runes.
- **Undocumented** — many fields are partial, and the endpoint may be restricted.

### 3.4 Third‑Party Live Data Platforms

Since Valve's official real‑time APIs are limited, the ecosystem relies on:

| Platform | Description | Data Source |
|----------|-------------|-------------|
| **STRATZ** | [https://stratz.com](https://stratz.com) – GraphQL API, live match data, player stats, hero builds | WebAPI + custom parsing of match replays |
| **OpenDota** | [https://www.opendota.com](https://www.opendota.com) – Free API for match history, live matches, player data | WebAPI + custom replay parsing |
| **Dotabuff** | [https://www.dotabuff.com](https://www.dotabuff.com) – Aggregated match stats and player profiles | WebAPI + replay data |
| **SteamDataBase** | [https://github.com/SteamDatabase/GameTracking-Dota2](https://github.com/SteamDatabase/GameTracking-Dota2) – Reverse‑engineered Protobuf definitions for the game's network protocol | Binary protocol analysis |

These platforms parse **match replays** (`.dem` files) rather than using a live push API. For truly real‑time data, GSI remains the primary option.

---

## 4. Summary Matrix

| Feature | GSI | Steam WebAPI | DotaTV / Coach |
|---------|-----|--------------|----------------|
| **Real‑time** | ✅ Push (1‑2s) | ⚠️ Poll (few seconds) | ✅ In‑client |
| **All 10 players** | ❌ Local only | ✅ League matches only | ✅ In‑client |
| **Item builds** | ✅ | ✅ (post‑match) | ✅ |
| **Map state** | ⚠️ Partial | ⚠️ Limited | ✅ |
| **Easy HTTP API** | ✅ | ✅ | ❌ (binary protocol) |
| **No auth required** | ✅ | ❌ API key | ✅ (in client) |
| **Coach‑specific data** | ❌ | ❌ | ✅ (fogged) |

---

## Key Takeaways

1. **For a real‑time overlay / companion app** running alongside the game, **GSI** is the best option — it's local, push‑based, and exposes the local player's full state.

2. **For remote live match tracking** (e.g., a tournament dashboard), **`GetLiveLeagueGames`** (league matches) and **`GetRealtimeStats`** (specific match) are available but limited to Valve‑sanctioned tournaments and league lobbies.

3. **No official "Coach HTTP API"** exists — Dota 2's coaching is entirely in‑client.

4. **Third‑party data platforms** (STRATZ, OpenDota) reverse‑engineer replay files for post‑match analysis and provide richer REST/GraphQL APIs than Valve's official WebAPI.

5. **Protobuf definitions** for the game's live network protocol are community‑maintained at [SteamDatabase/GameTracking-Dota2](https://github.com/SteamDatabase/GameTracking-Dota2) — these could theoretically be used to parse game server packets, but this is complex and requires low‑level network access.
