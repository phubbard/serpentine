# Generates the serpentine architecture diagram as SVG.
# Presentation attributes only (no <style>), so any renderer draws it the same.
import pathlib

W, H = 1280, 1036
BG, PANEL, INK, MUTED = "#f6f5f2", "#ffffff", "#1d1d1f", "#5f5f66"
LINE, ACCENT, WARM, EXT = "#dcd9d2", "#0a6c5a", "#a8491a", "#eceae4"
FONT = "'Helvetica Neue', Helvetica, Arial, sans-serif"
out = []

def esc(s):
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")

def wrap(text, width_px, size, bold=False):
    per = size * (0.56 if bold else 0.515)
    maxc = max(8, int(width_px / per))
    words, lines, cur = text.split(), [], ""
    for w in words:
        t = (cur + " " + w).strip()
        if len(t) <= maxc:
            cur = t
        else:
            lines.append(cur); cur = w
    if cur:
        lines.append(cur)
    return lines

def box(x, y, w, h, kicker, title, body, kind="core"):
    fill, stroke, dash, acc = PANEL, LINE, "", ACCENT
    if kind == "ext":
        fill, stroke, acc = EXT, "#d6d3cb", MUTED
    if kind == "ghost":
        dash, acc = ' stroke-dasharray="5 4"', MUTED
    out.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="10" '
               f'fill="{fill}" stroke="{stroke}" stroke-width="1.2"{dash}/>')
    tx, ty = x + 16, y + 28
    if kicker:
        out.append(f'<text x="{tx}" y="{ty}" font-size="10.5" font-weight="700" fill="{acc}" '
                   f'letter-spacing="1.1">{esc(kicker)}</text>')
        ty += 21
    for i, ln in enumerate(wrap(title, w - 32, 16.5, True)):
        out.append(f'<text x="{tx}" y="{ty}" font-size="16.5" font-weight="700" '
                   f'fill="{INK}">{esc(ln)}</text>')
        ty += 20
    ty += 2
    for para in body:
        for ln in wrap(para, w - 32, 12.5):
            out.append(f'<text x="{tx}" y="{ty}" font-size="12.5" fill="{MUTED}">{esc(ln)}</text>')
            ty += 16
    return ty

def arrow(d, color=ACCENT, dash=None, width=2.0, head="end"):
    m = f' marker-end="url(#a-{color[1:]})"' if head else ""
    ds = f' stroke-dasharray="{dash}"' if dash else ""
    out.append(f'<path d="{d}" fill="none" stroke="{color}" stroke-width="{width}"'
               f'{ds}{m} stroke-linejoin="round"/>')

def tag(x, y, text, color=MUTED, size=11.5, weight="600", anchor="middle"):
    w = len(text) * size * 0.54 + 14
    bx = x - w / 2 if anchor == "middle" else x - 7
    out.append(f'<rect x="{bx:.0f}" y="{y - size + 1:.0f}" width="{w:.0f}" height="{size + 7:.0f}" '
               f'rx="4" fill="{BG}"/>')
    out.append(f'<text x="{x}" y="{y}" font-size="{size}" font-weight="{weight}" fill="{color}" '
               f'text-anchor="{anchor}">{esc(text)}</text>')

out.append(f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" '
           f'viewBox="0 0 {W} {H}" font-family="{FONT}">')
out.append('<defs>')
for c in (ACCENT, MUTED):
    out.append(f'<marker id="a-{c[1:]}" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6.5" '
               f'markerHeight="6.5" orient="auto-start-reverse">'
               f'<path d="M0,1 L10,5 L0,9 z" fill="{c}"/></marker>')
out.append('</defs>')
out.append(f'<rect width="{W}" height="{H}" fill="{BG}"/>')

# ---- title -------------------------------------------------------------
out.append(f'<text x="40" y="44" font-size="25" font-weight="700" fill="{INK}">'
           'Serpentine — how a ride gets planned</text>')
out.append(f'<text x="40" y="70" font-size="14" fill="{MUTED}">The phone talks to one host and '
           'nothing else. Every third-party service is reached from inside the house, never from '
           'the device.</text>')

# ---- clients -----------------------------------------------------------
CY, CH = 122, 106
box(40, CY, 300, CH, "CLIENT", "iPhone · iPad · Mac",
    ["SwiftUI · MapKit · CoreLocation. No SPM packages, no analytics, no account, "
     "no third-party SDKs."])
box(364, CY, 256, CH, "CLIENT · IN PROGRESS", "Android",
    ["Kotlin · Compose. Same API, second client — the hard parts are all server-side."], "ghost")
box(644, CY, 240, CH, "CLIENT", "Browser test page",
    ["/v1/ — the same planner, vendored Leaflet, tiles from our own cache."])
box(916, CY, 324, CH, "ON THE DEVICE", "Apple Maps · Google Maps",
    ["Voice guidance by handoff: one URL carrying the planned stops. The platform's "
     "own navigation does the talking; there is no turn-by-turn in the app."], "ghost")
arrow(f"M190,{CY} V100 H1078 V{CY - 4}", MUTED, "2 4", 1.6)
tag(634, 96, "waypoint URL · stays on the device")

# ---- the house ---------------------------------------------------------
HY, HB = 292, 826
out.append(f'<rect x="40" y="{HY}" width="1200" height="{HB - HY}" rx="16" fill="none" '
           f'stroke="{WARM}" stroke-width="1.6" stroke-dasharray="7 5"/>')
out.append(f'<text x="1216" y="{HY + 26}" font-size="10.5" font-weight="700" fill="{WARM}" '
           f'letter-spacing="1.1" text-anchor="end">PAUL\'S HOUSE · LAN 204.128.136.0/24</text>')

# clients -> Caddy
for x in (190, 492, 764):
    arrow(f"M{x},{CY + CH} V262", ACCENT, None, 1.8, head=None)
out.append(f'<path d="M190,262 H764" stroke="{ACCENT}" stroke-width="1.8" fill="none"/>')
arrow("M210,262 V336", ACCENT, None, 2.4)
out.append(f'<text x="800" y="266" font-size="12" font-weight="600" fill="{ACCENT}">'
           'HTTPS · serpentine.phfactor.net — the only host any client contacts</text>')

CDY = 336
box(64, CDY, 292, 128, "INTERNET-FACING", "Caddy — webserver (Pi 5)",
    ["The only host with ports open: 80 and 443. TLS, then reverse_proxy /v1/* → axiom:8990. "
     "Log keeps half an IPv4 for 30 days, no routes."])
box(880, CDY, 336, 128, "MONITORING", "serpentine-health.timer",
    ["Every 2 min from the Pi, probing the public URL and the API. Three strikes, then ntfy. "
     "On the Pi so the watcher can't share fate with what it watches."])
arrow(f"M876,{CDY + 64} H360", MUTED, "2 4", 1.6)
tag(618, CDY + 60, "probes")

# ---- axiom -------------------------------------------------------------
AY = 508
out.append(f'<rect x="64" y="{AY}" width="1104" height="302" rx="12" fill="#f1efe9" '
           f'stroke="{LINE}" stroke-width="1.2"/>')
out.append(f'<text x="88" y="{AY + 26}" font-size="10.5" font-weight="700" fill="{ACCENT}" '
           f'letter-spacing="1.1">AXIOM · MAC STUDIO M4 MAX, 128 GB (.7)</text>')
arrow(f"M210,464 V{AY + 42}", ACCENT, None, 2.4)
arrow(f"M1048,464 V{AY - 4}", MUTED, "2 4", 1.6)

API_Y = AY + 42
box(88, API_Y, 1056, 86, "", "serpentine-api :8990",
    ["Go, stdlib only. Generates and scores loop candidates, out-and-backs and A→B rides, "
     "runs the energy model and picks charge stops, builds the handoff URL and the GPX. "
     "A plan lives in memory for up to 24 h, then it is gone — never written to disk."])
RY = API_Y + 116
for x in (257, 616, 974):
    arrow(f"M{x},{API_Y + 86} V{RY}", ACCENT, None, 1.8)
rw = (1056 - 2 * 20) / 3
for i, (t, b) in enumerate([
    ("GraphHopper 11 :8989",
     "Docker, whole-US graph: 13 GB, 70 M nodes. Two profiles — motorcycle (curvy) and direct "
     "(the errand ride). Built offline in 63 min from a Geofabrik extract and SRTM tiles."),
    ("stations.json",
     "Every public US charger, fetched once a day from the Department of Energy. The corridor "
     "search then runs locally, so planning a ride costs no third-party call."),
    ("stats.db · tile cache",
     "Hourly counters only: no routes, no locations, nothing tied to a device. OSM map tiles are "
     "fetched and cached here, so the rider never touches OpenStreetMap."),
]):
    box(88 + i * (rw + 20), RY, rw, 120, "", t, [b])

# ---- outside -----------------------------------------------------------
OY = 866
AB = AY + 302
for x in (200, 493, 786):
    arrow(f"M{x},{AB} V{OY}", MUTED, "2 4", 1.6)
arrow(f"M1196,464 V846 H1079 V{OY}", MUTED, "2 4", 1.6)
tag(346, 852, "no rider data: route shape, charger coordinates")

ow = (1152 - 3 * 20) / 4
for i, (t, b) in enumerate([
    ("NREL AFDC", "Charger locations, bulk, once a day. Key lives on axiom."),
    ("Open Charge Map", "Is this stop alive? Asked per charge stop, not per ride."),
    ("OpenStreetMap", "Map tiles for the test page, cached on axiom."),
    ("ntfy", "Alerts to Paul's phone when the service stops answering."),
]):
    box(64 + i * (ow + 20), OY, ow, 96, "OUTSIDE THE HOUSE", t, [b], "ext")

# ---- legend ------------------------------------------------------------
LY = 1004
items = [("carries the ride", ACCENT, None, 2.4),
         ("no rider data attached", MUTED, "2 4", 1.6),
         ("handoff and monitoring", MUTED, "2 4", 1.6)]
x = 40
for label, c, d, w in items:
    arrow(f"M{x},{LY - 4} H{x + 34}", c, d, w)
    out.append(f'<text x="{x + 44}" y="{LY}" font-size="12" fill="{MUTED}">{esc(label)}</text>')
    x += 44 + len(label) * 6.4 + 36
out.append(f'<text x="1240" y="{LY}" font-size="12" fill="{MUTED}" text-anchor="end">'
           'serpentine.phfactor.net</text>')
out.append('</svg>')

p = pathlib.Path("/Users/pfh/code/claude-cowork/serpentine/docs/img/architecture.svg")
p.write_text("\n".join(out))
print("wrote", p)
