# frozen_string_literal: true

require "rbconfig"
require_relative "treeline/version"
require_relative "treeline/platform"
require_relative "treeline/config_support"
require_relative "treeline/interpolation"
require_relative "treeline/user_config"
require_relative "treeline/project_config"
require_relative "treeline/registry"
require_relative "treeline/allocator"
require_relative "treeline/setup"

module Git
  module Treeline
    class Error < StandardError; end

    # Default config filename in project repos
    PROJECT_CONFIG_FILE = ".treeline.yml"

    class << self
      def user_config
        @user_config ||= UserConfig.new
      end

      def registry
        @registry ||= Registry.new
      end

      def reset!
        @user_config = nil
        @registry = nil
      end

      def project_config(project_root = nil)
        root = project_root || detect_project_root
        ProjectConfig.new(root)
      end

      def detect_project_root
        dir = Dir.pwd
        loop do
          return dir if File.exist?(File.join(dir, ".git")) || File.exist?(File.join(dir, PROJECT_CONFIG_FILE))

          parent = File.dirname(dir)
          return Dir.pwd if parent == dir

          dir = parent
        end
      end

      def detect_worktree_info
        main_repo = `git worktree list --porcelain 2>/dev/null`.lines.first&.sub("worktree ", "")&.strip
        current = Dir.pwd
        is_worktree = main_repo && current != main_repo
        branch = `git rev-parse --abbrev-ref HEAD 2>/dev/null`.strip
        name = File.basename(current)

        {
          main_repo: main_repo,
          worktree_path: current,
          worktree_name: name,
          branch: branch,
          is_worktree: is_worktree
        }
      end
    end
  end
end

require_relative "treeline/railtie" if defined?(Rails::Railtie)
