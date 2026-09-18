# Serpentine routing POC — in-house setup

Runs GraphHopper 11 with a custom motorcycle profile on **axiom** (M4 Max, 128 GB), fronted by the
existing Caddy on **webserver** (Pi 5). Nothing new opens on the UCG-Max.

## 1. On axiom

```sh
mkdir -p /Volumes/2TBSSD/serpentine/data/custom_models
cd /Volumes/2TBSSD/serpentine

# OSM extract (~3.2 GB). Re-download every few months for fresh data.
curl -L -o data/us-west-latest.osm.pbf https://download.geofabrik.de/north-america/us-west-latest.osm.pbf

# Config + profile from this repo
cp ~/code/claude-cowork/serpentine/infra/docker-compose.yml .
cp ~/code/claude-cowork/serpentine/infra/graphhopper/config.yml data/
cp ~/code/claude-cowork/serpentine/infra/graphhopper/custom_models/serpentine.json data/custom_models/

# Docker Desktop > Settings > Resources > Memory >= 24 GB (default 7.57 GB OOM-kills the import), then:
docker compose up -d
docker compose logs -f     # first run: import + urban density + LM prep, expect 30-60 min
```

Import is done when the log shows `Started server` / `Started application@...`. After that
`docker compose restart` comes back in seconds (graph cache is on the SSD).

If you'd rather not run Docker Desktop on a headless box: `brew install openjdk@21`, download
`graphhopper-web-11.0.jar` from the GitHub release, and run
`java -Xmx14g -jar graphhopper-web-11.0.jar server data/config.yml` from the same directory.
Same config, no VM memory ceiling to fight.

## 2. Smoke tests (from anywhere on the LAN)

```sh
# alive?
curl -s http://axiom:8989/health
curl -s http://axiom:8989/info | jq '.profiles, .encoded_values | keys'

# A->B, curvy: Julian to Ramona
curl -s -X POST http://axiom:8989/route -H 'Content-Type: application/json' -d '{
  "profile": "motorcycle",
  "points": [[-116.6019, 33.0786], [-116.8681, 33.0417]],
  "points_encoded": false,
  "instructions": true,
  "details": ["road_class", "max_speed", "curvature", "urban_density", "surface"]
}' | jq '.paths[0] | {distance, time, instructions: (.instructions | length)}'

# 150 km loop from home, heading east, seeded so it's reproducible
curl -s -X POST http://axiom:8989/route -H 'Content-Type: application/json' -d '{
  "profile": "motorcycle",
  "points": [[-117.16, 32.72]],
  "algorithm": "round_trip",
  "round_trip.distance": 150000,
  "round_trip.seed": 7,
  "heading": [90],
  "points_encoded": false
}' | jq '.paths[0] | {distance, time}'

# Same, but tighten the model per-request (LM mode allows multiply_by <= 1 only)
curl -s -X POST http://axiom:8989/route -H 'Content-Type: application/json' -d '{
  "profile": "motorcycle",
  "points": [[-116.6019, 33.0786], [-116.8681, 33.0417]],
  "custom_model": { "priority": [ { "if": "curvature >= 0.96", "multiply_by": "0.2" } ] }
}' | jq '.paths[0].distance'
```

Visual tuning: open `http://axiom:8989/maps/` in a browser, pick the `motorcycle` profile, expand
**Custom Model**, paste edits, drag points around. Iterate there, then copy the winner back into
`serpentine.json` and `docker compose restart` (custom model files are read at startup; no re-import
needed unless you add encoded values).

## 3. Expose it (Pi 5 / Caddy)

See `caddy/serpentine.caddyfile`. Add the DNS record, paste the block, `sudo systemctl reload caddy`.
Then from the phone: `https://serpentine.phfactor.net/health`.

## 4. What's deliberately not here yet

- Charger stops (NREL `nearby-route` on the returned polyline) — client side, next step.
- Out-and-back with a different return road — needs an `avoid_edges`-style penalty on the outbound
  path; GraphHopper OSS doesn't expose that directly, so it's either two routes with a per-request
  custom model penalising the first leg's area, or a small server-side patch.
- Real traffic. `urban_density` is the stand-in.
- Apple Maps handoff URL builder — trivial, belongs in the app.

## Gotchas (each cost real time)

- **Docker Desktop VM memory.** Default cap was 7.57 GB; the container sat at 92 % and would have
  been OOM-killed mid-import. Settings > Resources > Memory ≥ 24 GB, Apply & Restart, then
  `rm -rf data/graph-cache` before re-running — a partial import is not trustworthy.
- **`motorcycle.json` is a reserved name.** GraphHopper ships a built-in custom model with that
  name and exits with `Custom model file name 'motorcycle.json' is already used for built-in
  profiles`. Ours is `serpentine.json`; the API profile is still called `motorcycle`.
- **Corrupt SRTM tiles after an interrupted run.** Symptom: `Could not parse OSM file` caused by
  `Unexpected end of ZLIB input stream` on `/data/elevation/demNNNNNN`. GraphHopper caches tiles
  from `srtm.kurviger.de` without checksumming, so a killed download poisons every later start.
  `rm -rf data/elevation data/graph-cache` and restart. If a *different* tile fails next time,
  delete just that file.
- **Timing reference (M4 Max, 16 vCPU to Docker):** pass1 over 43 M ways in 51 s; ~200 SRTM tiles
  download during pass2; whole import + urban density + LM prep should be under an hour.

## Memory budget on axiom

`-Xmx14g` is generous for US-West; expect ~8-10 GB resident once serving. With gpt-oss-120b loaded at
128k context (~71 GB) there's still ~40 GB headroom. Lower to `-Xmx10g` if you want more room for models.
