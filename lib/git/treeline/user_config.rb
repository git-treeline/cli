# frozen_string_literal: true

require "json"
require "fileutils"

module Git
  module Treeline
    # User-level configuration at the platform-appropriate config directory.
    # Governs allocation policy for the machine — port ranges, Redis strategy, etc.
    #
    # Example config.json:
    #
    #   {
    #     "port": {
    #       "base": 3000,
    #       "increment": 10
    #     },
    #     "redis": {
    #       "strategy": "prefixed",
    #       "url": "redis://localhost:6379"
    #     }
    #   }
    #
    class UserConfig
      include ConfigSupport

      DEFAULTS = {
        "port" => { "base" => 3000, "increment" => 10 },
        "redis" => { "strategy" => "prefixed", "url" => "redis://localhost:6379" }
      }.freeze

      attr_reader :data

      def initialize(path = Platform.config_file)
        @path = path
        @data = load_config
      end

      def port_base
        config_dig(data, "port", "base")
      end

      def port_increment
        config_dig(data, "port", "increment")
      end

      def redis_strategy
        config_dig(data, "redis", "strategy")
      end

      def redis_url
        config_dig(data, "redis", "url")
      end

      def config_file_exists?
        File.exist?(@path)
      end

      def init!
        FileUtils.mkdir_p(File.dirname(@path))
        File.write(@path, "#{JSON.pretty_generate(DEFAULTS)}\n")
      end

      private

      def load_config
        return DEFAULTS.dup unless File.exist?(@path)

        user_data = JSON.parse(File.read(@path))
        deep_merge(DEFAULTS, user_data)
      rescue JSON::ParserError
        DEFAULTS.dup
      end
    end
  end
end
