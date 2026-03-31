# frozen_string_literal: true

module Git
  module Treeline
    # Resolves platform-appropriate paths for user-level configuration and data.
    #
    # macOS:   ~/Library/Application Support/git-treeline/
    # Linux:   ~/.config/git-treeline/ (respects XDG_CONFIG_HOME)
    # Windows: %APPDATA%/git-treeline/
    module Platform
      class << self
        def config_dir
          @config_dir ||= File.join(base_dir, "git-treeline")
        end

        def config_file
          File.join(config_dir, "config.json")
        end

        def registry_file
          File.join(config_dir, "registry.json")
        end

        private

        def base_dir
          case host_os
          when /darwin/i
            File.join(Dir.home, "Library", "Application Support")
          when /mswin|mingw|cygwin/i
            ENV.fetch("APPDATA", File.join(Dir.home, "AppData", "Roaming"))
          else
            ENV.fetch("XDG_CONFIG_HOME", File.join(Dir.home, ".config"))
          end
        end

        def host_os
          RbConfig::CONFIG["host_os"]
        end
      end
    end
  end
end
