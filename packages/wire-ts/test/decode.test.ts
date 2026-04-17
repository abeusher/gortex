import { strict as assert } from "node:assert";
import { test } from "node:test";

import {
  MalformedHeaderError,
  RowError,
  UnsupportedVersionError,
  decode,
  decodeAll,
  parseHeader,
} from "../dist/index.js";

test("parseHeader reads tool, fields, and meta", () => {
  const h = parseHeader("GCX1 tool=search_symbols fields=id,kind,name total=3 truncated=false");
  assert.equal(h.version, 1);
  assert.equal(h.tool, "search_symbols");
  assert.deepEqual(h.fields, ["id", "kind", "name"]);
  assert.equal(h.meta.total, "3");
  assert.equal(h.meta.truncated, "false");
});

test("parseHeader rejects missing tool", () => {
  assert.throws(
    () => parseHeader("GCX1 fields=a,b"),
    (err: unknown) => err instanceof MalformedHeaderError,
  );
});

test("parseHeader rejects missing fields", () => {
  assert.throws(
    () => parseHeader("GCX1 tool=x"),
    (err: unknown) => err instanceof MalformedHeaderError,
  );
});

test("parseHeader throws UnsupportedVersionError on GCX2", () => {
  assert.throws(
    () => parseHeader("GCX2 tool=x fields=a"),
    (err: unknown) => err instanceof UnsupportedVersionError,
  );
});

test("decodeAll yields header + rows", () => {
  const payload =
    "GCX1 tool=search_symbols fields=id,kind,name\n" +
    "# 2 matches\n" +
    "1\tfunc\tFoo\n" +
    "2\tmethod\tBar\n";
  const { header, rows } = decodeAll(payload);
  assert.equal(header.tool, "search_symbols");
  assert.equal(rows.length, 2);
  assert.equal(rows[0].id, "1");
  assert.equal(rows[0].kind, "func");
  assert.equal(rows[0].name, "Foo");
  assert.equal(rows[1].kind, "method");
});

test("decode iterates multi-section payloads", () => {
  const payload =
    "GCX1 tool=get_callers.nodes fields=id,name\n" +
    "a\tAlpha\n" +
    "b\tBeta\n" +
    "GCX1 tool=get_callers.edges fields=from,to,kind\n" +
    "a\tb\tcalls\n";

  const sections = Array.from(decode(payload));
  assert.equal(sections.length, 2);
  assert.equal(sections[0].header.tool, "get_callers.nodes");
  assert.equal(sections[0].rows.length, 2);
  assert.equal(sections[1].header.tool, "get_callers.edges");
  assert.equal(sections[1].rows[0].kind, "calls");
});

test("decode unescapes tabs and newlines inside cells", () => {
  const src = "func F() {\n\tprintln(\"x\\ty\")\n}";
  const payload =
    "GCX1 tool=get_symbol_source fields=id,source\n" +
    "sym-1\t" +
    src.replace(/\\/g, "\\\\").replace(/\t/g, "\\t").replace(/\n/g, "\\n") +
    "\n";
  const { rows } = decodeAll(payload);
  assert.equal(rows[0].source, src);
});

test("decode rejects rows with too many values", () => {
  const payload =
    "GCX1 tool=x fields=a,b\n" +
    "1\t2\t3\n";
  assert.throws(
    () => Array.from(decode(payload)),
    (err: unknown) => err instanceof RowError,
  );
});

test("decode fills missing trailing columns with empty strings", () => {
  const payload = "GCX1 tool=x fields=a,b,c\n" + "only-a\n";
  const { rows } = decodeAll(payload);
  assert.deepEqual(rows[0], { a: "only-a", b: "", c: "" });
});

test("decode skips comment and blank lines", () => {
  const payload =
    "GCX1 tool=x fields=v\n" +
    "\n" +
    "# note\n" +
    "1\n" +
    "\n" +
    "2\n";
  const { rows } = decodeAll(payload);
  assert.deepEqual(rows, [{ v: "1" }, { v: "2" }]);
});

test("decodeAll rejects multi-section payloads", () => {
  const payload =
    "GCX1 tool=a fields=v\n" +
    "1\n" +
    "GCX1 tool=b fields=v\n" +
    "2\n";
  assert.throws(
    () => decodeAll(payload),
    (err: unknown) => err instanceof RowError,
  );
});

test("decodeAll empty payload throws MalformedHeaderError", () => {
  assert.throws(
    () => decodeAll(""),
    (err: unknown) => err instanceof MalformedHeaderError,
  );
});
