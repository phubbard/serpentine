#!/usr/bin/env python3
"""Route a hardwired demo ride through GraphHopper and print the handoff URLs.

Usage:
    tools/demo_route.py [demo/utc-ramona-sunrise.json] [--gh http://axiom:8989] [--out demo/out]

Prints distance/time and the roads used, writes <name>.gpx and <name>.geojson to --out,
and prints Apple Maps and Google Maps URLs built from the demo's handoff waypoints.
Stdlib only; talks to GraphHopper directly (serpentine-api will own this in phase 1).
"""
import argparse
import json
from math import asin, cos, radians, sin, sqrt
import os
import sys
import urllib.parse
import urllib.request
from xml.sax.saxutils import escape

DEFAULT_DEMO = os.path.join(os.path.dirname(__file__), "..", "demo", "utc-ramona-sunrise.json")


def route(gh, demo):
    body = {
        "profile": demo["request"]["profile"],
        "points": [[p["lon"], p["lat"]] for p in demo["request"]["route_points"]],
        "points_encoded": False,
        "elevation": True,
        "instructions": True,
        "details": ["road_class", "curvature", "urban_density", "max_speed", "surface"],
    }
    req = urllib.request.Request(gh.rstrip("/") + "/route", data=json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            return json.load(r)
    except urllib.error.HTTPError as e:
        sys.exit(f"GraphHopper {e.code}: {e.read().decode()[:400]}")


def latlon(p):
    return f"{p['lat']:.5f},{p['lon']:.5f}"


def apple_maps_url(start, waypoints, end):
    # iOS 18.4+ unified directions URL; repeated waypoint= params, lat,lon order.
    q = [("source", latlon(start))] + [("waypoint", latlon(w)) for w in waypoints]
    q += [("destination", latlon(end)), ("mode", "driving"), ("avoid", "tolls,highways")]
    return "https://maps.apple.com/directions?" + urllib.parse.urlencode(q, safe=",")


def google_maps_url(start, waypoints, end):
    q = {"api": "1", "origin": latlon(start), "destination": latlon(end),
         "waypoints": "|".join(latlon(w) for w in waypoints), "travelmode": "driving"}
    return "https://www.google.com/maps/dir/?" + urllib.parse.urlencode(q, safe=",|")


def gpx(name, title, coords):
    pts = "\n".join(f'      <trkpt lat="{c[1]:.6f}" lon="{c[0]:.6f}">'
                    + (f"<ele>{c[2]:.0f}</ele>" if len(c) > 2 else "") + "</trkpt>"
                    for c in coords)
    return f"""<?xml version="1.0" encoding="UTF-8"?>
<gpx version="1.1" creator="serpentine" xmlns="http://www.topografix.com/GPX/1/1">
  <trk>
    <name>{escape(title)}</name>
    <trkseg>
{pts}
    </trkseg>
  </trk>
</gpx>
"""


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("demo", nargs="?", default=DEFAULT_DEMO)
    ap.add_argument("--gh", default=os.environ.get("SERPENTINE_GH", "http://axiom:8989"))
    ap.add_argument("--out", default=os.path.join(os.path.dirname(__file__), "..", "demo", "out"))
    args = ap.parse_args()

    with open(args.demo) as f:
        demo = json.load(f)
    res = route(args.gh, demo)
    path = res["paths"][0]
    coords = path["points"]["coordinates"]

    print(f"{demo['title']}")
    print(f"  {path['distance'] / 1000:.0f} km, {path['time'] / 3_600_000:.1f} h, "
          f"ascend {path.get('ascend', 0):.0f} m, GraphHopper took {res['info']['took']} ms\n")

    # Roads in order, merging consecutive instructions on the same road.
    legs = []
    for ins in path["instructions"]:
        road = ins.get("street_ref") or ins.get("street_name") or ""
        if ins.get("street_ref") and ins.get("street_name"):
            road = f"{ins['street_name']} ({ins['street_ref']})"
        if legs and legs[-1][0] == road:
            legs[-1][1] += ins["distance"]
        else:
            legs.append([road, ins["distance"]])
    for road, dist in legs:
        if dist >= 1500:
            print(f"  {dist / 1000:5.1f} km  {road or '(unnamed)'}")

    # Distance by road class, computed from coordinate intervals.
    def seg_km(a, b):
        d = 0.0
        for (lon1, lat1, *_), (lon2, lat2, *_) in zip(coords[a:b], coords[a + 1:b + 1]):
            h = sin(radians(lat2 - lat1) / 2) ** 2 + cos(radians(lat1)) * cos(radians(lat2)) * sin(radians(lon2 - lon1) / 2) ** 2
            d += 12742 * asin(sqrt(h))
        return d
    for key in ("road_class", "urban_density", "surface"):
        totals = {}
        for a, b, v in path["details"].get(key, []):
            totals[v] = totals.get(v, 0) + seg_km(a, b)
        print(f"\n  {key}: " + ", ".join(f"{v} {km:.0f} km" for v, km in sorted(totals.items(), key=lambda kv: -kv[1])))

    os.makedirs(args.out, exist_ok=True)
    base = os.path.join(args.out, demo["name"])
    with open(base + ".gpx", "w") as f:
        f.write(gpx(demo["name"], demo["title"], coords))
    with open(base + ".geojson", "w") as f:
        json.dump({"type": "Feature", "properties": {"name": demo["title"]},
                   "geometry": {"type": "LineString", "coordinates": coords}}, f)
    print(f"\n  wrote {os.path.relpath(base)}.gpx and .geojson")

    start = demo["request"]["route_points"][0]
    end = demo["request"]["route_points"][-1]
    wps = demo["handoff_waypoints"]
    print(f"\nApple Maps ({len(wps)} waypoints):\n{apple_maps_url(start, wps, end)}")
    print(f"\nGoogle Maps:\n{google_maps_url(start, wps, end)}")


if __name__ == "__main__":
    main()
