#!/usr/bin/env python3
"""Placeholder sprite sheets, generated rather than drawn.

**These are not art and are not meant to become art.** They are flat two-tone
silhouettes at the real dimensions, wearing the real tag names (D113), so the
view's sprite path can be written and tested against the real Aseprite export
format before a single frame is drawn.

**Drawn art goes in art/<character>/, never over the files this writes.**
Export from Aseprite into that folder (JSON Array, untrimmed, tags named per
D113, a slice named "origin" whose pivot is the feet) and rerun this script:
drawn tags replace their placeholders one at a time, so a sheet with only an
idle drawn still loads. Any number of exports per character — one file per
animation, per group, or one for everything — each with its own canvas size;
they are lined up on their origin slices. Nothing in the client changes.

Every character in data/characters/ gets a sheet, named after its "name"
lowercased, which is the name the client asks for.

Deliberately ugly for the same reason the game has drawn rectangles since M1:
placeholder art that looks finished is placeholder art nobody replaces.

The tag list is read out of data/characters/*.json rather than written here, so
it cannot drift from the roster — which is also the D113 rule demonstrated: the
tag is the name the sim already has.

    python3 tools/sprites.py

Reads art/<name>/*.{png,json} if present; writes client/public/sprites/<name>.{png,json}.
"""

import json
import pathlib
import re
import sys
import tempfile

from PIL import Image, ImageDraw

ROOT = pathlib.Path(__file__).resolve().parent.parent
OUT = ROOT / "client" / "public" / "sprites"
# Hand-drawn Aseprite exports, art/<name>/*.{png,json}. Sources, not what ships.
ART = ROOT / "art"
# The largest texture every WebGL device is safe to assume.
MAX_TEXTURE = 4096
FRAME_MS = 1000 / 60

# The placeholder canvas, used until a character has a drawn export, whose
# own canvas then wins. One sprite pixel is half a sim unit (the view draws
# sprites 1:1 at SCALE 2), so 128 px in front of the feet is 64 units: the
# longest hitbox on the roster is 58. Headroom is the 623HP's 72 units. The
# 64 px behind the feet is lean and a body lying flat.
CELL = (192, 160)
COLS = 16
# The feet, matching the simulation's coordinate origin. One per sheet.
ORIGIN = (64, 152)

# Frames per animation, by category. These are the Animation Budget's counts
# after D113 cut the ones the view cannot select between.
FRAMES = {
    "idle": 6, "walk": 6, "crouch": 4, "dash": 5, "jump": 5,
    "stun": 4, "block": 3, "thrown": 6, "down": 8,
    "normal": 6, "air": 4, "special": 8, "super": 12, "throw": 6,
}

# The eleven state tags. StateAttack has none — its animation is the move.
# StatePreJump and StateLanding reuse crouch.
STATE_TAGS = [
    ("idle", "idle"), ("walk_f", "walk"), ("walk_b", "walk"),
    ("crouch", "crouch"), ("dash", "dash"), ("dash_b", "dash"),
    ("jump", "jump"), ("hitstun", "stun"), ("blockstun", "block"),
    ("thrown", "thrown"), ("knockdown", "down"),
]

# Silhouette metrics, in sim units at 1 px each, measured up from the feet.
# The two must be identifiable from outline alone (Art Direction rule 1), so
# every number below is the opposite of its neighbour: line against wedge.
BODIES = {
    # Kai carries a neck: the head clears the shoulders, so the outline is a
    # column with a notch. Torv's head is sunk *into* the shoulder line and
    # never clears it, which is the wedge's flat top. That one gap is most of
    # what tells them apart at 96 px.
    "kai": dict(
        height=96, head_r=9, head_y=86, shoulder_w=34, hip_w=24,
        torso_top=74, torso_bot=44, leg_w=9, arm_w=7,
        fill=(214, 206, 188), shade=(150, 142, 126), tails=True,
    ),
    "torv": dict(
        height=104, head_r=11, head_y=90, shoulder_w=58, hip_w=38,
        torso_top=84, torso_bot=48, leg_w=14, arm_w=12,
        fill=(196, 150, 118), shade=(134, 100, 78), tails=False,
    ),
}
# A character added to the roster before anyone gives it a silhouette. Grey,
# so it is obviously nobody's final look.
DEFAULT_BODY = dict(BODIES["kai"], fill=(170, 170, 170), shade=(110, 110, 110), tails=False)


def strength_groups(moves):
    """Map each move id to the tag it draws from.

    A special in three strengths is one animation at three speeds (D21), so the
    three ids collapse onto one strength-stripped tag. A *normal* never does —
    5LP and 5HP are different animations — and the discriminator is data rather
    than a spelling rule: only a move with a motion is a special. Collapse also
    needs a group of more than one, which is what keeps the supers apart: there
    is exactly one 236236LP, so it keeps its own name.
    """
    stripped = {}
    for mv in moves:
        mid = mv["id"]
        motion = mv.get("input", {}).get("motion", "")
        m = re.fullmatch(r"(\d+)([LMH])([PK])", mid)
        stripped[mid] = f"{m.group(1)}{m.group(3)}" if motion and m else mid

    counts = {}
    for name in stripped.values():
        counts[name] = counts.get(name, 0) + 1
    # A group of one keeps its own id: there is exactly one 236236LP, and
    # collapsing it to 236236P would rename a super for no reason.
    return {mid: (name if counts[name] > 1 else mid) for mid, name in stripped.items()}


def move_category(mv):
    mid, motion = mv["id"], mv.get("input", {}).get("motion", "")
    if mv.get("super"):
        return "super"
    if mid.startswith("j"):
        return "air"
    if motion:
        return "special"
    if mid in ("LPLK",):
        return "throw"
    return "normal"


def tags_for(character):
    """Every tag this character's sheet must carry, in draw order.

    Also returns the move-index-to-tag table. The view gets a `moveIndex` in
    the render snapshot and has no way to turn it into a name — `sim.Move`
    carries frame data, not its own id — so the table ships with the sheet,
    built from the same JSON array the sim loads its moves from, in the same
    order. Presentation naming stays out of the simulation.
    """
    tags = [(name, cat) for name, cat in STATE_TAGS]
    groups = strength_groups(character["moves"])
    seen = set()
    move_tags = []
    for mv in character["moves"]:
        tag = groups[mv["id"]]
        move_tags.append(tag)
        if tag in seen:
            continue
        seen.add(tag)
        tags.append((tag, move_category(mv)))
    return tags, move_tags


def pose(cat, f, n):
    """Pose parameters for frame f of n. Crude on purpose.

    Returns bob, squash, lean, reach (how far the leading limb extends) and
    split (how far the legs part). Enough that each tag animates visibly and
    differently, which is all the loader needs to be tested against.
    """
    t = f / max(n - 1, 1)
    if cat == "idle":
        return dict(bob=[0, 1, 1, 0, -1, -1][f % 6])
    if cat == "walk":
        return dict(bob=[0, 1, 0, -1][f % 4], split=[0, 6, 9, 6, 0, -6][f % 6])
    if cat == "crouch":
        return dict(squash=1 - 0.3 * min(t * 2, 1))
    if cat == "dash":
        return dict(lean=4 + 6 * t, split=8, squash=0.94)
    if cat == "jump":
        return dict(squash=0.92, tuck=0.45 + 0.2 * t, lean=2, bob=int(4 * t))
    if cat == "stun":
        return dict(lean=-13 + 4 * t, bob=-2, tuck=0.1)
    if cat == "block":
        return dict(lean=-3, reach=6, squash=0.97)
    if cat == "thrown":
        return dict(lean=-14 * (1 - t), squash=0.85, bob=int(8 * (1 - t)))
    if cat == "down":
        return dict(down=min(t * 2.2, 1.0))
    # Attacks: wind up, extend through the active frames, pull back.
    peak = 0.45
    ramp = t / peak if t < peak else max(0.0, 1 - (t - peak) / (1 - peak))
    depth = {"normal": 26, "air": 22, "special": 34, "super": 40, "throw": 20}[cat]
    return dict(reach=depth * ramp, lean=3 * ramp, squash=1 - 0.05 * ramp)


def draw(d, body, p, ox, oy):
    """One fighter into the cell whose origin is (ox, oy)."""
    bob = p.get("bob", 0)
    squash = p.get("squash", 1.0)
    lean = p.get("lean", 0)
    reach = p.get("reach", 0)
    split = p.get("split", 0)
    down = p.get("down", 0)
    tuck = p.get("tuck", 0)

    fill, shade = body["fill"], body["shade"]
    # A body laid flat is as long as it was tall, so it has to be pulled back
    # over the origin or half of it lands outside the cell. The origin is still
    # the feet — it is the art that moved, not the coordinate system.
    recentre = -down * body["height"] * 0.42

    def box(x0, y0, x1, y1, color):
        """Local coords: x forward from centre, y up from the feet."""
        pts = []
        for x, y in ((x0, y0), (x1, y1)):
            y = y * squash + bob
            # Lying down: rotate the whole body onto its back about the feet.
            if down:
                x, y = x + y * down * 0.95 + recentre, y * (1 - down * 0.86)
            pts.append((ox + x + lean * (y / body["height"]), oy - y))
        d.rectangle([min(pts[0][0], pts[1][0]), min(pts[0][1], pts[1][1]),
                     max(pts[0][0], pts[1][0]), max(pts[0][1], pts[1][1])], fill=color)

    hw_s, hw_h = body["shoulder_w"] / 2, body["hip_w"] / 2
    top, bot, lw, aw = body["torso_top"], body["torso_bot"], body["leg_w"], body["arm_w"]

    # Tucked legs come up off the floor rather than shortening the body, which
    # is what makes a jump read as a jump and not as a shorter idle.
    foot = bot * tuck

    # Back leg and back arm first, in shade — two tones is the whole rendering.
    box(-hw_h - split / 2, foot, -hw_h + lw - split / 2, bot, shade)
    box(-hw_s, top - aw, -hw_s + aw, bot + 4, shade)

    # Torso: a tapered stack, which is where the two silhouettes diverge.
    steps = 6
    for i in range(steps):
        y0 = bot + (top - bot) * i / steps
        y1 = bot + (top - bot) * (i + 1) / steps
        w = hw_h + (hw_s - hw_h) * ((i + 1) / steps) ** 1.6
        box(-w, y0, w, y1, fill)

    # Front leg.
    box(hw_h - lw + split / 2, foot, hw_h + split / 2, bot, fill)
    # Front arm, which is the thing that extends on an attack.
    box(hw_s - aw, top - aw, hw_s + reach, top, fill)

    # Head: sunk between Torv's shoulders, clear of Kai's.
    hr, hy = body["head_r"], body["head_y"]
    box(-hr, hy - hr, hr, hy + hr, fill)

    # Kai's belt tails: asymmetry that reads at 96 px and costs two rectangles.
    if body["tails"]:
        box(-hw_h - 7, bot - 2, -hw_h, bot + 3, shade)
        box(-hw_h - 11, bot - 8, -hw_h - 4, bot - 3, shade)


def read_export(src):
    """One Aseprite export: ({tag: [(cell, sim frames), ...]}, canvas, origin).

    A frame's Aseprite duration becomes that many sim frames, because the view
    shows texture N on the Nth frame of a state. What plays in Aseprite's
    preview is what plays in the game, at 60 fps rounding.

    The canvas and the feet come from the export, so the canvas can grow
    mid-drawing. Aseprite exports no origin, but it does export slice pivots,
    and resizing the canvas moves a slice with the pixels. The feet are the
    pivot of a slice named "origin". Without one the fighter would float or
    sink against its boxes, so it is required.
    """
    sheet = json.loads(src.read_text())
    if not isinstance(sheet["frames"], list):
        raise SystemExit(f"{src}: export the sheet as JSON Array, not Hash")
    img = Image.open(src.parent / pathlib.Path(sheet["meta"]["image"]).name).convert("RGBA")

    # Untrimmed frames are the whole canvas, so every one is the same size.
    sizes = {(fr["frame"]["w"], fr["frame"]["h"]) for fr in sheet["frames"]}
    if len(sizes) != 1 or any(fr.get("trimmed") for fr in sheet["frames"]):
        raise SystemExit(f"{src}: frames are trimmed or differ in size ({sorted(sizes)}); "
                         "export untrimmed")
    cell = sizes.pop()

    # A slice keyed per frame is an origin that moves, which is the bug this
    # exists to prevent — the feet are the sim's position, and the boxes hang
    # off it. Refused rather than read from the first key, or a nudge on frame
    # 40 would be silently ignored and the drawing would drift from its boxes.
    slices = [sl for sl in sheet["meta"].get("slices", []) if sl["name"] == "origin"]
    if slices and len(slices[0]["keys"]) > 1:
        frames = [k["frame"] for k in slices[0]["keys"]]
        raise SystemExit(f"{src}: slice 'origin' moves on frames {frames}; it must stay put — "
                         "the feet are the fighter's position. Undo the move, or delete the "
                         "slice and make it again on frame 1")
    key0 = slices[0]["keys"][0] if slices else {}
    if "pivot" not in key0:
        raise SystemExit(f"{src}: no slice named 'origin' with a pivot at the feet "
                         "(Slice tool, then set its pivot, and export with slices)")
    b, pv = key0["bounds"], key0["pivot"]
    origin = (b["x"] + pv["x"], b["y"] + pv["y"])
    if not (0 <= origin[0] < cell[0] and 0 <= origin[1] < cell[1]):
        raise SystemExit(f"{src}: origin {origin} is outside the {cell[0]}x{cell[1]} canvas")

    out = {}
    for t in sheet["meta"]["frameTags"]:
        # ponytail: forward only. Reverse and ping-pong are frame orderings a
        # few lines could expand, add them when an animation asks for one.
        if t.get("direction", "forward") != "forward":
            raise SystemExit(f"{src}: tag {t['name']} is {t['direction']}, only forward is read")
        cells = []
        for fr in sheet["frames"][t["from"]:t["to"] + 1]:
            r = fr["frame"]
            crop = img.crop((r["x"], r["y"], r["x"] + cell[0], r["y"] + cell[1]))
            cells.append((crop, max(1, round(fr["duration"] / FRAME_MS))))
        out[t["name"]] = cells
    return out, cell, origin


def drawn(key, art):
    """Every drawn tag for key, lined up in one cell: (tags, cell, origin).

    Aseprite's exports are *sources* here, not the file the client loads: they
    carry no move table, and an artist working tag by tag has a sheet that is
    mostly missing, which the loader refuses outright. So the published sheet
    is a composite — drawn tags where they exist, placeholders everywhere
    else — and the game runs at every stage of the drawing.

    The exports may have different canvases, so each is placed in a shared
    cell big enough for all of them *measured from the feet*: the most room
    any export has behind, in front of, above and below its origin. The
    placeholder canvas takes part, so placeholders never clip. One cell, one
    anchor, which is all the client knows how to read.
    """
    exports = []
    for src in sorted((art / key).glob("*.json")):
        exports.append((src, *read_export(src)))

    left, up = ORIGIN
    right, down = CELL[0] - ORIGIN[0], CELL[1] - ORIGIN[1]
    for _, _, (w, h), (ox, oy) in exports:
        left, right, up, down = max(left, ox), max(right, w - ox), max(up, oy), max(down, h - oy)
    cell, origin = (left + right, up + down), (left, up)

    tags, source = {}, {}
    for src, found, _, (ox, oy) in exports:
        for name, cells in found.items():
            if name in tags:
                raise SystemExit(f"{src}: tag {name!r} is also in {source[name]}; draw it once")
            padded = []
            for crop, reps in cells:
                c = Image.new("RGBA", cell, (0, 0, 0, 0))
                c.paste(crop, (left - ox, up - oy))
                padded.append((c, reps))
            tags[name], source[name] = padded, src.name
    return tags, cell, origin


def build(key, character, art=None, out=None):
    art, out = art or ART, out or OUT
    body = BODIES.get(key, DEFAULT_BODY)
    tags, move_tags = tags_for(character)
    # Placeholders are drawn on the drawn canvas at the drawn feet, so the
    # two kinds of cell share one size and one anchor.
    have, (cw, ch), origin = drawn(key, art)

    # A drawn tag the roster never asks for is almost always a typo, and a
    # typo'd tag is silently a placeholder forever. Say so every run.
    for name in sorted(set(have) - {n for n, _ in tags}):
        print(f"{key}: art tag {name!r} matches no state or move, ignored", file=sys.stderr)

    total = sum(len(have[n]) if n in have else FRAMES[cat] for n, cat in tags)
    cols = min(COLS, MAX_TEXTURE // cw)
    rows = (total + cols - 1) // cols
    if rows * ch > MAX_TEXTURE:
        raise SystemExit(f"{key}: {total} cells of {cw}x{ch} need a {cols * cw}x{rows * ch} "
                         f"sheet, over the {MAX_TEXTURE} px texture limit — shrink the largest "
                         "canvas, or move effects out of the character's exports")
    img = Image.new("RGBA", (cols * cw, rows * ch), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)

    frames, frame_tags, i = [], [], 0
    for name, cat in tags:
        first = len(frames)
        n = FRAMES[cat]
        for f in range(len(have[name]) if name in have else n):
            cx, cy = (i % cols) * cw, (i // cols) * ch
            if name in have:
                cell, reps = have[name][f]
                img.paste(cell, (cx, cy))
            else:
                draw(d, body, pose(cat, f, n), cx + origin[0], cy + origin[1])
                reps = 1
            # A held pose is one cell listed several times, not several cells.
            frames += [{
                "filename": f"{key} {i}.aseprite",
                "frame": {"x": cx, "y": cy, "w": cw, "h": ch},
                "rotated": False, "trimmed": False,
                "spriteSourceSize": {"x": 0, "y": 0, "w": cw, "h": ch},
                "sourceSize": {"w": cw, "h": ch},
                "duration": round(FRAME_MS) if name in have else 100,
            }] * reps
            i += 1
        frame_tags.append({"name": name, "from": first, "to": len(frames) - 1, "direction": "forward"})

    out.mkdir(parents=True, exist_ok=True)
    img.save(out / f"{key}.png")
    (out / f"{key}.json").write_text(json.dumps({
        "frames": frames,
        "meta": {
            "app": "tools/sprites.py", "version": "composite" if have else "placeholder",
            "image": f"{key}.png", "format": "RGBA8888",
            "size": {"w": img.width, "h": img.height}, "scale": "1",
            "origin": {"x": origin[0], "y": origin[1]},
            "frameTags": frame_tags,
            # Move index to tag, in roster order. Not an Aseprite field, which
            # is why the drawn export is a source and this file is generated:
            # the price of keeping move ids out of sim.Move. CI reruns this
            # script and refuses a diff, so the table cannot drift.
            "moveTags": move_tags,
        },
    }, indent=2) + "\n")
    return len(tags), total, len(have), img.size


def check_composite(roster):
    """Drawn tags replace their placeholders, durations become sim frames, and
    two exports with different canvases land in one cell lined up on their
    feet — asserted on two tiny exports in a temp dir."""

    def export(d, name, w, h, origin, tag, durations):
        Image.new("RGBA", (len(durations) * w, h), (255, 0, 0, 255)).save(d / f"{name}.png")
        (d / f"{name}.json").write_text(json.dumps({
            "frames": [{"frame": {"x": i * w, "y": 0, "w": w, "h": h}, "duration": ms}
                       for i, ms in enumerate(durations)],
            "meta": {"image": f"{name}.png", "frameTags": [{"name": tag, "from": 0,
                                                             "to": len(durations) - 1}],
                     "slices": [{"name": "origin", "keys": [{"frame": 0,
                                 "bounds": {"x": origin[0] - 5, "y": origin[1] - 5, "w": 10, "h": 10},
                                 "pivot": {"x": 5, "y": 5}}]}]},
        }))

    with tempfile.TemporaryDirectory() as tmp:
        tmp = pathlib.Path(tmp)
        (tmp / "kai").mkdir()
        # Wider behind the feet and taller than the placeholder canvas...
        export(tmp / "kai", "idle", 200, 170, (70, 159), "idle", [100, 17])
        # ...and one that is small, but deeper below its feet than either.
        export(tmp / "kai", "walk", 100, 100, (50, 80), "walk_f", [100])
        build("kai", roster, art=tmp, out=tmp / "out")
        sheet = json.loads((tmp / "out/kai.json").read_text())
        img = Image.open(tmp / "out/kai.png")

        tags = {t["name"]: t for t in sheet["meta"]["frameTags"]}
        assert (tags["idle"]["from"], tags["idle"]["to"]) == (0, 6), tags["idle"]  # 100 ms = 6, + 1
        assert tags["walk_f"]["to"] - tags["walk_f"]["from"] + 1 == 6
        assert tags["walk_b"]["to"] - tags["walk_b"]["from"] + 1 == FRAMES["walk"]  # placeholder
        assert len(tags) == len(tags_for(roster)[0]) and sheet["meta"]["moveTags"]

        # Behind: 70 (idle). In front: 130 (idle). Above: 159 (idle). Below: 20 (walk).
        assert {(f["frame"]["w"], f["frame"]["h"]) for f in sheet["frames"]} == {(200, 179)}
        assert sheet["meta"]["origin"] == {"x": 70, "y": 159}, sheet["meta"]["origin"]
        # The walk cell's feet are on the shared feet: its 100x100 canvas starts
        # at (70-50, 159-80) inside its cell, and nothing is drawn left of it.
        r = sheet["frames"][tags["walk_f"]["from"]]["frame"]
        assert img.getpixel((r["x"] + 20, r["y"] + 79))[3] == 255
        assert img.getpixel((r["x"] + 19, r["y"] + 79))[3] == 0
    print("composite ok")


def check(rosters):
    """The collapse rule, asserted on the real roster.

    It is the only part of this file with a decision in it, and it has exactly
    two ways to be wrong: collapsing a normal (5LP and 5HP are different
    animations) or renaming a super that had no sibling to merge with.
    """
    kai, torv = (strength_groups(rosters[name]["moves"]) for name in ("kai", "torv"))

    # Three strengths of one special, one animation (D21, D113).
    assert kai["236LP"] == kai["236MP"] == kai["236HP"] == "236P", kai["236LP"]
    assert kai["623LP"] == kai["623HP"] == "623P"
    assert torv["63214LP"] == torv["63214MP"] == torv["63214HP"] == "63214P"

    # Normals have no motion, so they never collapse however they are spelled.
    for mid in ("5LP", "5MP", "5HP", "2LP", "2HP", "jLP", "jHP"):
        assert kai[mid] == mid, f"{mid} collapsed to {kai[mid]}"

    # A group of one keeps its own name: there is one 236236LP, not three.
    for mid in ("236236LP", "236236LK", "214214LP", "236EX", "623EX", "LPLK"):
        assert kai[mid] == mid, f"{mid} renamed to {kai[mid]}"

    print("collapse rule ok")


if __name__ == "__main__":
    # The whole roster, keyed by the name the client lowercases to find a sheet.
    rosters = {}
    for p in sorted((ROOT / "data" / "characters").glob("*.json")):
        c = json.loads(p.read_text())
        rosters[c["name"].lower()] = c
    check(rosters)
    check_composite(rosters["kai"])
    for key, c in rosters.items():
        tags, total, art, size = build(key, c)
        print(f"{key:5} {tags:3} tags ({art} drawn) {total:4} cells  {size[0]}x{size[1]}")
