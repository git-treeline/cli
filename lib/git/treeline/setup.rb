# frozen_string_literal: true

require "fileutils"

module Git
  module Treeline
    # Orchestrates the full worktree setup:
    #   1. Allocate resources (port, database, Redis) via user-level policy
    #   2. Register the allocation
    #   3. Copy files from main repo
    #   4. Write .env.local with allocated values
    #   5. Clone the database (if configured)
    #   6. Run setup commands
    #   7. Configure editor settings
    class Setup
      attr_reader :user_config, :project_config, :registry, :allocator, :worktree_path, :main_repo

      def initialize(worktree_path:, main_repo: nil)
        @worktree_path = File.expand_path(worktree_path)
        @main_repo = main_repo || detect_main_repo
        @user_config = Git::Treeline.user_config
        @project_config = Git::Treeline.project_config(@main_repo)
        @registry = Git::Treeline.registry
        @allocator = Allocator.new(user_config: @user_config, project_config: @project_config, registry: @registry)
      end

      def run
        worktree_name = File.basename(worktree_path)
        allocation = allocator.allocate(worktree_path: worktree_path, worktree_name: worktree_name)
        redis_url = allocator.build_redis_url(allocation)

        log "Allocating port #{allocation[:port]} for '#{worktree_name}'"
        log "Database: #{allocation[:database]}" if allocation[:database]
        log "Redis: #{redis_url}"

        registry.allocate(allocation)
        copy_files
        env_vars = build_env_vars(allocation, redis_url)
        write_env_file(env_vars)
        clone_database(allocation[:database]) if allocation[:database]
        run_setup_commands
        configure_editor(allocation)

        log ""
        log "Done! Worktree '#{worktree_name}' ready:"
        log "  Port:     #{allocation[:port]}"
        log "  Database: #{allocation[:database]}" if allocation[:database]
        log "  Redis:    #{redis_url}"
        log "  URL:      http://localhost:#{allocation[:port]}"
        log "  Dir:      #{worktree_path}"

        allocation
      end

      private

      def detect_main_repo
        result = Dir.chdir(worktree_path) { `git worktree list --porcelain 2>/dev/null` }
        result.lines.first&.sub("worktree ", "")&.strip || worktree_path
      end

      def copy_files
        project_config.copy_files.each do |file|
          src = File.join(main_repo, file)
          dest = File.join(worktree_path, file)
          next unless File.exist?(src)

          FileUtils.mkdir_p(File.dirname(dest))
          FileUtils.cp(src, dest)
          log "Copied #{file}"
        end
      end

      def build_env_vars(allocation, redis_url)
        project_config.env_template.each_with_object({}) do |(key, pattern), vars|
          vars[key] = Interpolation.interpolate(
            pattern, allocation: allocation, redis_url: redis_url, project: project_config.project
          )
        end
      end

      def write_env_file(vars)
        target = project_config.env_file_target
        env_path = File.join(worktree_path, target)

        # Copy source env file from main repo if available
        source = File.join(main_repo, project_config.env_file_source)
        source = File.join(main_repo, ".env") unless File.exist?(source)
        FileUtils.cp(source, env_path) if File.exist?(source)

        # Apply overrides
        vars.each { |key, value| update_or_append(env_path, key, value) }
        log "#{target} written"
      end

      def update_or_append(file, key, value)
        FileUtils.touch(file) unless File.exist?(file)
        content = File.read(file)

        if content.match?(/^#{Regexp.escape(key)}=/)
          content.sub!(/^#{Regexp.escape(key)}=.*/, "#{key}=\"#{value}\"")
        else
          content += "\n" unless content.end_with?("\n") || content.empty?
          content += "#{key}=\"#{value}\"\n"
        end

        File.write(file, content)
      end

      def clone_database(db_name)
        return unless project_config.database_adapter == "postgresql"
        return unless project_config.database_template

        template = project_config.database_template
        validate_db_identifier!(db_name)
        validate_db_identifier!(template)

        existing = `psql -lqt 2>/dev/null`.split("\n").any? { |l| l.split("|").first&.strip == db_name }
        if existing
          log "Database #{db_name} already exists, skipping"
          return
        end

        log "Terminating connections to #{template}"
        # Safe to interpolate: validate_db_identifier! ensures [a-zA-Z_][a-zA-Z0-9_]* only
        system(
          "psql", "-d", "postgres",
          "-c", "SELECT pg_terminate_backend(pid) FROM pg_stat_activity " \
                "WHERE datname = '#{template}' AND pid <> pg_backend_pid();",
          out: File::NULL, err: File::NULL
        )

        log "Cloning database #{template} -> #{db_name}"
        unless system("createdb", db_name, "--template", template)
          raise Error, "Failed to clone database #{template} -> #{db_name}"
        end

        log "Database cloned"
      end

      def validate_db_identifier!(name)
        return if name.match?(/\A[a-zA-Z_][a-zA-Z0-9_]*\z/)

        raise Error, "Invalid database identifier: #{name.inspect}. " \
                     "Must contain only letters, digits, and underscores."
      end

      def run_setup_commands
        return if project_config.setup_commands.empty?

        Dir.chdir(worktree_path) do
          project_config.setup_commands.each do |cmd|
            log "Running: #{cmd}"
            raise Error, "Setup command failed: #{cmd}" unless system(cmd)
          end
        end
      end

      def configure_editor(allocation)
        title_template = project_config.editor["vscode_title"]
        return unless title_template

        branch = Dir.chdir(worktree_path) { `git rev-parse --abbrev-ref HEAD 2>/dev/null`.strip }
        title = title_template
                .gsub("{project}", project_config.project)
                .gsub("{port}", allocation[:port].to_s)
                .gsub("{branch}", branch)

        vscode_dir = File.join(worktree_path, ".vscode")
        FileUtils.mkdir_p(vscode_dir)
        File.write(File.join(vscode_dir, "settings.json"), "#{JSON.pretty_generate("window.title" => title)}\n")
        log ".vscode/settings.json written"
      end

      def log(message)
        $stdout.puts "==> #{message}" unless message.empty?
      end
    end
  end
end
