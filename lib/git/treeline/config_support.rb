# frozen_string_literal: true

module Git
  module Treeline
    # Shared helpers for configuration classes that load JSON/YAML with defaults.
    module ConfigSupport
      private

      def deep_merge(base, override)
        base.each_with_object(override.dup) do |(k, v), merged|
          merged[k] = if v.is_a?(Hash) && merged[k].is_a?(Hash)
                        deep_merge(v, merged[k])
                      else
                        merged.key?(k) ? merged[k] : v
                      end
        end
      end

      def config_dig(hash, *keys)
        keys.reduce(hash) { |h, k| h.is_a?(Hash) ? h[k] : nil }
      end
    end
  end
end
