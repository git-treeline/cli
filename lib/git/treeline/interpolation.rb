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
        port = allocation[:port] || allocation["port"]
        database = allocation[:database] || allocation["database"]
        redis_prefix = allocation[:redis_prefix] || allocation["redis_prefix"]
        worktree = allocation[:worktree_name] || allocation["worktree_name"]

        pattern
          .gsub("{port}", port.to_s)
          .gsub("{database}", database.to_s)
          .gsub("{redis_url}", redis_url)
          .gsub("{redis_prefix}", redis_prefix.to_s)
          .gsub("{project}", project)
          .gsub("{worktree}", worktree.to_s)
      end
    end
  end
end
