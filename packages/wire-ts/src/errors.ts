export class WireError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "WireError";
  }
}

/**
 * Thrown when the payload begins with a version prefix other than the
 * one this decoder understands (currently GCX1). Callers should
 * retry the MCP call without `format:"gcx"` to get a JSON response.
 */
export class UnsupportedVersionError extends WireError {
  constructor(public readonly got: string) {
    super(`unsupported wire-format version: ${got}`);
    this.name = "UnsupportedVersionError";
  }
}

export class MalformedHeaderError extends WireError {
  constructor(message: string, public readonly line: string) {
    super(message);
    this.name = "MalformedHeaderError";
  }
}

export class RowError extends WireError {
  constructor(message: string, public readonly line: string) {
    super(message);
    this.name = "RowError";
  }
}
