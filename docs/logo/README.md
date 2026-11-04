# The Custos logo

| | Light backgrounds | Dark backgrounds |
|---|---|---|
| Logo | [`custos.svg`](custos.svg) | [`custos-dark.svg`](custos-dark.svg) |
| Mark (icons, favicons, Cockpit's menu) | [`custos-mark.svg`](custos-mark.svg) | [`custos-mark-dark.svg`](custos-mark-dark.svg) |

**What it shows.** *Custos* is Latin for guardian or keeper. The mark is
a C, the guard around the agent (the amber dot); the bar in its opening
is the gate, mcp-gateway, the agent's only way out to the system. It
holds down to 16 px.

**Colours.**

| | |
|---|---|
| ink | `#16324F` (on dark backgrounds `#E8EEF4`) |
| agent | `#F2A93B` |

**Type.** The wordmark is *custos* in Inter Display SemiBold (SIL Open
Font License), outlined: the files need no font.

**Use.** On GitHub, a `<picture>` picks the variant for the reader's
theme (see the top of the [README](../../README.md)); elsewhere, pick the
file for the background. Keep the space around the logo at least the
gate's width, and don't recolour the agent.

**Rebuilding.** [`make-logo.py`](make-logo.py) writes the four files:

```console
python3 -m venv /tmp/logo && /tmp/logo/bin/pip install fonttools
/tmp/logo/bin/python docs/logo/make-logo.py docs/logo /path/to/InterDisplay-SemiBold.otf
```
