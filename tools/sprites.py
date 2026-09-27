#!/usr/bin/env python3
"""Placeholder sprite sheets, generated rather than drawn.

**These are not art and are not meant to become art.** They are flat two-tone
silhouettes at the real dimensions, wearing the real tag names (D113), so the
view's sprite path can be written and tested against the real Aseprite export
format before a single frame is drawn.

**Drawn art goes in art/, never over the files this writes.** Export from
Aseprite to art/kai.{png,json} (JSON Array, untrimmed 128x128 cells, feet at
ORIGIN, tags named per D113) and rerun this script: drawn tags replace their
placeholders one at a time, so a sheet with only an idle drawn still loads.
Nothing in the client changes.

Deliberately ugly for the same reason the game has drawn rectangles since M1:
placeholder art that looks finished is placeholder art nobody replaces.

The tag list is read out of data/characters/*.json rather than written here, so
it cannot drift from the roster — which is also the D113 rule demonstrated: the
tag is the name the sim already has.

    python3 tools/sprites.py

Reads art/{kai,torv}.{png,json} if present; writes client/public/sprites/{kai,torv}.{png,json}.
"""

import json
import pathlib
import re
import sys
import tempfile

from PIL import Image, ImageDraw

ROOT = pathlib.Path(__file__).resolve().parent.parent
OUT = ROOT / "client" / "public" / "sprites"
# Hand-drawn Aseprite exports, {kai,torv}.{png,json}. Sources, not what ships.
ART = ROOT / "art"
FRAME_MS = 1000 / 60

# One cell holds the tallest fighter plus the reach of an extended limb.
CELL = 128
COLS = 16
# Centre-bottom, at the feet, matching the simulation's coordinate origin.
# Every sprite in every sheet uses this one and it never varies.
ORIGIN = (CELL // 2, 120)

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


def drawn(key, art):
    """The hand-drawn export for key, as {tag: [(cell, sim frames), ...]}.

    Empty when nothing has been drawn yet. Aseprite's export is a *source*
    here, not the file the client loads: it carries no move table, and an
    artist working tag by tag has a sheet that is mostly missing, which the
    loader refuses outright. So the published sheet is a composite — drawn
    tags where they exist, placeholders everywhere else — and the game runs
    at every stage of the drawing.

    A frame's Aseprite duration becomes that many sim frames, because the view
    shows texture N on the Nth frame of a state. What plays in Aseprite's
    preview is what plays in the game, at 60 fps rounding.
    """
    src = art / f"{key}.json"
    if not src.exists():
        return {}
    sheet = json.loads(src.read_text())
    if not isinstance(sheet["frames"], list):
        raise SystemExit(f"{src}: export the sheet as JSON Array, not Hash")
    img = Image.open(art / pathlib.Path(sheet["meta"]["image"]).name).convert("RGBA")

    out = {}
    for t in sheet["meta"]["frameTags"]:
        # ponytail: forward only. Reverse and ping-pong are frame orderings a
        # few lines could expand, add them when an animation asks for one.
        if t.get("direction", "forward") != "forward":
            raise SystemExit(f"{src}: tag {t['name']} is {t['direction']}, only forward is read")
        cells = []
        for fr in sheet["frames"][t["from"]:t["to"] + 1]:
            r = fr["frame"]
            # Every cell shares one size and one origin (ORIGIN, at the feet),
            # which is what lets a composite mix drawn and placeholder cells.
            if fr.get("trimmed") or (r["w"], r["h"]) != (CELL, CELL):
                raise SystemExit(f"{src}: tag {t['name']} has a {r['w']}x{r['h']} "
                                 f"or trimmed cell; export untrimmed {CELL}x{CELL}")
            cell = img.crop((r["x"], r["y"], r["x"] + CELL, r["y"] + CELL))
            cells.append((cell, max(1, round(fr["duration"] / FRAME_MS))))
        out[t["name"]] = cells
    return out


def build(key, character, art=None, out=None):
    art, out = art or ART, out or OUT
    body = BODIES[key]
    tags, move_tags = tags_for(character)
    have = drawn(key, art)

    # A drawn tag the roster never asks for is almost always a typo, and a
    # typo'd tag is silently a placeholder forever. Say so every run.
    for name in sorted(set(have) - {n for n, _ in tags}):
        print(f"{key}: art tag {name!r} matches no state or move, ignored", file=sys.stderr)

    total = sum(len(have[n]) if n in have else FRAMES[cat] for n, cat in tags)
    rows = (total + COLS - 1) // COLS
    img = Image.new("RGBA", (COLS * CELL, rows * CELL), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)

    frames, frame_tags, i = [], [], 0
    for name, cat in tags:
        first = len(frames)
        n = FRAMES[cat]
        for f in range(len(have[name]) if name in have else n):
            cx, cy = (i % COLS) * CELL, (i // COLS) * CELL
            if name in have:
                cell, reps = have[name][f]
                img.paste(cell, (cx, cy))
            else:
                draw(d, body, pose(cat, f, n), cx + ORIGIN[0], cy + ORIGIN[1])
                reps = 1
            # A held pose is one cell listed several times, not several cells.
            frames += [{
                "filename": f"{key} {i}.aseprite",
                "frame": {"x": cx, "y": cy, "w": CELL, "h": CELL},
                "rotated": False, "trimmed": False,
                "spriteSourceSize": {"x": 0, "y": 0, "w": CELL, "h": CELL},
                "sourceSize": {"w": CELL, "h": CELL},
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
            "origin": {"x": ORIGIN[0], "y": ORIGIN[1]},
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
    """A drawn tag replaces its placeholder, durations become sim frames, and
    every other tag survives — asserted on a two-frame sheet in a temp dir."""
    with tempfile.TemporaryDirectory() as tmp:
        tmp = pathlib.Path(tmp)
        Image.new("RGBA", (2 * CELL, CELL), (255, 0, 0, 255)).save(tmp / "kai.png")
        frame = lambda x, ms: {"frame": {"x": x, "y": 0, "w": CELL, "h": CELL}, "duration": ms}
        (tmp / "kai.json").write_text(json.dumps({
            "frames": [frame(0, 100), frame(CELL, 17)],
            "meta": {"image": "kai.png", "frameTags": [{"name": "idle", "from": 0, "to": 1}]},
        }))
        build("kai", roster, art=tmp, out=tmp / "out")
        sheet = json.loads((tmp / "out/kai.json").read_text())

    tags = {t["name"]: t for t in sheet["meta"]["frameTags"]}
    assert (tags["idle"]["from"], tags["idle"]["to"]) == (0, 6), tags["idle"]  # 100 ms = 6 frames, + 1
    assert tags["walk_f"]["to"] - tags["walk_f"]["from"] + 1 == FRAMES["walk"]
    assert len(tags) == len(tags_for(roster)[0]) and sheet["meta"]["moveTags"]
    print("composite ok")


def check(rosters):
    """The collapse rule, asserted on the real roster.

    It is the only part of this file with a decision in it, and it has exactly
    two ways to be wrong: collapsing a normal (5LP and 5HP are different
    animations) or renaming a super that had no sibling to merge with.
    """
    kai, torv = (strength_groups(r["moves"]) for r in rosters)

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
    paths = ((ROOT / "data/characters/00-shoto.json", "kai"),
             (ROOT / "data/characters/01-grappler.json", "torv"))
    rosters = [json.loads(p.read_text()) for p, _ in paths]
    check(rosters)
    check_composite(rosters[0])
    for (_, key), c in zip(paths, rosters):
        tags, total, art, size = build(key, c)
        print(f"{key:5} {tags:3} tags ({art} drawn) {total:4} cells  {size[0]}x{size[1]}")
