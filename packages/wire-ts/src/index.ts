/**
 * @gortex/wire — decoder for the GCX1 compact wire format used by
 * Gortex MCP tools.
 *
 * See docs/wire-format.md in the gortex repo for the specification.
 */

export { decode, decodeAll, parseHeader } from "./decode.js";
export {
  UnsupportedVersionError,
  MalformedHeaderError,
  RowError,
  WireError,
} from "./errors.js";
export type { Header, Section, Row } from "./types.js";
