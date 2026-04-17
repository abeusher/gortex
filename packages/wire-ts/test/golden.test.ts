import { strict as assert } from "node:assert";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { test } from "node:test";

import { decode } from "../dist/index.js";

/**
 * Consumes GCX fixtures produced by the Go-side generator
 * (`bench/wire-format` or `internal/mcp` tests). This test pins
 * cross-implementation parity — if the Go encoder ever changes
 * shape, the TS decoder must still round-trip every fixture.
 */

const here = fileURLToPath(new URL(".", import.meta.url));
const fixturePath = (name: string) => `${here}/golden/${name}`;

test("golden: search_symbols 5-row fixture", async () => {
  const payload = await readFile(fixturePath("search_symbols.gcx"), "utf8");
  const [section] = Array.from(decode(payload));
  assert.equal(section.header.tool, "search_symbols");
  assert.deepEqual(section.header.fields, [
    "id",
    "kind",
    "name",
    "path",
    "line",
    "sig",
  ]);
  assert.equal(section.rows.length, 5);
  assert.equal(section.rows[0].id, "a.go::Foo");
  assert.equal(section.rows[0].sig, "func Foo()");
  assert.equal(section.rows[4].name, "Quux");
});

test("golden: get_callers multi-section fixture", async () => {
  const payload = await readFile(fixturePath("get_callers.gcx"), "utf8");
  const sections = Array.from(decode(payload));
  assert.equal(sections.length, 2);
  assert.equal(sections[0].header.tool, "get_callers.nodes");
  assert.equal(sections[1].header.tool, "get_callers.edges");
  assert.equal(sections[0].rows.length, 2);
  assert.equal(sections[1].rows.length, 1);
  assert.equal(sections[1].rows[0].origin, "ast_resolved");
});

test("golden: get_symbol_source round-trips newlines and tabs", async () => {
  const payload = await readFile(fixturePath("get_symbol_source.gcx"), "utf8");
  const [section] = Array.from(decode(payload));
  const src = section.rows[0].source;
  assert.ok(src.includes("\n"), "source should contain real newlines after decoding");
  assert.ok(src.includes("\t"), "source should contain a real tab after decoding");
  assert.equal(section.header.meta.etag, "etag-test");
});
