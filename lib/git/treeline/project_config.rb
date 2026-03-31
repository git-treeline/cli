# frozen_string_literal: true

require "yaml"

module Git
  module Treeline
    # Per-repo configuration from .treeline.yml.
    # Describes what the project needs — database template, files to copy,
    # setup commands, env var mappings. Does NOT govern allocation policy
    # (ports, Redis strategy) — that lives in UserConfig.
    #
    # Example .treeline.yml:
    #
    #   project: salt
    #
    #   env_file:
    #     target: .env.local          # file written in the worktree
    #     source: .env.local          # file copied from main repo (falls back to .env)
    #
    #   database:
    #     adapter: postgresql
    #     template: salt_development
    #     pattern: "{template}_{worktree}"
    #
    #   copy_files:
    #     - config/master.key
    #
    #   env:
    #     PORT: "{port}"
    #     DATABASE_NAME: "{database}"
    #     REDIS_URL: "{redis_url}"
    #     APPLICATION_HOST: "localhost:{port}"
    #
    #   setup_commands:
    #     - bundle install --quiet
    #     - yarn install --silent
    #
    #   editor:
    #     vscode_title: "{project} (:{port}) — {branch} — ${activeEditorShort}"
    #
    class ProjectConfig
      include ConfigSupport

      DEFAULTS = {
        "env_file" => {
          "target" => ".env.local",
          "source" => ".env.local"
        },
        "database" => {
          "adapter" => "postgresql",
          "template" => nil,
          "pattern" => "{template}_{worktree}"
        },
        "copy_files" => [],
        "env" => {},
        "setup_commands" => [],
        "editor" => {}
      }.freeze

      attr_reader :project_root, :data

      def initialize(project_root)
        @project_root = project_root
        @data = load_config
      end

      def project
        data["project"] || File.basename(project_root)
      end

      def database_adapter
        config_dig(data, "database", "adapter")
      end

      def database_template
        config_dig(data, "database", "template")
      end

      def database_pattern
        config_dig(data, "database", "pattern")
      end

      def env_file_target
        config_dig(data, "env_file", "target")
      end

      def env_file_source
        config_dig(data, "env_file", "source")
      end

      def copy_files
        data["copy_files"] || DEFAULTS["copy_files"]
      end

      def env_template
        data["env"] || DEFAULTS["env"]
      end

      def setup_commands
        data["setup_commands"] || DEFAULTS["setup_commands"]
      end

      def editor
        data["editor"] || DEFAULTS["editor"]
      end

      def config_file_exists?
        File.exist?(config_path)
      end

      private

      def config_path
        File.join(project_root, PROJECT_CONFIG_FILE)
      end

      def load_config
        return DEFAULTS.dup unless File.exist?(config_path)

        yaml = YAML.safe_load_file(config_path) || {}
        deep_merge(DEFAULTS, yaml)
      end
    end
  end
end
