# Generates the serpentine architecture diagram as SVG, plus PNGs.
#
# Authored at 1024 wide because that is the width WordPress serves it at: text sized for 1280 and
# then downscaled lands around 10px and stops being readable. Upload the @2x file — the browser
# shows it at 1024 CSS px and retina screens get the full pixels.
#
# Presentation attributes only (no <style>), so every renderer draws it the same.
import pathlib

W, H = 1024, 948
BG, PANEL, INK, MUTED = "#f6f5f2", "#ffffff", "#1d1d1f", "#5f5f66"
LINE, ACCENT, WARM, EXT = "#dcd9d2", "#0a6c5a", "#a8491a", "#eceae4"
FONT = "'Helvetica Neue', Helvetica, Arial, sans-serif"
out = []

def esc(s):
    return s.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")

def wrap(text, width_px, size, bold=False):
    per = size * (0.56 if bold else 0.515)
    maxc = max(8, int(width_px / per))
    lines, cur = [], ""
    for w in text.split():
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
    out.append(f'<rect x="{x:.0f}" y="{y:.0f}" width="{w:.0f}" height="{h:.0f}" rx="9" '
               f'fill="{fill}" stroke="{stroke}" stroke-width="1.2"{dash}/>')
    tx, ty = x + 14, y + 22
    if kicker:
        out.append(f'<text x="{tx:.0f}" y="{ty:.0f}" font-size="10" font-weight="700" fill="{acc}" '
                   f'letter-spacing="1">{esc(kicker)}</text>')
        ty += 22
    for ln in wrap(title, w - 28, 15.5, True):
        out.append(f'<text x="{tx:.0f}" y="{ty:.0f}" font-size="15.5" font-weight="700" '
                   f'fill="{INK}">{esc(ln)}</text>')
        ty += 20
    for para in body:
        for ln in wrap(para, w - 28, 12):
            out.append(f'<text x="{tx:.0f}" y="{ty:.0f}" font-size="12" fill="{MUTED}">'
                       f'{esc(ln)}</text>')
            ty += 16

def arrow(d, color=ACCENT, dash=None, width=1.9, head=True):
    m = f' marker-end="url(#a-{color[1:]})"' if head else ""
    ds = f' stroke-dasharray="{dash}"' if dash else ""
    out.append(f'<path d="{d}" fill="none" stroke="{color}" stroke-width="{width}"{ds}{m} '
               f'stroke-linejoin="round"/>')

def tag(x, y, text, color=MUTED, size=11, weight="600"):
    w = len(text) * size * 0.54 + 12
    out.append(f'<rect x="{x - w / 2:.0f}" y="{y - size + 1:.0f}" width="{w:.0f}" '
               f'height="{size + 6:.0f}" rx="4" fill="{BG}"/>')
    out.append(f'<text x="{x:.0f}" y="{y:.0f}" font-size="{size}" font-weight="{weight}" '
               f'fill="{color}" text-anchor="middle">{esc(text)}</text>')

out.append(f'<svg xmlns="http://www.w3.org/2000/svg" width="{W}" height="{H}" '
           f'viewBox="0 0 {W} {H}" font-family="{FONT}">')
out.append('<defs>')
for c in (ACCENT, MUTED):
    out.append(f'<marker id="a-{c[1:]}" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6.5" '
               f'markerHeight="6.5" orient="auto-start-reverse">'
               f'<path d="M0,1 L10,5 L0,9 z" fill="{c}"/></marker>')
out.append('</defs>')
out.append(f'<rect width="{W}" height="{H}" fill="{BG}"/>')

out.append(f'<text x="32" y="40" font-size="21" font-weight="700" fill="{INK}">'
           'Serpentine — how a ride gets planned</text>')
out.append(f'<text x="32" y="62" font-size="12.5" fill="{MUTED}">The phone talks to one host and '
           'nothing else. Everything third-party is reached from inside the house.</text>')

# ---- clients -----------------------------------------------------------
CY, CH = 112, 112
box(32, CY, 228, CH, "CLIENT", "iPhone · iPad · Mac",
    ["SwiftUI and MapKit. No SDKs, no account, no analytics."])
box(276, CY, 228, CH, "CLIENT · IN PROGRESS", "Android",
    ["Compose. A second client, not a reimplementation."], "ghost")
box(520, CY, 228, CH, "CLIENT", "Browser",
    ["/v1/ — the same planner, in a page."])
box(764, CY, 228, CH, "ON THE DEVICE", "Apple · Google Maps",
    ["Voice guidance by handoff: one URL carrying the planned stops."], "ghost")
arrow(f"M146,{CY} V92 H878 V{CY - 4}", MUTED, "2 4", 1.5)
tag(512, 88, "waypoint URL · stays on the device")

# ---- the house ---------------------------------------------------------
HY, HB = 276, 760
out.append(f'<rect x="32" y="{HY}" width="960" height="{HB - HY}" rx="14" fill="none" '
           f'stroke="{WARM}" stroke-width="1.5" stroke-dasharray="7 5"/>')
out.append(f'<text x="968" y="{HY + 22}" font-size="10" font-weight="700" fill="{WARM}" '
           f'letter-spacing="1" text-anchor="end">PAUL\'S HOUSE · LAN 204.128.136.0/24</text>')

for x in (146, 390, 634):
    arrow(f"M{x},{CY + CH} V248", ACCENT, None, 1.6, head=False)
out.append(f'<path d="M146,248 H634" stroke="{ACCENT}" stroke-width="1.6" fill="none"/>')
arrow("M202,248 V316", ACCENT, None, 2.2)
out.append(f'<text x="660" y="244" font-size="11.5" font-weight="600" fill="{ACCENT}">'
           'HTTPS · serpentine.phfactor.net</text>')
out.append(f'<text x="660" y="259" font-size="11.5" fill="{MUTED}">'
           'the only host any client contacts</text>')

box(52, 316, 300, 128, "INTERNET-FACING", "Caddy — webserver (Pi 5)",
    ["The only host with ports open: 80 and 443. TLS, then reverse_proxy /v1/* → axiom:8990. "
     "The log keeps half an IPv4 for 30 days, and no routes."])
box(672, 316, 300, 128, "MONITORING", "serpentine-health.timer",
    ["Every 2 min from the Pi, probing the API and the public URL. Three strikes, then ntfy. "
     "On the Pi so the watcher can't share fate with axiom."])
arrow("M668,380 H356", MUTED, "2 4", 1.5)
tag(512, 375, "probes")

# ---- axiom -------------------------------------------------------------
AY, AH = 468, 272
out.append(f'<rect x="52" y="{AY}" width="880" height="{AH}" rx="11" fill="#f1efe9" '
           f'stroke="{LINE}" stroke-width="1.2"/>')
out.append(f'<text x="72" y="{AY + 22}" font-size="10" font-weight="700" fill="{ACCENT}" '
           f'letter-spacing="1">AXIOM · MAC STUDIO M4 MAX, 128 GB (.7)</text>')
arrow(f"M202,444 V{AY - 4}", ACCENT, None, 2.2)
arrow(f"M822,444 V{AY - 4}", MUTED, "2 4", 1.5)

box(72, 508, 840, 76, "", "serpentine-api :8990",
    ["Go, stdlib only. Generates and scores rides, runs the energy model, picks the charge stops, "
     "builds the Maps handoff and the GPX. A plan lives in memory for up to 24 h, then it is "
     "gone — never written to disk, never logged."])
rw = (840 - 32) / 3
for i, (cx, t, b) in enumerate([
    (207, "GraphHopper 11 :8989",
     "Docker. Whole-US graph: 13 GB, 70 M nodes. Curvy and direct profiles, built offline in 63 min."),
    (508, "stations.json",
     "Every US public charger, fetched once a day. Corridor search runs locally, so a ride costs "
     "no third-party call."),
    (809, "stats.db · tile cache",
     "Hourly counters: no routes, no locations, nothing tied to a device. Map tiles cached here."),
]):
    arrow(f"M{cx},584 V612", ACCENT, None, 1.6)
    box(72 + i * (rw + 16), 612, rw, 108, "", t, [b])

# ---- outside -----------------------------------------------------------
OY = 798
for x in (146, 390, 634):
    arrow(f"M{x},{AY + AH} V{OY}", MUTED, "2 4", 1.5)
arrow(f"M960,444 V778 H878 V{OY}", MUTED, "2 4", 1.5)
tag(268, 782, "no rider data")

for i, (t, b) in enumerate([
    ("NREL AFDC", "Charger locations, in bulk, once a day."),
    ("Open Charge Map", "Is this stop alive? Asked per stop."),
    ("OpenStreetMap", "Map tiles, cached on axiom."),
    ("ntfy", "Alerts when the service stops answering."),
]):
    box(32 + i * 244, OY, 228, 92, "OUTSIDE THE HOUSE", t, [b], "ext")

# ---- legend ------------------------------------------------------------
LY = 920
x = 32
for label, c, d, w in [("carries the ride", ACCENT, None, 2.2),
                       ("no rider data attached", MUTED, "2 4", 1.5),
                       ("handoff and monitoring", MUTED, "2 4", 1.5)]:
    arrow(f"M{x},{LY - 4} H{x + 30}", c, d, w)
    out.append(f'<text x="{x + 39}" y="{LY}" font-size="11.5" fill="{MUTED}">{esc(label)}</text>')
    x += 39 + len(label) * 6.1 + 32
out.append(f'<text x="992" y="{LY}" font-size="11.5" fill="{MUTED}" text-anchor="end">'
           'serpentine.phfactor.net</text>')
out.append('</svg>')

p = pathlib.Path(__file__).resolve().parent.parent / "docs/img/architecture.svg"
p.write_text("\n".join(out))
print("wrote", p)
