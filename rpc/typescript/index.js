// Pre-release placeholder for caravan-rpc.
//
// Runtime SDK for the Caravan application-definition compiler.
// The functional SDK lands at 0.1.0. This 0.0.1 release reserves the npm
// name and provides import-compatible no-op stubs so SDK-wrapped code does
// not fail at require/import.
//
// See https://github.com/paulxiep/caravan for thesis, PoC specs, and roadmap.

"use strict";

const PLACEHOLDER_MSG =
  "caravan-rpc 0.0.1 is a pre-release placeholder. " +
  "The functional SDK lands at 0.1.0; see https://github.com/paulxiep/caravan.";

const version = "0.0.1";

function defineWagon(name) {
  return Symbol.for(`caravan-rpc:wagon:${name}`);
}

function provide(_token, _impl) {
  /* placeholder no-op */
}

function client(_token) {
  throw new Error(PLACEHOLDER_MSG);
}

module.exports = { version, defineWagon, provide, client };
