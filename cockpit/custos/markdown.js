/* markdown renders the Markdown a model writes (CommonMark's common
 * part: headings, paragraphs, lists, tables, fenced code, quotes, rules,
 * code spans, bold, emphasis) as DOM nodes. The text is the agent's and
 * untrusted (a prompt injection may have written it): nothing becomes
 * HTML, only text nodes in elements made here, and a link is shown as its
 * text and address, not followed.
 */
"use strict";

function markdown(text) {
    const lines = String(text || "").replace(/\r\n?/g, "\n").split("\n");
    const out = [];
    let i = 0;
    const blank = l => /^\s*$/.test(l);
    const isRow = l => /^\s*\|.*\|\s*$/.test(l);
    const isSep = l => /^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)*\|?\s*$/.test(l);
    const listItem = l => l.match(/^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$/);
    const startsBlock = l => /^\s*(```|~~~|#{1,6}\s|>|([-*_])\s*\2\s*\2[\s\-*_]*$)/.test(l) || listItem(l) ||
        isRow(l);

    while (i < lines.length) {
        const line = lines[i];
        if (blank(line)) { i++; continue; }

        const fence = line.match(/^\s*(```|~~~)\s*([\w+-]*)/);
        if (fence) {
            const body = [];
            i++;
            while (i < lines.length && !lines[i].trim().startsWith(fence[1])) body.push(lines[i++]);
            i++; // the closing fence
            out.push(el("pre", { class: "md-code" }, el("code", null, body.join("\n"))));
            continue;
        }

        const h = line.match(/^\s*(#{1,6})\s+(.*?)\s*#*\s*$/);
        if (h) {
            // A response's headings sit below the dialog's own.
            out.push(el("h" + Math.min(6, h[1].length + 2), { class: "md-h" }, inline(h[2])));
            i++;
            continue;
        }

        if (/^\s*([-*_])\s*\1\s*\1[\s\-*_]*$/.test(line)) {
            out.push(el("hr"));
            i++;
            continue;
        }

        if (isRow(line) && i + 1 < lines.length && isSep(lines[i + 1])) {
            const cells = l => l.trim().replace(/^\|/, "").replace(/\|$/, "").split(/(?<!\\)\|/).map(c => c.trim().replace(/\\\|/g, "|"));
            const align = cells(lines[i + 1]).map(c => c.endsWith(":") ? (c.startsWith(":") ? "center" : "right") : null);
            const head = cells(line);
            i += 2;
            const rows = [];
            while (i < lines.length && isRow(lines[i])) rows.push(cells(lines[i++]));
            const cell = (tag, c, k) => el(tag, align[k] ? { style: "text-align: " + align[k] } : null, inline(c));
            out.push(el("div", { class: "md-table" }, el("table", null,
                el("thead", null, el("tr", null, head.map((c, k) => cell("th", c, k)))),
                el("tbody", null, rows.map(r => el("tr", null, head.map((_, k) => cell("td", r[k] || "", k))))))));
            continue;
        }

        if (/^\s*>/.test(line)) {
            const body = [];
            while (i < lines.length && /^\s*>/.test(lines[i])) body.push(lines[i++].replace(/^\s*>\s?/, ""));
            out.push(el("blockquote", { class: "md-quote" }, markdown(body.join("\n"))));
            continue;
        }

        if (listItem(line)) {
            out.push(list());
            continue;
        }

        const para = [];
        while (i < lines.length && !blank(lines[i]) && !(para.length && startsBlock(lines[i]))) para.push(lines[i++].trim());
        out.push(el("p", null, inline(para.join(" "))));
    }
    return out;

    /* list reads the items from line i at one indentation; deeper ones
     * make a nested list in the item before them. */
    function list() {
        const first = listItem(lines[i]);
        const indent = first[1].length;
        const ordered = /\d/.test(first[2]);
        const items = [];
        while (i < lines.length) {
            const m = listItem(lines[i]);
            if (!m) {
                // A continuation line of the item before.
                if (!blank(lines[i]) && items.length && /^\s+\S/.test(lines[i]) && !startsBlock(lines[i])) {
                    items[items.length - 1].text.push(lines[i++].trim());
                    continue;
                }
                break;
            }
            if (m[1].length > indent && items.length) {
                items[items.length - 1].sub.push(list());
                continue;
            }
            if (m[1].length < indent || /\d/.test(m[2]) !== ordered) break;
            items.push({ text: [m[3]], sub: [] });
            i++;
        }
        const start = ordered ? parseInt(first[2], 10) : 1;
        return el(ordered ? "ol" : "ul", ordered && start !== 1 ? { start: String(start) } : null,
            items.map(it => el("li", null, inline(it.text.join(" ")), it.sub)));
    }
}

/* inline renders code spans, links (as text), bold and emphasis; not
 * within a word, so exec__disk_usage stays a tool name. */
function inline(s) {
    const out = [];
    const re = /(`+)([\s\S]*?[^`])\1(?!`)|\[([^\]]+)\]\(([^)\s]+)\)|(?<![\w*])(\*\*|__)(?=\S)([\s\S]*?\S)\5(?!\w)|(?<![\w*])(\*|_)(?=\S)([\s\S]*?\S)\7(?![\w*])/;
    while (s) {
        const m = s.match(re);
        if (!m) { out.push(s); break; }
        if (m.index > 0) out.push(s.slice(0, m.index));
        if (m[1]) out.push(el("code", null, m[2].trim()));
        else if (m[3]) out.push(el("span", { class: "md-link" }, inline(m[3]), " ", el("span", { class: "muted" }, "(" + m[4] + ")")));
        else if (m[5]) out.push(el("strong", null, inline(m[6])));
        else out.push(el("em", null, inline(m[8])));
        s = s.slice(m.index + m[0].length);
    }
    return out;
}
