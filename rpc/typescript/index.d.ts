/**
 * caravan-rpc — runtime SDK for the Caravan application-definition compiler.
 *
 * 0.0.1 is a pre-release placeholder. The functional SDK lands at 0.1.0.
 * See https://github.com/paulxiep/caravan
 */

export declare const version: string;

/**
 * Declare a Caravan seam interface as a runtime token.
 *
 * The real implementation associates the token with the TS interface for
 * client-side dispatch. In 0.0.1 it returns a symbol; in 0.1.0 it will
 * also register the method signatures for JSON (de)serialization.
 */
export declare function defineWagon<T>(name: string): symbol;

/**
 * Register an impl for a previously-declared wagon token.
 *
 * 0.0.1 is a no-op. The real implementation registers the impl in the
 * SDK's inproc registry.
 */
export declare function provide(token: symbol, impl: unknown): void;

/**
 * Return a dispatcher proxy for a previously-declared wagon token.
 *
 * 0.0.1 throws — the SDK is not yet functional. The real implementation
 * returns a Proxy that intercepts method calls and dispatches per
 * `CARAVAN_RPC_PEERS` env config.
 */
export declare function client<T>(token: symbol): T;
