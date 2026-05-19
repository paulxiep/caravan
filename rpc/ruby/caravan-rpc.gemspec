# frozen_string_literal: true

require_relative "lib/caravan/rpc/version"

Gem::Specification.new do |spec|
  spec.name        = "caravan-rpc"
  spec.version     = Caravan::Rpc::VERSION
  spec.authors     = ["Rachapong Chirarattananon"]

  spec.summary     = "Runtime SDK for the Caravan application-definition compiler."
  spec.description = "Pre-release placeholder. The functional SDK lands at 0.1.0; see https://github.com/paulxiep/caravan."
  spec.homepage    = "https://github.com/paulxiep/caravan"
  spec.license     = "Apache-2.0"
  spec.required_ruby_version = ">= 3.0"

  spec.metadata["homepage_uri"]      = spec.homepage
  spec.metadata["source_code_uri"]   = "https://github.com/paulxiep/caravan/tree/main/rpc/ruby"
  spec.metadata["bug_tracker_uri"]   = "https://github.com/paulxiep/caravan/issues"
  spec.metadata["documentation_uri"] = "https://github.com/paulxiep/caravan/blob/main/docs/poc_rpc_sdk.md"

  spec.files         = Dir["lib/**/*.rb", "README.md", "LICENSE"]
  spec.require_paths = ["lib"]
end
