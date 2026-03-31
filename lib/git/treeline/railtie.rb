# frozen_string_literal: true

module Git
  module Treeline
    # Auto-injects allocated environment variables in Rails development.
    # Reads allocation from the central registry and sets ENV vars before
    # other initializers run — so config/database.yml, config/puma.rb,
    # and Redis initializers pick up worktree-specific values automatically.
    class Railtie < Rails::Railtie
      initializer "git_treeline.apply_allocation", before: :load_environment_config do
        next unless Rails.env.development?

        allocation = Git::Treeline.registry.find(Dir.pwd)
        next unless allocation

        user_config = Git::Treeline.user_config
        project_config = Git::Treeline.project_config
        redis_url = Interpolation.build_redis_url(user_config.redis_url, allocation)

        env_vars = {
          "PORT" => allocation["port"]&.to_s,
          "DATABASE_NAME" => allocation["database"],
          "REDIS_URL" => redis_url
        }

        project_config.env_template.each do |key, pattern|
          env_vars[key] = Interpolation.interpolate(
            pattern, allocation: allocation, redis_url: redis_url, project: project_config.project
          )
        end

        env_vars.compact.each do |key, value|
          ENV[key] ||= value
        end
      end
    end
  end
end
