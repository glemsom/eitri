# Dota 2 Existing Third-Party Tools, Overlays, and Bots

> **Research Date:** 2025-07-21  
> **Status:** Initial research — sources may become stale; verify before making decisions.

---

## 1. Overwolf Apps / In-Game Overlays for Dota 2

### 1.1 STRATZ Overlay (Overwolf)
- **URL:** https://stratz.com/overlay
- **Description:** A popular Overwolf-powered in-game overlay that shows real-time stats including MMR, hero matchups, item builds, ward placements, GPM/XPM graphs, and team fight analysis. Integrates with STRATZ's extensive Dota 2 database.
- **Features:**
  - Real-time win probability graph
  - Neutral camp stacking timers
  - Roshan respawn timer
  - Hero counter-picking suggestions
  - Item and ability build recommendations based on pro matches
- **Status:** Active, regularly updated.

### 1.2 DotaPlus (by Overwolf)
- **URL:** https://www.overwolf.com/app/DotaPlus
- **Description:** An Overwolf app that provides post-match analysis, player statistics, hero builds, and live match tracking. Not to be confused with Valve's official Dota Plus subscription.
- **Features:**
  - Live match stats overlay
  - Post-match performance analysis
  - Hero build suggestions
  - Player profile tracking
- **Status:** Appears to have been delisted or renamed; previously available in Overwolf appstore.

### 1.3 Other Overwolf Dota 2 Apps
- Overwolf historically supported Dota 2 with several apps (match trackers, voice modulators, etc.) but the Dota 2 category on Overwolf has been scaled back. Most active development has shifted to STRATZ.

---

## 2. Popular Dota 2 Assistant Tools

### 2.1 Dotabuff
- **URL:** https://www.dotabuff.com
- **Description:** The most comprehensive Dota 2 statistics and analytics website. Tracks player statistics, match history, hero performance, and item/ability builds.
- **Features:**
  - Dotabuff Plus (paid subscription) — deep analytics, player trends, MMR tracking
  - Hero performance stats, counters, and synergies
  - Match replay analysis
  - Leaderboards and rankings
- **Status:** Active, owned by Curse/Overwolf.

### 2.2 OpenDota
- **URL:** https://www.opendota.com
- **Description:** Free and open-source Dota 2 statistics platform. Provides comprehensive match analysis, player stats, and API access.
- **Features:**
  - Free, community-driven
  - Full match parsing and analysis
  - Player performance benchmarks
  - API for developers
- **Status:** Active, open-source (GitHub: https://github.com/odota).

### 2.3 STRATZ
- **URL:** https://stratz.com
- **Description:** A modern Dota 2 stats platform with real-time data, in-game overlay, and extensive analytics.
- **Features:**
  - Real-time match data via gRPC streaming
  - Hero builds, counters, and trends
  - Pro match tracking
  - In-game overlay (Overwolf)
  - Free tier with optional subscription (STRATZ Pro)
- **Status:** Active.

### 2.4 Dota 2 Official Dota Plus
- **URL:** Via Dota 2 client (Valve)
- **Description:** Valve's official in-game subscription service ($3.99/month or part of Dota 2 Battle Pass).
- **Features:**
  - Hero mastery (leveling, chat wheel, taunts)
  - In-game guides (item/ability builds from high-MMR players)
  - Hero performance metrics during pick phase
  - Post-match breakdowns
  - Relics/shards
- **Status:** Active, official Valve product.

---

## 3. Coach / Analysis Bots and Real-Time Suggestion Tools

### 3.1 Dota Coach Bot (Discord/Telegram)
- Various Discord bots exist that pull match data from OpenDota or STRATZ APIs to provide post-match analysis, MMR tracking, and improvement suggestions.
- Examples: "Dota Coach" on Telegram, "Dota 2 Stats Bot" on Discord.

### 3.2 In-Game Coaching via Dota 2 Client
- Valve's built-in coach mode allows a player to spectate and give real-time advice to a party member.
- No third-party equivalent currently exists that injects live coaching suggestions directly into the game client (Valve's policies restrict this).

### 3.3 GitHub Open-Source Coach/Analysis Bots
- **Repositories found via search:**
  - `odota/parser` — Match parsing engine used by OpenDota
  - `stratz/stratz-api` — STRATZ API and data pipeline
  - Various smaller bots for Discord/Telegram that use these APIs for match analysis
- No known actively maintained "AI coach" bot that provides live in-game suggestions.

---

## 4. AI-Powered Dota 2 Assistants

### 4.1 OpenAI Five
- **Status:** Historical (2017–2019). OpenAI trained a neural network to play Dota 2 at a high level. The project was experimental and is no longer active.
- **Relevance:** Demonstrated that AI can make real-time decisions in Dota 2, but the technology was not released as a consumer assistant tool.

### 4.2 Current AI/ML Applications in Dota 2 Tools
- **Hero Counter-Picking:** Tools like STRATZ and Dotabuff use ML models to suggest hero counters based on win rate data and drafting patterns.
- **Build Recommendations:** ML-based item/ability build suggestions based on match context.
- **Win Probability Models:** Real-time win chance predictions (STRATZ overlay).
- **No known product** provides a LLM-based conversational coaching assistant for Dota 2.

### 4.3 Potential Gap
- There is **no existing AI-powered in-game assistant** that uses modern LLMs (like GPT-4, Claude, etc.) to provide real-time, context-aware coaching or decision support during a Dota 2 match.
- Existing tools are limited to statistics, overlays, and post-match analysis — not interactive, real-time AI coaching.

---

## 5. Summary Table

| Tool / Product        | Type                | Real-Time Overlay | AI-Powered | Live Coaching | Cost        | Status   |
|-----------------------|---------------------|:-----------------:|:----------:|:-------------:|-------------|----------|
| STRATZ Overlay        | Overwolf overlay    | ✅                | Partial¹   | ❌             | Free / Pro  | Active   |
| Dotabuff              | Web analytics       | ❌                | Partial¹   | ❌             | Free / Plus | Active   |
| OpenDota              | Web analytics       | ❌                | ❌          | ❌             | Free        | Active   |
| Dota Plus (Valve)     | In-game subscription| ✅ (in-client)    | ❌          | ❌             | $3.99/mo   | Active   |
| Dota Coach (bot)      | Discord/Telegram    | ❌                | ❌          | ❌             | Free        | Varies   |
| OpenAI Five           | ML research project | ❌                | ✅ (agent)  | ❌             | N/A         | Inactive |
| AI Dota 2 Coach (ours)| *(planned)*         | ❌ (planned)      | ✅ (planned)| ✅ (planned)   | TBD         | Planning |

¹ = Uses ML for hero counter suggestions and win probability, not conversational AI.

---

## 6. Key Takeaways

1. **Overwolf ecosystem** has scaled back Dota 2 support; STRATZ is the dominant remaining Overwolf overlay.
2. **No existing tool** provides real-time, conversational AI coaching during a match.
3. **Gap identified:** An LLM-powered assistant that analyzes game state (from screen capture or API) and gives strategic advice, item build suggestions, and decision support would be **novel**.
4. **APIs available:** STRATZ (gRPC), OpenDota, Valve's official APIs can provide real-time match data.
5. **Valve's terms** restrict direct client interaction, but external overlays and companion apps are allowed.

---

## 7. Source URLs

| Source                | URL                                              |
|-----------------------|--------------------------------------------------|
| STRATZ                | https://stratz.com/overlay                       |
| Dotabuff              | https://www.dotabuff.com                         |
| OpenDota              | https://www.opendota.com                         |
| OpenDota GitHub       | https://github.com/odota                         |
| STRATZ Overwolf App   | https://stratz.com/en/overlay                    |
| Overwolf Appstore     | https://www.overwolf.com/appstore                |
| STRATZ Features       | https://stratz.com/en/features                   |
| STRATZ About          | https://stratz.com/en/about                      |
| OpenAI Five (blog)    | https://openai.com/research/dota-2               |
| Valve Dota Plus       | In-client; https://www.dota2.com/                |

> **Note:** Several source URLs (Overwolf app pages, Reddit threads, Fandom wiki) returned 404 or were blocked by Cloudflare at the time of research. Verify via direct browsing.
