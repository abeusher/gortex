/**
 * Decode a GCX field value by reversing the minimal escape alphabet
 * (`\\`, `\t`, `\n`). Unknown `\x` sequences decode to the literal
 * byte `x` so a pathological payload cannot wedge the decoder.
 */
export function unescape(s: string): string {
  if (!s.includes("\\")) return s;
  let out = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i];
    if (c !== "\\" || i + 1 >= s.length) {
      out += c;
      continue;
    }
    i++;
    const next = s[i];
    switch (next) {
      case "\\":
        out += "\\";
        break;
      case "t":
        out += "\t";
        break;
      case "n":
        out += "\n";
        break;
      default:
        out += next;
    }
  }
  return out;
}
