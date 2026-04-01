# frozen_string_literal: true

require "json"
require "fileutils"

module Git
  module Treeline
    # Central registry tracking all port, database, and Redis allocations
    # across every project on this machine. Stored at the platform-appropriate
    # config directory alongside config.json.
    class Registry
      attr_reader :path

      def initialize(path = Platform.registry_file)
        @path = path
      end

      def allocations
        load_data["allocations"]
      end

      def find(worktree_path)
        allocations.find { |a| a["worktree"] == worktree_path }
      end

      def find_by_project(project)
        allocations.select { |a| a["project"] == project }
      end

      def used_ports
        allocations.flat_map { |a| a["ports"] || [a["port"]] }.compact
      end

      def used_redis_dbs
        allocations.map { |a| a["redis_db"] }.compact
      end

      def used_redis_prefixes
        allocations.map { |a| a["redis_prefix"] }.compact
      end

      def allocate(entry)
        with_lock do |data|
          data["allocations"].reject! { |a| a["worktree"] == entry[:worktree] }
          data["allocations"] << normalize_entry(entry)
          data
        end
        entry
      end

      def release(worktree_path)
        removed = false
        with_lock do |data|
          removed = !data["allocations"].reject! { |a| a["worktree"] == worktree_path }.nil?
          data
        end
        removed
      end

      def prune
        count = 0
        with_lock do |data|
          before = data["allocations"].size
          data["allocations"].select! { |a| Dir.exist?(a["worktree"]) }
          count = before - data["allocations"].size
          data
        end
        count
      end

      LOCK_TIMEOUT = 5 # seconds

      private

      def with_lock
        FileUtils.mkdir_p(File.dirname(@path))
        lock_path = "#{@path}.lock"

        File.open(lock_path, File::CREAT | File::RDWR) do |lock_file|
          unless lock_file.flock(File::LOCK_EX | File::LOCK_NB)
            # Another process holds the lock — block with a timeout
            deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + LOCK_TIMEOUT
            loop do
              break if lock_file.flock(File::LOCK_EX | File::LOCK_NB)

              if Process.clock_gettime(Process::CLOCK_MONOTONIC) >= deadline
                raise Error, "Timed out waiting for registry lock (#{lock_path}). " \
                             "If no other git-treeline process is running, remove the lock file."
              end
              sleep 0.1
            end
          end

          data = load_data
          updated = yield data
          save_data(updated)
        end
      end

      def load_data
        return empty_registry unless File.exist?(@path)

        JSON.parse(File.read(@path))
      rescue JSON::ParserError
        empty_registry
      end

      def save_data(data)
        FileUtils.mkdir_p(File.dirname(@path))
        File.write(@path, "#{JSON.pretty_generate(data)}\n")
      end

      def empty_registry
        { "version" => 1, "allocations" => [] }
      end

      def normalize_entry(entry)
        entry.transform_keys(&:to_s).tap do |e|
          e["allocated_at"] ||= Time.now.utc.iso8601
        end
      end
    end
  end
end
