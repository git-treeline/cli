# frozen_string_literal: true

module Git
  module Treeline
    # Shared logic for resolving Redis URLs and interpolating env var templates.
    # Used by both Setup (CLI-time) and Railtie (Rails boot-time).
    module Interpolation
      module_function

      def build_redis_url(redis_base_url, allocation)
        base = redis_base_url.chomp("/")
        redis_db = allocation[:redis_db] || allocation["redis_db"]
        redis_db ? "#{base}/#{redis_db}" : base
      end

      def interpolate(pattern, allocation:, redis_url:, project:)
        tokens = build_token_map(allocation, redis_url, project)
        tokens.reduce(pattern) { |result, (token, value)| result.gsub(token, value) }
      end

      def build_token_map(allocation, redis_url, project)
        tokens = {
          "{port}" => fetch(allocation, :port).to_s,
          "{database}" => fetch(allocation, :database).to_s,
          "{redis_url}" => redis_url,
          "{redis_prefix}" => fetch(allocation, :redis_prefix).to_s,
          "{project}" => project,
          "{worktree}" => fetch(allocation, :worktree_name).to_s
        }

        ports = fetch(allocation, :ports)
        Array(ports).each_with_index { |p, i| tokens["{port_#{i + 1}}"] = p.to_s }

        tokens
      end

      def fetch(hash, sym_key)
        hash[sym_key] || hash[sym_key.to_s]
      end
    end
  end
end
