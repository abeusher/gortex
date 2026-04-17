import { unescape } from "./escape.js";
import {
  MalformedHeaderError,
  RowError,
  UnsupportedVersionError,
} from "./errors.js";
import type { Header, Row, Section } from "./types.js";

const TAG = "GCX1";
const VERSION = 1;

/**
 * Parse a single header line. Accepts the form
 *   GCX1 tool=<name> fields=<a>,<b>,... [k=v]...
 * Returns a fully populated Header or throws.
 */
export function parseHeader(line: string): Header {
  if (line.length < TAG.length || line.slice(0, TAG.length) !== TAG) {
    // Version mismatch is a distinct error so callers can fall back.
    const tagPrefix = line.split(" ")[0] ?? line;
    if (tagPrefix.startsWith("GCX")) {
      throw new UnsupportedVersionError(tagPrefix);
    }
    throw new MalformedHeaderError(
      `expected header prefix "${TAG}", got ${JSON.stringify(line.slice(0, 10))}`,
      line,
    );
  }
  const rest = line.slice(TAG.length).trim();
  const header: Header = {
    version: VERSION,
    tool: "",
    fields: [],
    meta: {},
  };
  for (const token of rest.split(" ")) {
    if (token === "") continue;
    const eq = token.indexOf("=");
    if (eq < 0) {
      throw new MalformedHeaderError(
        `malformed header token ${JSON.stringify(token)} (want key=value)`,
        line,
      );
    }
    const key = unescape(token.slice(0, eq));
    const value = unescape(token.slice(eq + 1));
    switch (key) {
      case "tool":
        header.tool = value;
        break;
      case "fields":
        header.fields = value.split(",").map(unescape);
        break;
      default:
        header.meta[key] = value;
    }
  }
  if (header.tool === "") {
    throw new MalformedHeaderError("header missing tool= key", line);
  }
  if (header.fields.length === 0) {
    throw new MalformedHeaderError("header missing fields= key", line);
  }
  return header;
}

/**
 * Lazily decode a payload into sections. Each section carries its
 * header and the rows that follow, up to either the next header or
 * end of input. Comment lines ("# ...") and blank lines are skipped.
 */
export function* decode(payload: string): Iterable<Section> {
  // Split on LF; the spec strips CR on the encoder side.
  const lines = payload.split("\n");
  let i = 0;

  // Skip leading blank lines.
  while (i < lines.length && lines[i] === "") i++;

  while (i < lines.length) {
    const headerLine = lines[i++];
    if (headerLine === "") continue;
    const header = parseHeader(headerLine);
    const rows: Row[] = [];
    while (i < lines.length) {
      const line = lines[i];
      if (line === "") {
        i++;
        continue;
      }
      if (line.startsWith("#")) {
        i++;
        continue;
      }
      if (line.startsWith(TAG)) {
        // New section boundary. Leave the cursor on the header.
        break;
      }
      rows.push(parseRow(line, header.fields));
      i++;
    }
    yield { header, rows };
  }
}

/**
 * Decode a single-section payload. Throws if the payload contains
 * more than one section.
 */
export function decodeAll(payload: string): Section {
  const sections = Array.from(decode(payload));
  if (sections.length === 0) {
    throw new MalformedHeaderError("empty payload", "");
  }
  if (sections.length > 1) {
    throw new RowError(
      "payload contains multiple sections; use decode() to iterate",
      "",
    );
  }
  return sections[0];
}

function parseRow(line: string, fields: string[]): Row {
  const values = line.split("\t");
  if (values.length > fields.length) {
    throw new RowError(
      `row has ${values.length} values but header declared ${fields.length} fields`,
      line,
    );
  }
  const row: Row = {};
  for (let i = 0; i < fields.length; i++) {
    row[fields[i]] = i < values.length ? unescape(values[i]) : "";
  }
  return row;
}
