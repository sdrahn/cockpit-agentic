"""Builds the Custos logo SVGs: the mark, and the mark with the wordmark
(Inter Display SemiBold, outlined). Usage: make-logo.py OUTDIR [InterDisplay-SemiBold.otf] (needs fonttools)"""
import math
import sys

from fontTools.pens.boundsPen import BoundsPen
from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen
from fontTools.ttLib import TTFont

out = sys.argv[1]
INK, INK_DARK, AMBER = "#16324F", "#E8EEF4", "#F2A93B"
# Fixed colours per file: a light-background and a dark-background
# variant (on GitHub, a <picture> picks the one for the reader's theme).
STYLE = ""


def arc(cx, cy, r, a0, a1):
    def p(a):
        return cx + r * math.cos(math.radians(a)), cy - r * math.sin(math.radians(a))
    x0, y0 = p(a0)
    x1, y1 = p(a1)
    large = 1 if (a1 - a0) % 360 > 180 else 0
    return f"M{x0:.2f} {y0:.2f}A{r} {r} 0 {large} 0 {x1:.2f} {y1:.2f}"


# The mark on a 128 grid: the guard (a C, open to the right), the gate in
# its opening (mcp-gateway, the agent's only way out), the agent inside.
MARK = (f'<path class="inks" d="{arc(64, 64, 42, 38, 322)}" fill="none" stroke-width="16" stroke-linecap="round"/>'
        '<rect class="ink" x="96" y="50" width="12" height="28" rx="4"/>'
        f'<circle cx="62" cy="64" r="13" fill="{AMBER}"/>')


def svg(w, h, body, title, ink=INK):
    body = body.replace('class="inks"', f'stroke="{ink}"').replace('class="ink"', f'fill="{ink}"')
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {w:.0f} {h:.0f}" width="{w:.0f}" height="{h:.0f}" role="img" aria-label="{title}">'
            f'<title>{title}</title>{STYLE}{body}</svg>\n')


for suffix, ink in (("", INK), ("-dark", INK_DARK)):
    open(f"{out}/custos-mark{suffix}.svg", "w").write(svg(128, 128, MARK, "Custos", ink))

# The wordmark: "custos", outlined, its x-height about the mark's opening.
font = TTFont(sys.argv[2] if len(sys.argv) > 2 else "/usr/share/fonts/opentype/inter/InterDisplay-SemiBold.otf")
gs, cmap = font.getGlyphSet(), font.getBestCmap()
upm = font["head"].unitsPerEm
xh = font["OS/2"].sxHeight
scale = 44 / xh  # x-height 44 units on the mark's grid
tracking = 0.01 * upm
paths, x = [], 0.0
for ch in "custos":
    g = cmap[ord(ch)]
    pen = SVGPathPen(gs)
    # Font units are y-up; flip and place on a baseline at y = 0.
    gs[g].draw(TransformPen(pen, (scale, 0, 0, -scale, x * scale, 0)))
    paths.append(pen.getCommands())
    x += gs[g].width + tracking
bp = BoundsPen(gs)
for ch in "custos":
    gs[cmap[ord(ch)]].draw(bp)
text_w = (x - tracking) * scale
gap, mark_w = 30, 128
# Baseline so the x-height is centred on the mark's centre (y = 64).
base = 64 + 44 / 2
word = f'<g class="ink" transform="translate({mark_w + gap} {base:.2f})"><path d="{"".join(paths)}"/></g>'
w = mark_w + gap + text_w + 4
for suffix, ink in (("", INK), ("-dark", INK_DARK)):
    open(f"{out}/custos{suffix}.svg", "w").write(svg(w, 128, MARK + word, "Custos", ink))
print(f"lockup {w:.0f}x128")
