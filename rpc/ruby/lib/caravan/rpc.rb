# frozen_string_literal: true

require_relative "rpc/version"

# Pre-release placeholder for the Caravan Ruby SDK.
#
# Runtime SDK for the Caravan application-definition compiler.
# The functional SDK lands at 0.1.0. This 0.0.1 release reserves the
# RubyGems name and provides import-compatible no-op stubs so SDK-wrapped
# code does not crash at require.
#
# See https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md
# for the SDK spec.
module Caravan
  module Rpc
    PLACEHOLDER_MSG =
      "caravan-rpc 0.0.1 is a pre-release placeholder. " \
      "The functional SDK lands at 0.1.0; see https://github.com/paulxiep/caravan."

    # Pre-release identity decorator.
    #
    # The real +wagon+ marks a class as a seam for the Caravan compiler.
    # In 0.0.1 it returns the class unchanged so SDK-wrapped code requires
    # cleanly.
    def self.wagon(klass)
      klass
    end

    # Pre-release no-op.
    #
    # The real +provide+ registers an impl for an interface in the SDK's
    # inproc registry. In 0.0.1 it does nothing.
    def self.provide(_iface, _impl)
      nil
    end

    # Pre-release stub.
    #
    # The real +client+ returns a dispatcher proxy reading +CARAVAN_RPC_PEERS+
    # from env. In 0.0.1 it raises +NotImplementedError+ — the SDK is not
    # yet functional.
    def self.client(_iface)
      raise NotImplementedError, PLACEHOLDER_MSG
    end
  end
end
