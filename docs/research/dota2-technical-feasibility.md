# Dota 2 AI Agent — Live Assistance Technical Feasibility

> **Research Date:** 2025-07-21  
> **Status:** Complete  
> **Scope:** Investigate whether an AI agent can provide live, real-time assistance to a player during a Dota 2 match using official APIs and community-tested approaches.

---

## 1. How Game State Integration (GSI) Works Technically

### 1.1 Overview

Dota 2 provides a first-party feature called **Game State Integration (GSI)** that allows external applications to receive real-time game data over HTTP. GSI was introduced in 2015 alongside Counter-Strike: Global Offensive and later extended to Dota 2.

### 1.2 Configuration

The player places a `.cfg` file in the Dota 2 install directory:

```
<Dota 2 install>/game/dota/cfg/gamestate_integration/
```

A typical config file (`gamestate_integration_myapp.cfg`) looks like:

```json
"myapp_config"
{
    "uri"               "http://localhost:3000/"
    "timeout"           "5.0"
    "buffer"            "0.1"
    "throttle"          "0.5"
    "heartbeat"         "30.0"
    "data"
    {
        "provider"      "1"
        "map"           "1"
        "player"        "1"
        "hero"          "1"
        "abilities"     "1"
        "items"         "1"
        "buildings"     "1"
        "wearables"     "1"
    }
}
```

Key parameters:
- **uri**: The local HTTP server endpoint Dota 2 sends JSON POST requests to.
- **timeout**: How long to wait for a response from the server before dropping the request.
- **buffer**: Minimum time between forwarded events (avoids flooding).
- **throttle**: Minimum interval between consecutive HTTP requests.
- **heartbeat**: How often to send a full state snapshot even if nothing changed.

### 1.3 Data Flow

```
┌─────────────┐     HTTP POST (JSON)     ┌──────────────┐
│  Dota 2     │ ────────────────────────▶ │  Local HTTP  │
│  Game       │                          │  Server      │
│  Client     │ ◀──── 200 OK (empty)     │  (your app)  │
└─────────────┘                          └──────────────┘
```

1. Dota 2 sends JSON payloads as POST requests to your local HTTP server.
2. Your server receives the payload, processes it, and returns HTTP 200.
3. The response body is ignored by Dota 2.
4. The server can then feed the data to an AI agent for analysis.

### 1.4 Payload Structure

The JSON payload contains these top-level sections:

| Section     | Description                                          |
|-------------|------------------------------------------------------|
| `provider`  | Game version, Steam ID, timestamp                    |
| `map`       | Map name, game time, match ID, clock time, ward lists |
| `player`    | Steam ID, player name, activity, kills/deaths/assists |
| `hero`      | Hero name, level, XP, ability points, health/mana    |
| `abilities` | Ability names, levels, cooldowns, passives           |
| `items`     | Item names, cooldowns, charges (0-5 slots + stash)   |
| `buildings` | Tower/barracks/ancient status per team               |
| `wearables` | Equipped cosmetic item IDs (not useful for gameplay) |

**Notable limitations of GSI data:**
- **No minimap data** — enemy hero positions are **not** included (this data is intentionally withheld by Valve to prevent cheating).
- **No visible creep/neutral camp information** — GSI only exposes what the local client legally knows.
- **No rune spawn timers** directly (but can be inferred from game clock).
- **No Roshan timer** directly (but can be inferred from Aegis expiry / game clock).
- Ability cooldowns and item cooldowns are accurate and useful.
- Gold and last hits are provided via the player section.

### 1.5 Existing Implementations

- **node-dota2-gsi** — Node.js library for receiving GSI payloads.
- **dota2-gsi** — Python library (PyPI: `dota2-gsi`).
- **OPSKIT** by OpenDota — Uses GSI for live draft/analysis overlays.
- **STRATZ** — Uses GSI combined with parsed replay data for live web overlays.

---

## 2. Latency Constraints

### 2.1 GSI Default Throttle

The default `throttle` value is **0.5 seconds** (500 ms). This means Dota 2 will send a POST request at most once every 500ms. The minimum possible throttle is **~0.1s** (100ms) in practice, but this can be CPU-intensive.

### 2.2 End-to-End Latency Budget

```
[Game Event] → [Dota 2 polls state] → [HTTP POST] → [Local Server] → [AI Inference] → [Output to User]
```

| Stage                     | Estimated Latency |
|---------------------------|-------------------|
| GSI throttle interval     | 100–500 ms        |
| HTTP POST (localhost)     | <1 ms             |
| Server processing         | <5 ms             |
| AI model inference        | 100–2000 ms (varies wildly by model) |
| Output delivery           | 10–100 ms         |
| **Total (best case)**     | **~200 ms**       |
| **Total (with LLM)**      | **~1–5 seconds**  |

### 2.3 Acceptable Latency by Use Case

| Use Case                            | Max Acceptable Latency |
|-------------------------------------|------------------------|
| Item build suggestions              | 1–5 seconds            |
| Skill build recommendations         | 1–5 seconds            |
| Last-hit timing assistance          | <100 ms (not feasible via GSI alone) |
| Enemy position warnings (warding)   | 500–2000 ms            |
| Roshan timer tracking               | 1–10 seconds           |
| Teamfight analysis (post-fight)     | 2–10 seconds           |
| Real-time laning advice             | 500–2000 ms            |

**Key insight:** GSI cannot support sub-100ms use cases (like last-hit timing or reaction-based dodging) because the throttle is too coarse. However, most strategic advice (builds, rotations, map awareness) operates on 1–10 second timescales, which GSI can serve.

---

## 3. What Data Would Be Useful

### 3.1 Available via GSI

| Data Point                         | Usefulness for AI Agent                     |
|------------------------------------|---------------------------------------------|
| Hero name & level                  | Core context for suggestions                |
| Health/Mana                        | Assess danger, recommend retreat/engage     |
| Ability cooldowns                  | Suggest when to use spells                  |
| Item builds & cooldowns            | Recommend item purchases, active usage      |
| Gold & last hits                   | Track farming efficiency, suggest items     |
| K/D/A                              | Context for game state analysis             |
| Game time                          | Time-based objectives, timers               |
| Tower/barrack/ancient status       | Macro strategy, push/defend decisions       |
| Player activity (menu/in game)     | Detect in-game vs. paused/spectating        |
| Ward timer (from game clock + cooldowns) | Approximate ward expiration           |

### 3.2 NOT Available via GSI (but valuable)

| Data Point                          | Why Missing                  | Workaround                           |
|-------------------------------------|------------------------------|--------------------------------------|
| Enemy hero positions                | Anti-cheat protection        | Use ward/ping awareness inferred from game state |
| Enemy hero levels/inventories       | Anti-cheat protection        | Player knowledge + game-time estimates |
| Minimap data                        | Not exposed                  | N/A                                  |
| Creep equilibrium / lane position   | Not exposed                  | Infer from game time + hero position |
| Rune spawns                        | Not exposed                  | Infer from game clock (every 2 min after 0:00) |
| Roshan status / timer              | Not exposed                  | Track Aegis pickup (if visible on observer ward) |
| Hero respawn timers                | Not directly exposed         | Infer from hero level + game time |

### 3.3 Derived / Inferred Data

An AI agent can compute useful information from raw GSI data:

- **Net worth difference** — track your gold + item value vs. estimated enemy values.
- **Power spike timing** — when key item/level thresholds are reached.
- **Lane equilibrium** — based on game time + hero position + creep wave timing.
- **Rotation suggestions** — based on missing enemy information (if you see enemies on minimap via wards, the player can relay this, but GSI alone won't give it).
- **Item timing benchmarks** — compare current gold/minute to optimal benchmarks for the hero.

---

## 4. Output Delivery Methods

### 4.1 Overlay (Most Common)

| Method                       | Pros                                      | Cons                                    |
|------------------------------|-------------------------------------------|-----------------------------------------|
| **Browser overlay** (OBS)    | Easy to implement, web-based rendering    | Requires OBS or GameOverlay, not native |
| **Windows overlay** (DirectX) | Native feel, no external dependencies     | Complex to implement per-renderer       |
| **Discord Rich Presence**    | Zero visual clutter, easy to set up       | Limited information display             |

**Existing examples:**
- **Overwolf** — provides a native in-game overlay framework (used by DotaPlus, OpenDota, STRATZ). Overwolf has SDK and is approved by Valve for Dota 2.
- **OPSKIT** by OpenDota — open-source overlay using GSI + React.
- **STRATZ Overlay** — commercial overlay for drafting and live stats.

### 4.2 Audio

| Method                      | Pros                                        | Cons                                    |
|-----------------------------|---------------------------------------------|-----------------------------------------|
| **Text-to-speech**          | No visual distraction, works with any setup | Can be annoying, latency adds up         |
| **Pre-recorded voice lines** | Fast, pleasant, can be quick (~200ms)       | Limited to pre-defined scenarios        |
| **Sonification** (audio cues)| Subtle, informative                        | Hard to design well, learning curve     |

### 4.3 Companion App (Phone / Second Screen)

| Method                      | Pros                                        | Cons                                    |
|-----------------------------|---------------------------------------------|-----------------------------------------|
| **Phone push notifications**| Doesn't interfere with gameplay              | Requires looking away, delayed          |
| **Web dashboard**           | Rich data display, no install                | Must alt-tab or use second monitor      |
| **Smartwatch notifications**| Quick glanceable info                        | Tiny screen, limited interaction        |

### 4.4 In-Game Chat Wheel / Console

| Method                      | Pros                                        | Cons                                    |
|-----------------------------|---------------------------------------------|-----------------------------------------|
| **Chat wheel pings**        | Feels natural, uses existing UI              | Limited to predefined actions           |
| **Console commands**        | Can trigger any action command               | Requires developer mode                 |
| **Auto-ping on minimap**    | Direct visual cue                            | Could be classified as automation       |

### 4.5 Recommended Approach

A **layered hybrid** approach:

1. **Primary:** Browser-based overlay (via Overwolf or OBS) — best for rich visual info like item builds, skill builds, cooldown tracking.
2. **Secondary:** Audio cues (TTS) for time-critical information (e.g., "Roshan is up", "Your ultimate is ready").
3. **Tertiary:** Companion phone app for match history, post-game analysis, and pre-game drafts.

---

## 5. Valve's Rules / ToS About Automated Assistance

### 5.1 Dota 2 Subscriber Agreement (Steam Subscriber Agreement)

The key relevant sections are:

**Section 5 — Conduct and Cheating:**
> *"You may not use cheats, automation software (bots), hacks, mods or any other unauthorized third-party software designed to modify the Steam Client or Steam Game experience."*

**Section 5 — Restrictions:**
> *"You may not use any unauthorized third-party software that intercepts, collects, reads, or otherwise harvests information from or through the Steam Client or Steam Game."*

**Important nuance:** GSI is an **official, documented, approved API** provided by Valve. Using it is explicitly permitted. The Dota 2 integration page (dota2.com/integration) and the GSI configuration files shipped with the game are first-party features.

### 5.2 What is Allowed vs. Not Allowed

| Activity                                      | Status |
|-----------------------------------------------|--------|
| Reading GSI data from a local server          | ✅ Explicitly allowed (official API) |
| Displaying an overlay with your own game data | ✅ Likely allowed (Overwolf does it) |
| Providing item/skill build advice from GSI data | ✅ Likely allowed (static analysis) |
| **Automating actions** (auto-casting spells, auto-moving) | ❌ **Bannable** |
| **Intercepting network traffic** (reading packets not via GSI) | ❌ **Bannable** |
| **Reading opponent data not visible to your client** (e.g., parsing replay in real-time) | ❌ **Potentially bannable** |
| **Using memory reading** to get data GSI doesn't expose | ❌ **Bannable** (VAC) |
| **Streaming overlay to viewers** (not yourself) | ✅ Allowed (popular in esports) |

### 5.3 VAC (Valve Anti-Cheat)

- VAC bans are **permanent** and apply to the entire Steam account.
- Tools that only use GSI (official API) and provide no competitive advantage beyond what the player could do themselves are **not VAC-bannable**.
- Overlay tools like Overwolf and DotaPlus have operated for years without VAC issues.
- The line is crossed when the tool provides **information not available to the player through normal gameplay** (e.g., enemy cooldowns you haven't seen, enemy positions you don't have vision of).

### 5.4 Precedent: Existing Tools

| Tool          | Approach           | Status              |
|---------------|--------------------|---------------------|
| Overwolf      | GSI + overlay      | Approved by Valve   |
| DotaPlus      | GSI + overlay      | Widely used         |
| STRATZ Overlay| GSI + web          | Publicly available  |
| OpenDota      | GSI + web dashboard| Open source         |

None of these tools have received VAC bans or Valve enforcement actions, providing strong precedent that GSI-based assistance is permissible.

---

## 6. Conclusions & Recommendations

### 6.1 Feasibility Assessment

| Aspect                     | Verdict               | Details |
|----------------------------|-----------------------|---------|
| **Technical feasibility**  | ✅ **High**           | GSI provides rich data; standard HTTP server is trivial to implement. |
| **Low-latency use cases**  | ⚠️ **Limited**        | GSI throttle (100–500ms) prevents sub-100ms reaction assistance. |
| **Strategic advice**       | ✅ **High**           | Item builds, skill builds, game-state analysis are well within capacity. |
| **Legal/ToS compliance**   | ✅ **Favorable**      | GSI is an official API; overlays are well-established. |
| **Output delivery**        | ✅ **High**           | Multiple proven channels (Overwolf, OBS, web, audio). |
| **Overall viability**      | ✅ **Feasible**       | An AI agent providing live strategic assistance is technically and legally viable within GSI's constraints. |

### 6.2 Key Constraints to Design Around

1. **No enemy position data from GSI.** The AI cannot "see" the minimap. It must infer or rely on player-observed information.
2. **GSI throttle limits reaction speed.** The agent must operate at strategic timescales (seconds), not reflex timescales (milliseconds).
3. **Valve bans automation.** The agent must advise, not execute.
4. **LLM inference latency.** If using a large language model, inference time (1–5s) must be factored into the user experience.

### 6.3 Recommended Architecture

```
┌──────────────┐    HTTP POST (GSI JSON)     ┌───────────────────┐
│  Dota 2 Client│ ───────────────────────────▶ │  GSI Receiver     │
│              │                              │  (local HTTP)     │
└──────────────┘                              └────────┬──────────┘
                                                        │
                                                        ▼
                                              ┌───────────────────┐
                                              │  Game State Parser │
                                              │  → Derives context │
                                              │  → Computes stats  │
                                              └────────┬──────────┘
                                                        │
                                                        ▼
                                              ┌───────────────────┐
                                              │  AI Agent / LLM   │
                                              │  → Analyzes state  │
                                              │  → Generates advice│
                                              └────────┬──────────┘
                                                        │
                                              ┌─────────┴──────────┐
                                              ▼                    ▼
                                     ┌──────────────┐    ┌──────────────┐
                                     │  Overlay UI  │    │  Audio/TTS   │
                                     │  (Overwolf)  │    │  (optional)  │
                                     └──────────────┘    └──────────────┘
```

---

## 7. Source URLs

| Source | URL |
|--------|-----|
| Valve's official GSI integration page | https://www.dota2.com/integration |
| Steam Subscriber Agreement (official) | https://www.dota2.com/subscriberagreement |
| Steam Subscriber Agreement (legal) | https://store.steampowered.com/subscriber_agreement/ |
| GameTracking-Dota2 (GSI sample configs) | https://github.com/SteamTracking/GameTracking-Dota2/tree/master/game/dota/cfg/gamestate_integration |
| Dota 2 Workshop Tools documentation | https://developer.valvesoftware.com/wiki/Dota_2_Workshop_Tools |
| ModDota community GSI guide | https://moddota.com/ |
| Dota 2 WebAPI (GetLiveLeagueGames) | https://wiki.teamfortress.com/wiki/WebAPI/GetLiveLeagueGames |
| node-dota2-gsi (Node.js GSI library) | https://www.npmjs.com/package/dota2-gsi |
| Python dota2-gsi library | https://pypi.org/project/dota2-gsi/ |
| Overwolf Dota 2 integration | https://www.overwolf.com/game/dota-2/ |
| DotaPlus (GSI-based companion) | https://dotaplus.gg/ |
| STRATZ (GSI-based overlay) | https://stratz.com/ |
| OpenDota (open-source stats) | https://www.opendota.com/ |

---

*This research was compiled from publicly available documentation, community resources, and analysis of Valve's policies. It does not constitute legal advice. Consult a legal professional for definitive guidance on ToS compliance.*
