/**
 * Row values come back as strings — GCX1 is a text format and the
 * decoder does not attempt to infer field types. Callers coerce on
 * demand (e.g. `Number(row.line)`).
 */
export type Row = Record<string, string>;

export interface Header {
  /** Parsed protocol version — always 1 for GCX1 payloads. */
  version: number;
  /** Tool name or dotted sub-section (e.g. "get_callers.edges"). */
  tool: string;
  /** Declared column order for the row stream. */
  fields: string[];
  /** Free-form key/value metadata emitted by the encoder. */
  meta: Record<string, string>;
}

export interface Section {
  header: Header;
  rows: Row[];
}
