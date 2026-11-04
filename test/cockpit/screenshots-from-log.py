"""Writes the screenshots that the CI job cockpit-vm prints into its log
(base64 between "SHOT-BEGIN <name>" and "SHOT-END <name>", after GitHub's
timestamps) as PNG files: the documentation's docs/screenshots.
Usage: screenshots-from-log.py LOG OUTDIR"""
import base64, os, re, sys
log, out = sys.argv[1], sys.argv[2]
os.makedirs(out, exist_ok=True)
name, buf = None, []
for line in open(log, encoding="utf-8", errors="replace"):
    line = re.sub(r"^\S+Z ", "", line.rstrip("\r\n"))
    m = re.match(r"SHOT-(BEGIN|END) (\S+)$", line)
    if m and m.group(1) == "BEGIN":
        name, buf = m.group(2), []
    elif m and name:
        data = base64.b64decode("".join(buf))
        assert data[:8] == b"\x89PNG\r\n\x1a\n", name
        open(os.path.join(out, name + ".png"), "wb").write(data)
        print(name, len(data))
        name = None
    elif name:
        buf.append(line.strip())
