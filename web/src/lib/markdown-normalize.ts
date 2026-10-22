// CommonMark requires every line of a list item's content to be indented to the
// item's content column. LLM-written reports often open a fenced code block at
// the correct indent but then emit the code body at column 0. The parser then
// closes the list item at the first unindented line, and the block's original
// closing fence is re-read as a NEW opening fence — swallowing everything after
// it into one giant code block.
//
// This normaliser makes indented fenced blocks tolerant: content lines (and the
// closing fence) that are shallower than the opening fence are padded up to it.
// Fences at column 0, well-indented blocks, and tab-indented blocks are left
// untouched, so valid Markdown round-trips unchanged.

const OPEN_FENCE = /^([ \t]*)(`{3,}|~{3,})(.*)$/;
const CLOSE_FENCE = /^([ \t]*)(`{3,}|~{3,})[ \t]*$/;

export function normalizeFencedCodeInLists(markdown: string): string {
  if (!markdown.includes("```") && !markdown.includes("~~~")) return markdown;

  const lines = markdown.split("\n");
  const out: string[] = [];

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const open = line.match(OPEN_FENCE);
    if (!open) {
      out.push(line);
      continue;
    }

    const indent = open[1];
    // Only space-indented fences can sit inside a list item (tabs make the
    // content-column arithmetic ambiguous — leave those alone).
    if (indent.length === 0 || indent.includes("\t")) {
      out.push(line);
      continue;
    }

    const marker = open[2];
    const fenceChar = marker[0];

    // Find the closing fence: same character, at least as long, no info string.
    let close = -1;
    for (let j = i + 1; j < lines.length; j++) {
      const m = lines[j].match(CLOSE_FENCE);
      if (m && m[2][0] === fenceChar && m[2].length >= marker.length) {
        close = j;
        break;
      }
    }

    // Unclosed fence: pad to the end of the document so the rest stays code.
    const bodyEnd = close === -1 ? lines.length : close;
    out.push(line);
    for (let k = i + 1; k < bodyEnd; k++) {
      out.push(padTo(lines[k], indent.length));
    }
    if (close !== -1) {
      out.push(padTo(lines[close], indent.length));
      i = close;
    } else {
      i = lines.length - 1;
    }
  }

  return out.join("\n");
}

function padTo(line: string, indent: number): string {
  if (line.trim() === "") return line;
  const leading = line.match(/^[ \t]*/)?.[0] ?? "";
  if (leading.includes("\t") || leading.length >= indent) return line;
  return " ".repeat(indent - leading.length) + line;
}
