# frozen_string_literal: true

module Git
  module Treeline
    # Allocates ports and Redis resources from the central registry.
    # Port ranges and Redis strategy come from UserConfig (machine-level).
    # Database naming comes from ProjectConfig (repo-level).
    class Allocator
      MAX_REDIS_DBS = 16 # Redis default

      attr_reader :user_config, :project_config, :registry

      def initialize(project_config:, user_config: Git::Treeline.user_config, registry: Git::Treeline.registry)
        @user_config = user_config
        @project_config = project_config
        @registry = registry
      end

      def allocate(worktree_path:, worktree_name:)
        port = next_available_port
        redis = allocate_redis(worktree_name)
        database = build_database_name(worktree_name)

        {
          project: project_config.project,
          worktree: worktree_path,
          worktree_name: worktree_name,
          port: port,
          database: database,
          **redis
        }
      end

      def next_available_port
        used = registry.used_ports
        port = user_config.port_base + user_config.port_increment

        port += user_config.port_increment while used.include?(port)
        port
      end

      def allocate_redis(worktree_name)
        case user_config.redis_strategy
        when "database"
          { redis_db: next_available_redis_db, redis_prefix: nil }
        else # "prefixed" is the default
          { redis_db: nil, redis_prefix: "#{project_config.project}:#{worktree_name}" }
        end
      end

      def next_available_redis_db
        used = registry.used_redis_dbs
        # DB 0 is typically the default/main, start from 1
        db = 1
        db += 1 while used.include?(db)

        if db >= MAX_REDIS_DBS
          raise Error, "No available Redis databases (0-#{MAX_REDIS_DBS - 1} all allocated). " \
                       "Consider switching to redis.strategy: prefixed in your config.json"
        end

        db
      end

      def build_database_name(worktree_name)
        return nil unless project_config.database_template

        project_config.database_pattern
                      .gsub("{template}", project_config.database_template)
                      .gsub("{worktree}", sanitize_name(worktree_name))
                      .gsub("{project}", project_config.project)
      end

      def build_redis_url(allocation)
        Interpolation.build_redis_url(user_config.redis_url, allocation)
      end

      private

      def sanitize_name(name)
        name.gsub(/[^a-zA-Z0-9_]/, "_").gsub(/_+/, "_").gsub(/\A_|_\z/, "")
      end
    end
  end
end
